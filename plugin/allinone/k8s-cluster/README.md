# MDIS 集群模式部署（incluster）

整套 MDIS 部署进**被监控的 K8s 集群内**，Prometheus 用 ServiceAccount 自动发现集群自身。

首次实战：2026-07-14 部署到 Nocoly 香港生产集群（腾讯云 TKE，6 节点，227 个业务 Pod）。

> 📖 **面向客户的完整版**（含 kubeadm 拉集群、StorageClass 准备、对象存储接入）见文档中心：
> <https://docs-pd.mingdao.com/deployment/ops/deployment/kubernetes-cluster>
> 本文保留实现细节与踩坑记录，两边如有出入以文档中心为准。

| | `../k8s/` | `./k8s-cluster/`（本目录） |
|---|---|---|
| MDIS 主体在哪 | 集群外（单机 compose） | **集群内** |
| K8s 监控模式 | static（5 步，要 bearer token + 3 个 NodePort） | **incluster（2 步，零 token）** |
| 有状态存储 | — | **StatefulSet + PVC** |

---

## 与 docs-pd 官方 K8s 文档的四处偏离

官方那套在云 K8s 上有真问题，以下每一条都是实战踩出来的。

### 1. 有状态组件：`hostPath + Deployment` → `StatefulSet + PVC`

云厂商的节点是可替换的（故障自愈、扩缩容、版本升级都会重建节点），`hostPath` 的数据不跟着 Pod 走。

- 丢 Prometheus TSDB 只是难受
- **丢 `ops-mongo` = 告警规则、通知渠道、数据源配置、慢查询历史全没**

另外 Deployment 默认滚动更新会让新旧 Pod 同时挂同一块盘（官方因此不得不写 `strategy: Recreate`）；StatefulSet 单副本天然「先删后建」，没这个问题。

**存储类要求**：建议先建一个支持在线扩容的 SC（`allowVolumeExpansion: true`），PVC 建出来后再改 SC 就得重建盘了。腾讯云 TKE 示例：

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: cbs-expandable
  annotations:
    storageclass.kubernetes.io/is-default-class: "true"
provisioner: com.tencent.cloud.csi.cbs
parameters:
  diskType: CLOUD_PREMIUM     # 显式声明；TKE 默认 SC 常写 `type: cbs`，那是无效参数，实际靠驱动默认值兜底
reclaimPolicy: Delete
volumeBindingMode: Immediate
allowVolumeExpansion: true    # ← TKE 默认 SC 没开这个；驱动(csi-resizer)本就支持，只缺这个准入开关
```

> ⚠️ 扩容时**不要只盯 `PVC.status.capacity`**。它只在第二阶段（节点上 resize2fs）完成后才更新，而第二阶段**必须有 Pod 挂载**。没有 Pod 的 PVC 会一直停在旧值、condition 显示 `FileSystemResizePending` —— 这不是失败，云盘其实早就扩好了。

### 2. K8s 监控：`static` → `incluster`

部署在集群内，Prometheus 用 ServiceAccount + `kubernetes_sd_configs` 自动发现 kubelet/cadvisor/ksm/pods。零地址、零 bearer token，也不用暴露 30080/30081/10250 三个 NodePort。

### 3. `ops-agent` 的 Service 必须是 **headless**

**这是官方 manifests 的一个硬伤。** `ops-agent` 为「每个数据源实例」动态起一个 exporter 进程，端口是**动态分配**的：

```
mongodb → 19400, 19401, 19402...   （每多一个 MongoDB 实例就多一个端口）
kafka   → 19500, 19501...
mysql   → 19100...
```

而官方给的是 `ClusterIP` + 固定 5 个端口（9104/9114/9121/9216/9308）。ClusterIP Service **只转发 ports 里列出的端口**，动态端口一律 connection refused —— **Prometheus 会把所有中间件 target 报成 DOWN**。

官方那套只在「单实例 + env SD 静态模式」下成立。一旦用 http SD + 多实例（我们 5 个 MongoDB、2 个 Kafka），必然抓不到。

**headless（`clusterIP: None`）的 DNS 直接解析到 Pod IP，任意端口可直连，无需枚举。**

### 4. 容器日志：必须额外部署 `alloy-logs` DaemonSet

`ops-alloy`（`ROLE=alloy`）走 **docker.sock** 采日志，而 K8s 用 containerd —— **它在 K8s 下采不到任何容器日志**（日志里会刷 docker.sock 报错，属预期噪音；它在 K8s 下的唯一作用是接收 OTLP）。

只照抄官方 manifests = **Loki 里一条日志都不会有**。

`50-daemonsets.yaml` 里的 `alloy-logs` 经 kube API 读 pod 日志，才是 K8s 下的正解。

---

## 部署步骤

```bash
# 0. 前置：确保默认 StorageClass 支持在线扩容（见上）
kubectl get sc

# 1. namespace
kubectl apply -f 00-namespace.yaml

# 2. Secret（拷贝 example 后填写）
cp 10-secret.yaml.example 10-secret.yaml
#    ENV_OPS_TOKEN         登录令牌
#    ENV_ALERT_CRYPTO_KEY  32 字节：openssl rand -hex 16
#    ⚠️ 用 kubectl create secret 或直接写 YAML，别用 shell heredoc 转义拼——
#       引号会变成值的一部分（踩过：token 变成 "\"abc\""，登录死活不通）
kubectl apply -f 10-secret.yaml

# 3. 其余
kubectl apply -f 11-configmap.yaml -f 20-rbac.yaml -f 30-stateful.yaml -f 40-stateless.yaml -f 50-daemonsets.yaml

# 4. 等就绪（首次要拉 2.16GB 镜像，跨境可能很慢）
kubectl get pods -n hap-ops -w
```

### gateway 比 ops-mongo 先起来不要紧（1.4.5 起）

K8s 没有 compose 的 `depends_on`，镜像拉得慢时（跨境 2.16GB）gateway 往往先 Running。

**1.4.5 起 ops-server 连不上 `ops-mongo` 会在后台按 3s / 6s / 12s 退避重试**，mongo 就绪后自动挂载告警子系统，无需手动重启。判断标准只有一条：

```bash
kubectl logs deploy/ops-gateway -n hap-ops | grep 告警子系统
# 期望最终打印：[alert] 告警子系统已挂载 /api/alert/
```

> 历史：1.4.5 之前 gateway 只等 60 秒（30 次 × 2s）就放弃，且 ops-server 初始化不重试 → 告警子系统被永久禁用，
> 必须手动 `kubectl rollout restart deploy/ops-gateway -n hap-ops`。若你在用 1.4.5 以前的版本才需要这一步。

---

## 数据源接入（UI 或 API）

中间件全部远程抓，**无需在目标机器装任何东西**。主机只要有 `node_exporter:59100` 即可直接填地址。

### ⚠️ 数据源接入注意事项

**① MongoDB 数据源必须用有 `clusterMonitor` 权限的账号。**
`mongodb_exporter` 需要 `getDiagnosticData` / `currentOp` / `replSetGetConfig`，**业务账号（只有 readWrite）全都无权限**。

后果很隐蔽：
- 权限完全不足 → exporter **panic 崩溃**（nil pointer），HTTP 无响应，Prometheus 抓到 `EOF`
- 权限部分不足 → exporter **不崩，HTTP 200，但只吐 1 个指标**（`mongodb_up` 自己）

**第二种最坑：target 显示 UP，看起来一切正常，实际什么都没采到。** 判断标准不是 target 状态，而是指标条数（正常一台应有 **2000+** 个 `mongodb_*` 指标）：

```bash
curl -s http://ops-agent:19400/metrics | grep -c '^mongodb_'   # 正常 2000+，异常 1
```

推荐建专用只读账号（最小权限）：
```js
use admin
db.createUser({
  user: "mdis_monitor",
  pwd:  "<强随机>",
  roles: [
    { role: "clusterMonitor", db: "admin" },
    { role: "read",           db: "local" }
  ]
})
```

**② 慢查询诊断目标在 UI 里配，没有对应的环境变量。**
1.5.0 起 `ENV_MONGODB_URI` 已废弃（配了也不读，启动日志会提示）。在「数据源」页添加
MongoDB 数据源并勾选「诊断」用途即可；账号需要 `clusterMonitor` 角色。
从 1.4.x 升上来的实例，库里那条 `builtin-diagnose-mongo` 不再被平台维护，
它现在只是一条普通数据源，可以正常编辑或删除：
```
DELETE /api/alert/mongo-instances/builtin-diagnose-mongo
```

> 已修复、不必再规避的历史问题（升到 1.4.10 及以上即可）：
> - **host / redis / elasticsearch 用 API 创建后是禁用态** —— 三类 handler 曾硬编码 `Enabled = false`，
>   API 建出来的数据源 `enabled=false`、SD 永远返回空、target 一个都不出现（从 UI 建会被「启用」开关掩盖）。
> - **kafka / mongodb / mysql 用 API 创建后没有默认用途** —— `purposes` 为空，而采集只挑用途含 `metrics` 的源，
>   表现为建成功、列表可见、开关是开的，但永远不采集，比 `enabled=false` 更难发现。

---

## 对外暴露

`ops-gateway-ui`（NodePort 30881）→ 集群外 nginx 反代。

**Service 必须拆成两个**：`type: NodePort` 会给该 Service 的**每一个**端口都分配 NodePort，把 `8081`（ops-server 内部 API，只该给集群内 Prometheus 做 SD）也一起暴露出去。故 UI 走 NodePort、API 只走 ClusterIP（见 `40-stateless.yaml`）。

nginx 侧（挂子路径，复用现有域名，不必新开域名/证书）：

```nginx
upstream hap-ops {
    server <node1>:30881 max_fails=0;
    server <node2>:30881 max_fails=0;
    server <node3>:30881 max_fails=0;
}

location /mdis/ {
    allow <运维出口IP>;
    deny  all;
    proxy_pass http://hap-ops;
    proxy_set_header Host              $host;
    proxy_set_header X-Real-IP         $remote_addr;
    proxy_set_header X-Forwarded-For   $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_http_version 1.1;
    proxy_set_header Connection "";
}
```

MDIS 的 `ENV_OPS_SUB_PATH=/mdis`，所以 `/mdis/` 原样透传即可，无需 rewrite。
访问：`https://<域名>/mdis/web/`

安全边界 = **IP 白名单 + MDIS 自身的 32 位 token + 会话 cookie**，不必再加 Basic Auth。

---

## 接 istio 链路追踪（可选，业务零改造）

若被监控集群已跑 istio 且业务已注入 sidecar，把 istio 的 OTLP 上报指向 `ops-alloy`，**业务代码一行不用改**即可拿到全 mesh 链路。

> 🔑 **两个必配项，缺任一都是「配了却 0 trace」（实测 istio 1.23 踩过，纯静默无报错）：**
> 1. **`ops-alloy` Service 的 4317 端口必须带 `appProtocol: grpc`**（见 `40-stateless.yaml`）。istio 靠它把上游 cluster 配成 HTTP/2；否则 sidecar 的 OpenTelemetry tracer 走 gRPC 建不起流 → 静默不产 span。**这是最隐蔽的坑，端口名叫 `otlp-grpc` 也没用，前缀非 `grpc-`/`http2-` istio 不认，必须显式 appProtocol。**
> 2. **MeshConfig 里补 `defaultProviders.tracing`**，不能只靠 Telemetry 资源。

```yaml
# istio MeshConfig（istio configmap 的 .data.mesh）
defaultProviders:
  tracing:                 # ← 必须有这段
  - otel-tracing
extensionProviders:
- name: otel-tracing
  opentelemetry:
    service: ops-alloy.hap-ops.svc.cluster.local
    port: 4317
---
apiVersion: telemetry.istio.io/v1
kind: Telemetry
metadata: { name: mesh-default, namespace: istio-system }
spec:
  tracing:
  - providers: [{ name: otel-tracing }]
    randomSamplingPercentage: 1.0    # 采样率必须控制，pod 多时全量会打爆 Tempo
```

链路：`istio-proxy → ops-alloy(OTLP 4317, appProtocol=grpc→HTTP/2) → ops-tempo`

排查：sidecar 零 `tracing.*` 统计 = tracer 没产 span；查 `outbound|4317||ops-alloy` cluster 若是 `use_downstream` 而非 HTTP/2，就是漏了 appProtocol。

> ⚠️ 改 istio MeshConfig 会触发**所有 sidecar 热加载配置**。虽不重启 Pod，但碰的是生产流量面 —— **单独窗口做，备份原 ConfigMap，不要和 MDIS 部署混在一起**。

---

## 其它坑

**Grafana 看板不能用 `subPath` 挂载覆盖。** `grafana.sh` 启动时要 `sed -i` 就地改看板（替换 datasource UID），而 subPath 是只读 bind mount，`sed -i` 的 rename 会失败：

```
sed: cannot rename /usr/share/grafana/dashboardFile/sedXXXXXX: Device or resource busy
→ CrashLoopBackOff
```

要覆盖看板，用 initContainer 把镜像里的看板复制到 emptyDir，再覆盖单个文件，主容器挂 emptyDir（可写）。

**镜像跨境拉取。** 镜像在阿里云杭州（`registry.cn-hangzhou.aliyuncs.com`），海外集群拉 2.16GB 可能十几分钟，且**各节点快慢不一**。若某个节点卡住，可 `kubectl cordon` 该节点 + 删掉其上的非 DaemonSet Pod，逼它们调度到已有镜像缓存的节点（DaemonSet 躲不掉，只能等）。海外部署建议先把镜像同步到就近的镜像仓库。
