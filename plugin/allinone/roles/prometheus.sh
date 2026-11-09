PROM_SERVER_DIR=/usr/local/prometheus/server

# ============ 幂等化：每次启动从纯净基线重建可变文件 ============
# 本脚本会向 prometheus.yml 追加 k8s job、向 <job>.yaml 追加 file_sd target。
# docker restart 复用同一写层,若不先还原会重复追加 —— prometheus.yml 出现重名 job
# 会直接启动失败(线上踩过),job 文件则 target 累积。故:首次把镜像里的纯净
# prometheus.yml 存为 .base,之后每次启动都从 .base 还原,使 restart 与 recreate 行为一致。
PROM_CFG="$PROM_SERVER_DIR/prometheus.yml"
[ -f "$PROM_CFG.base" ] || cp "$PROM_CFG" "$PROM_CFG.base"
cp "$PROM_CFG.base" "$PROM_CFG"

# ============ 采集目标：数据源注册中心驱动 ============
# 采集目标由数据源注册中心（UI「数据源」页）驱动：后台循环 curl ops-server 的
# file_sd YAML 写回同名 <job>.yaml，prometheus.yml 的 file_sd_configs 不变、自动热加载。
# host 也走 SD 拉取（promfile/host）。
#
# 1.5.0 移除了 env 模式（host + 中间件 file_sd 由 ENV_PROMETHEUS_<TYPE> 一次性渲染）：
# 它与 UI 并存时是第二个入口，配错零反馈、改了不生效，是最难排查的一类问题。
ENV_OPS_SD_URL="${ENV_OPS_SD_URL:=http://gateway:8081}"
# host 与中间件一起从 promfile 拉取（host.yaml 由 promfile/host 生成）
SD_JOBS="host kafka elasticsearch redis mysql mongodb"

# 下面这层 { } 是原 if http/else env 分支的残留。刻意保留而不是把内容左移一级：
# 块内有多处 heredoc（追加 k8s job 到 prometheus.yml），heredoc 里的缩进会原样
# 写进 YAML，整体 dedent 会改坏生成的配置。
{
    # token 拼到 query（ENV_OPS_SD_TOKEN 为空则不带）
    _tok=""
    [ -n "$ENV_OPS_SD_TOKEN" ] && _tok="?token=${ENV_OPS_SD_TOKEN}"
    # 单次刷新：每个 job 拉一次 file_sd YAML 原子写回 <job>.yaml
    refresh_http_sd() {
        local job url tmp
        for job in $SD_JOBS; do
            url="${ENV_OPS_SD_URL}/api/sd/promfile/${job}${_tok}"
            tmp="$PROM_SERVER_DIR/${job}.yaml.tmp"
            if curl -sf -m 10 "$url" -o "$tmp" 2>/dev/null; then
                mv "$tmp" "$PROM_SERVER_DIR/${job}.yaml"
            else
                rm -f "$tmp"
                # 拉取失败保留上次的 <job>.yaml，不清空（避免抖动导致 target 全掉）
                echo "$(date +%F\ %T) - [sd] 拉取 $job 失败，沿用上次 file_sd"
            fi
        done
    }
    echo "$(date +%F\ %T) - [sd] http 模式：从 ${ENV_OPS_SD_URL} 拉取 host+中间件 file_sd"
    refresh_http_sd   # 启动前先写一次，保证首次抓取即有 target
    ( while true; do
        sleep 30
        refresh_http_sd
      done ) &
}

# ============ K8s 监控 ============
# ENV_K8S_MONITOR_MODE: off(默认) | incluster
#   incluster: ops-prometheus 作为 Pod 跑在被监控 k8s 内，用 SA + kubernetes_sd_configs 自动发现
# 1.5.3 移除 static（集群外 + Bearer Token + NodePort）：采集 K8s 一律要求 MDIS 部署在
# 被监控集群内。删除前实现见 tag archive/k8s-static-mode。
ENV_K8S_MONITOR_MODE="${ENV_K8S_MONITOR_MODE:=off}"
PROM_DIR=/usr/local/prometheus/server

case "$ENV_K8S_MONITOR_MODE" in
  incluster)
    # 追加 kubernetes_sd_configs job 段；经 apiserver proxy 抓 kubelet/cadvisor，避开 kubelet 直连鉴权
    SA=/var/run/secrets/kubernetes.io/serviceaccount
    KSM_NS="${ENV_K8S_KSM_NAMESPACE:=mdis-monitoring}"
    # 集群标识：下面每个 job 的 relabel_configs 会把它注入成真实 label(见文末说明)，
    # heredoc 未加引号故此处必须先于 heredoc 定义。
    _CLUSTER="${ENV_K8S_CLUSTER_NAME:-default}"
    cat >> $PROM_DIR/prometheus.yml <<EOF

  # --- K8s monitoring (incluster mode, auto-appended) ---
  - job_name: "k8s-kubelet"
    scheme: https
    tls_config:
      ca_file: $SA/ca.crt
      insecure_skip_verify: true
    bearer_token_file: $SA/token
    kubernetes_sd_configs:
      - role: node
    relabel_configs:
      - action: labelmap
        regex: __meta_kubernetes_node_label_(.+)
      - target_label: __address__
        replacement: kubernetes.default.svc:443
      - source_labels: [__meta_kubernetes_node_name]
        regex: (.+)
        target_label: __metrics_path__
        replacement: /api/v1/nodes/\${1}/proxy/metrics
      # 节点名注入 node label：经 apiserver proxy 抓取时 __address__ 被统一改写成
      # kubernetes.default.svc:443，所有节点的 instance 完全相同，不注入就无从区分节点
      - source_labels: [__meta_kubernetes_node_name]
        target_label: node
      # 放最后：labelmap 若从节点标签映出同名 label，这里以集群标识为准
      - target_label: origin_prometheus
        replacement: "$_CLUSTER"

  - job_name: "k8s-cadvisor"
    scheme: https
    tls_config:
      ca_file: $SA/ca.crt
      insecure_skip_verify: true
    bearer_token_file: $SA/token
    kubernetes_sd_configs:
      - role: node
    relabel_configs:
      - action: labelmap
        regex: __meta_kubernetes_node_label_(.+)
      - target_label: __address__
        replacement: kubernetes.default.svc:443
      - source_labels: [__meta_kubernetes_node_name]
        regex: (.+)
        target_label: __metrics_path__
        replacement: /api/v1/nodes/\${1}/proxy/metrics/cadvisor
      # cadvisor 指标自身只带 pod/namespace/container/id，不带 node；而 K8s 看板里
      # container_* 与 kube_node_status_allocatable(来自 ksm，带 node) 做 by(node) 除法，
      # 不注入则一侧 node="" 另一侧为真实节点名，向量匹配不上 → 整块面板 No data
      - source_labels: [__meta_kubernetes_node_name]
        target_label: node
      - target_label: origin_prometheus
        replacement: "$_CLUSTER"

  - job_name: "k8s-kube-state-metrics"
    kubernetes_sd_configs:
      - role: endpoints
        namespaces:
          names: [$KSM_NS]
    relabel_configs:
      - source_labels: [__meta_kubernetes_service_name]
        regex: kube-state-metrics
        action: keep
      # 只保留 metrics 端口(8080)，丢弃 endpoints role 额外发现的 telemetry 端口(8081)
      - source_labels: [__meta_kubernetes_endpoint_port_name]
        regex: http-metrics
        action: keep
      - target_label: origin_prometheus
        replacement: "$_CLUSTER"

  - job_name: "k8s-pods"
    kubernetes_sd_configs:
      - role: pod
    relabel_configs:
      - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_scrape]
        action: keep
        regex: "true"
      - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_path]
        action: replace
        target_label: __metrics_path__
        regex: (.+)
      - source_labels: [__address__, __meta_kubernetes_pod_annotation_prometheus_io_port]
        action: replace
        regex: ([^:]+)(?::\d+)?;(\d+)
        replacement: \$1:\$2
        target_label: __address__
      - source_labels: [__meta_kubernetes_namespace]
        action: replace
        target_label: namespace
      - source_labels: [__meta_kubernetes_pod_name]
        action: replace
        target_label: pod
      - target_label: origin_prometheus
        replacement: "$_CLUSTER"
EOF

    # ---- 集群标识 origin_prometheus ----
    # K8s 面板 15 个 panel 全带 origin_prometheus=~"$origin_prometheus"，下拉变量取自
    # label_values(kube_node_info, origin_prometheus)，所以这个标签必须真实存在于样本上。
    #
    # ⚠️ 1.5.7 曾只写 external_labels 就以为修好了，实测无效：external_labels 按 Prometheus
    # 的设计只在「与外部系统通信」时附加(remote_write / 联邦 / 发给 Alertmanager)，不写进
    # 本地 TSDB。平台自采本集群走的是本地存储，label_values 因此恒返回空、下拉选不到任何
    # 集群，15 个图全空 —— 与 1.5.7 想修的症状一模一样。真正的修法是上面每个 K8s job
    # 的 relabel_configs 末尾静态注入 target_label，让标签进 TSDB。
    #
    # external_labels 仍保留：remote_write 采集组件模式下，它负责给推出去的样本盖上集群标识
    # (值与 relabel 注入的一致，series 自带 label 优先，不冲突)，同时让告警通知带上集群名。
    awk -v cn="$_CLUSTER" '
      /^global:/ && !ins { print; print "  external_labels:"; print "    origin_prometheus: \"" cn "\""; ins=1; next }
      { print }
    ' "$PROM_CFG" > "$PROM_CFG.tmp" && mv "$PROM_CFG.tmp" "$PROM_CFG"
    echo "$(date +%F\ %T) - [k8s] incluster 采集已启用，集群标识 origin_prometheus=\"$_CLUSTER\"（已注入各 K8s job 的 relabel_configs）"

    # ---- 采集组件模式：把 K8s 指标推回主平台 ----
    # 仅用于「平台不在这个集群里」的场景：被监控集群只装 Prometheus+RBAC+kube-state-metrics
    # 当采集组件，数据靠 remote_write 推回平台。相比已废弃的 static 模式：
    #   · 被监控集群不必暴露 apiserver / kubelet / ksm 任何端口，只需能出站
    #   · Bearer Token 只在集群内本地用，不跨集群传递
    #   · 天然支持多集群 —— 每个集群装一份，靠 origin_prometheus 区分
    # ⚠️ 平台自身就在被监控集群内时不要配这个变量：数据本来就落本地 TSDB，
    # 配了等于自己推给自己，白白多一跳。
    if [ -n "${ENV_PROMETHEUS_REMOTE_WRITE_URL:-}" ]; then
      cat >> "$PROM_CFG" <<EOF

# --- 推回主平台（采集组件模式，auto-appended）---
remote_write:
  - url: "$ENV_PROMETHEUS_REMOTE_WRITE_URL"
EOF
      # 走平台网关时要带 token：网关那条 write 入口用 Bearer 鉴权（浏览器会话 Cookie
      # 这边带不了）。不填则不写 authorization 段——直连 Prometheus 端口的场景不需要。
      if [ -n "${ENV_PROMETHEUS_REMOTE_WRITE_TOKEN:-}" ]; then
        cat >> "$PROM_CFG" <<EOF
    authorization:
      type: Bearer
      credentials: "$ENV_PROMETHEUS_REMOTE_WRITE_TOKEN"
EOF
      fi
      cat >> "$PROM_CFG" <<EOF
    # 只推 K8s 相关指标：本机 prometheus 自身的 series 推过去没用，白占主平台存储
    write_relabel_configs:
      - source_labels: [__name__]
        regex: "kube_.*|container_.*|kubelet_.*|machine_.*|node_.*"
        action: keep
EOF
      echo "$(date +%F\ %T) - [k8s] 采集组件模式：指标将推送至 $ENV_PROMETHEUS_REMOTE_WRITE_URL"
      # 平台 Prometheus 跑在 --web.external-url=/prometheus/server/ 下，写入端点也在这个
      # 前缀里；漏了前缀会 404，而 Prometheus 只在自己日志里重试、UI 上只表现为「没数据」。
      # 这里直接判掉，省得对着空面板排查。
      case "$ENV_PROMETHEUS_REMOTE_WRITE_URL" in
        */prometheus/server/api/v1/write) : ;;
        *) echo "$(date +%F\ %T) - [k8s] ⚠️ ENV_PROMETHEUS_REMOTE_WRITE_URL 看着不对：写入端点必须以 /prometheus/server/api/v1/write 结尾（平台网关是 http://<平台地址>:<NodePort>/mdis/prometheus/server/api/v1/write），当前填的是 $ENV_PROMETHEUS_REMOTE_WRITE_URL" ;;
      esac
      # 经网关（带子路径）却没给 token，必然 401。同样只在日志里可见，提前点破。
      case "$ENV_PROMETHEUS_REMOTE_WRITE_URL" in
        *:9090/prometheus/server/*) : ;;   # 直连 Prometheus 端口，无需 token
        *) [ -z "${ENV_PROMETHEUS_REMOTE_WRITE_TOKEN:-}" ] && \
             echo "$(date +%F\ %T) - [k8s] ⚠️ 看着是经平台网关推送，但没设 ENV_PROMETHEUS_REMOTE_WRITE_TOKEN——网关会返回 401，指标进不去。填平台的 ENV_OPS_TOKEN 即可。" ;;
      esac
    fi
    ;;
  off|*)
    : # 不启用 k8s 监控
    ;;
esac

# 设置 Prometheus 保留策略
# - 时间保留 ENV_PROMETHEUS_RETENTION（默认 15d）
# - 大小保留 ENV_PROMETHEUS_RETENTION_SIZE（默认空=不限；设如 "80GB" 后超限自动删最旧 block，
#   防止本地 PVC 被撑满。二者先到者触发。Prometheus 按 base-2 解析，80GB = 80GiB）
export ENV_PROMETHEUS_RETENTION="${ENV_PROMETHEUS_RETENTION:=15d}"
RETENTION_SIZE_FLAG=""
if [ -n "${ENV_PROMETHEUS_RETENTION_SIZE}" ]; then
  RETENTION_SIZE_FLAG="--storage.tsdb.retention.size=${ENV_PROMETHEUS_RETENTION_SIZE}"
fi

# 启动 Prometheus
exec /usr/local/prometheus/server/prometheus --config.file=/usr/local/prometheus/server/prometheus.yml \
  --storage.tsdb.path=/data/mdis/prometheus \
  --web.external-url=/prometheus/server/ \
  --storage.tsdb.retention.time=${ENV_PROMETHEUS_RETENTION} \
  ${RETENTION_SIZE_FLAG} \
  --web.enable-remote-write-receiver \
  --enable-feature=exemplar-storage,native-histograms
