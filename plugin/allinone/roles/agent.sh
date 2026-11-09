#!/bin/bash
#
# ops-agent 中间件 exporter 集合。
#
# 采集目标只有一个来源：UI「数据源」页。本脚本周期性从 ops-server 的
# /api/sd/sources 拉取「已启用且用途含 metrics」的数据源，按它返回的确定性端口
# 拉起/回收对应 exporter。数据源改完即时生效（下一轮 30s 内），不用重启容器。
#
# 1.5.0 移除了 env 模式（按 ops.yaml 的 ENV_MYSQL_*/ENV_REDIS_*/… 起 exporter，
# 多目标用 ";" 分隔）。那套与 UI 并存时是两套入口两套语法：客户按 UI 的直觉在
# ENV 里用逗号分隔多节点，会静默拼成一个假地址，且改 ops.yaml 不再生效、零报错。

log() { echo "$(date +"%Y-%m-%d %H:%M:%S") - $1"; }

# ===================== exporter 守护 =====================
# 1.4.7 起每个 exporter 由一个后台 while 循环托管：进程退出就重启（退避 2→60s 封顶），
# 退出原因同时打进 docker logs。
#
# 为什么必须有：1.4.6 及以前是裸 `cmd ... > log 2>&1 &`，而 kafka_exporter 在
# 「启动时连不上 broker」会 F(fatal) 直接退出、不重试，其余 4 类只是 up=0、目标恢复后自愈。
# 于是 agent 早于中间件几秒起来（compose 同机部署的常态，中间件重启/慢启动也一样），
# Kafka 指标就永久为空——而 agent 容器仍 Up、docker logs 只有一行 "Starting Kafka Exporter"，
# 错误只写进容器内 logs/*.log，客户根本无从自查。
SUP_DIR=/usr/local/agent/logs
declare -A SUP_PIDS   # type -> "wrapper_pid wrapper_pid ..."

# supervise <type> <label> <logfile> <cmd...>
supervise() {
  local typ="$1" label="$2" logfile="$3"; shift 3
  mkdir -p "$(dirname "$logfile")"
  local rcfile="${logfile}.restarts"
  : > "$rcfile"
  (
    local delay=2 code n=0 last_code="" last_log=0 now
    while :; do
      "$@" >> "$logfile" 2>&1
      code=$?
      n=$((n + 1))
      echo "$n" > "$rcfile"
      # 日志降噪：同一退出码反复出现时，只在第 1 次以及之后每 10 分钟打一条。
      # 一个持续连不通的目标会以 60s 封顶的节奏无限重启，每轮 4 行日志足以把其它类型
      # 的真实报错冲出可视范围——用户翻日志时反而更难定位。次数不丢，通过
      # <log>.restarts 汇进「采集状态」的失败原因里。
      now=$(date +%s)
      if [ "$code" != "$last_code" ] || [ $((now - last_log)) -ge 600 ]; then
        log "[supervise] $label 退出(code=$code，累计第 ${n} 次)，${delay}s 后重启。最后 3 行："
        tail -n 3 "$logfile" 2>/dev/null | sed "s/^/             [$label] /"
        last_code="$code"; last_log=$now
      fi
      sleep "$delay"
      delay=$((delay * 2)); [ "$delay" -gt 60 ] && delay=60
    done
  ) &
  SUP_PIDS[$typ]="${SUP_PIDS[$typ]} $!"
}

# stop_supervised <type> <binary>：先杀守护壳再杀进程，否则守护壳会立刻用旧配置把它拉回来
stop_supervised() {
  local typ="$1" binary="$2" p
  for p in ${SUP_PIDS[$typ]}; do kill "$p" 2>/dev/null; done
  SUP_PIDS[$typ]=""
  pkill -f "/usr/local/agent/$binary/$binary" 2>/dev/null
}

# ===================== 启动自检 =====================
# 起完 exporter 等 8s，逐个探自己的 /metrics 判断「真的采到数据了吗」。
# 专治「进程活着但一条数据没有」——最典型的是 ES 凭据写错（401），
# 此前表现为 Prometheus target=up、面板全空、任何日志都没有报错。
# 运行中 exporter 注册表，按类型分桶（http SD 模式下每轮只重建变化的类型，
# 不能用一个数组整体清空——否则没变化的类型条目会丢，状态上报直接变空）
declare -A SC_BY_TYPE

sc_reset() { SC_BY_TYPE[$1]=""; }
sc_add()   { SC_BY_TYPE[$1]="${SC_BY_TYPE[$1]}$2"$'\n'; }
sc_all()   { local t; for t in "${!SC_BY_TYPE[@]}"; do printf '%s' "${SC_BY_TYPE[$t]}"; done; }
sc_count() { sc_all | grep -c . ; }

selfcheck() {
  local -a pending ok_list
  mapfile -t pending < <(sc_all)
  [ "${#pending[@]}" -eq 0 ] && return 0
  sleep 10
  ok_list=()
  local round entry typ port target body left
  # 最多轮询 8 轮 × 5s：exporter 首次抓取有快有慢（ES 的 clusterinfo collector 尤其慢），
  # 只探一次会把「还没抓完」误判成「采不到」——自检自己变噪声源就白做了。
  for round in 1 2 3 4 5 6 7 8; do
    left=()
    for entry in "${pending[@]}"; do
      IFS='|' read -r typ port target logfile <<< "$entry"
      body=$(curl -sf -m 5 "http://127.0.0.1:${port}/metrics" 2>/dev/null)
      if [ -n "$body" ] && check_metrics "$typ" "$body"; then
        ok_list+=("$entry")
      else
        left+=("$entry")
      fi
    done
    pending=("${left[@]}")
    [ "${#pending[@]}" -eq 0 ] && break
    [ "$round" -lt 8 ] && sleep 5
  done

  log "== 采集自检 =="
  for entry in "${ok_list[@]}"; do
    IFS='|' read -r typ port target logfile <<< "$entry"
    log "  ✓ ${typ} :${port} -> ${target}"
  done
  # 失败原因按 curl 退出码分三种说，别再一律归成「进程已退出」：
  #   7        连不上端口 → 进程真的挂了或还没起来
  #   52/56/18 连上了但响应被掐断 → exporter 自身 panic，进程仍在跑（ps 看得到）
  #   28       抓取超时
  #   其余     有响应但指标不达标
  # 1.5.8 客户实测踩到的就是 52：mongodb exporter 因 currentop 缺权限每次抓取 panic，
  # 旧代码报「进程已退出，原因见上方 [supervise] 行」——进程没退出、supervise 那行也
  # 根本不会打（它只在进程退出时才打）。客户据此判断「日志里没有任何报错」，而真正的
  # 堆栈只躺在容器内 logs/*.log 里。所以这里必须直接把日志文件路径写出来。
  local rc
  for entry in "${pending[@]}"; do
    IFS='|' read -r typ port target logfile <<< "$entry"
    curl -s -m 5 -o /dev/null "http://127.0.0.1:${port}/metrics" 2>/dev/null; rc=$?
    case "$rc" in
      7)
        log "  ✗ ${typ} :${port} -> ${target}  端口没在监听：进程已退出或还没起来，原因见容器内 ${logfile} 末尾" ;;
      52|56|18)
        log "  ✗ ${typ} :${port} -> ${target}  端口在监听但响应被中断：多为 exporter 自身 panic（进程仍在跑），堆栈见容器内 ${logfile}" ;;
      28)
        log "  ✗ ${typ} :${port} -> ${target}  抓取超时(>5s)：目标响应慢或网络抖动，详见容器内 ${logfile}" ;;
      *)
        log "  ✗ ${typ} :${port} -> ${target}  进程在跑但 40s 内没采到数据：$(fail_hint "$typ")" ;;
    esac
  done
}

# ===================== 采集状态上报 =====================
# 把每个 exporter 的实时状态推给 ops-server，UI「数据源」页直接显示红绿点和失败原因。
#
# 为什么要推而不是让 ops-server 来探：失败原因（exporter 为什么退出）只有 agent 手上有——
# supervise 捕获的退出输出落在容器内 logs/*.log 里。客户环境十来个容器，让用户去猜该进哪个
# 容器看哪个文件是不现实的，这正是这次真实客户部署反馈的痛点。
json_escape() { printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e "s/$(printf '\t')/ /g"; }

# humanize_reason <原始日志行> <目标地址>：把 exporter 吐的英文/堆栈换成能直接照做的中文。
# 「数据源」页的采集状态列是用户排障的唯一入口——那里出现 Go 堆栈或裸英文，
# 等于把问题原样推回给我们，用户自己闭环不了。
humanize_reason() {
  local raw="$1" target="$2" typ="$3" low extra="" clean
  # exporter 普遍用 logfmt 输出：time="2026-..." level=error msg="真正的原因"。
  # 整行丢给用户等于让他在时间戳和日志级别里找线索——先把 msg="..." 抠出来。
  clean=$(printf '%s' "$raw" | sed -n 's/.*msg="\([^"]*\)".*/\1/p')
  [ -z "$clean" ] && clean="$raw"
  low=$(printf '%s' "$clean" | tr 'A-Z' 'a-z')
  # 类型相关的补充提示只挂给对应类型——不加判断的话，MySQL 认证失败也会被告知
  # 「ES 用户名是 md」，反而把人带偏
  case "$typ" in
    elasticsearch) extra="，明道云私有部署自带 ES 的用户名是 md，不是 elastic" ;;
    mongodb)       extra="，注意 URI 里的密码若含特殊字符需 URL 编码" ;;
  esac
  case "$low" in
    *"connection refused"*)
      echo "连不上 ${target}：端口不通，或对端服务没起" ;;
    *"no such host"*|*"nodename nor servname"*|*"name resolution"*)
      echo "解析不了主机名 ${target}：确认地址拼写，且容器内能解析到该名字（跨 stack 要用宿主机 IP）" ;;
    *"i/o timeout"*|*"context deadline exceeded"*|*"timeout"*)
      echo "连接 ${target} 超时：多为网络不通或防火墙拦截" ;;
    *"no route to host"*|*"network is unreachable"*)
      echo "路由不可达 ${target}：确认网段与安全组" ;;
    *"unable to authenticate"*|*"authentication failed"*|*"auth failed"*|*"invalid password"*|*"access denied"*)
      echo "账号或密码不对（${target}）${extra}" ;;
    *"not authorized"*|*"unauthorized"*|*"permission denied"*)
      if [ "$typ" = "mongodb" ]; then
        echo "账号权限不足（${target}）：MongoDB 采集需要 clusterMonitor 角色"
      else
        echo "账号权限不足（${target}）：确认该账号有读取监控指标的权限"
      fi ;;
    *"empty client"*|*"cannot get mongo node type"*|*"no reachable servers"*|*"server selection"*|*"topology is closed"*)
      # mongodb_exporter 连不上目标时不报 refused，而是吐一串
      # "cannot get mongo node type from an empty client" —— 字面上完全看不出是连不上
      echo "连不上目标 MongoDB（${target}）：确认地址端口可达、账号密码正确${extra}" ;;
    *"noauth"*)
      echo "Redis 需要密码（${target}）：请在数据源里补上密码" ;;
    *"wrongpass"*)
      echo "Redis 密码不对（${target}）" ;;
    *"panic:"*)
      echo "exporter 异常退出：${clean#*panic: }（多为目标地址不可达或版本不兼容）" ;;
    *)
      echo "$clean" ;;
  esac
}

report_status() {
  local -a entries
  mapfile -t entries < <(sc_all)
  [ "${#entries[@]}" -eq 0 ] && return 0
  local entry typ port target logfile body running collecting reason rc crc note first=1 payload
  payload='{"mode":"http","exporters":['
  for entry in "${entries[@]}"; do
    IFS='|' read -r typ port target logfile <<< "$entry"
    body=$(curl -sf -m 5 "http://127.0.0.1:${port}/metrics" 2>/dev/null); crc=$?
    if [ -z "$body" ]; then
      # running 要按退出码判，不能一律 false：curl 7 才是端口没监听（进程真挂），
      # 52/56/18 是连上了但响应被掐断——exporter 自身 panic，进程好好活着。
      # 说成「进程已退出」用户一 ps 就发现不对，整套状态显示的可信度就没了。
      note=""
      if [ "$crc" = 7 ]; then
        running=false
      else
        running=true; note="（进程在跑，但每次抓取都被中断）"
      fi
      collecting=false
      # 退出原因取行有两个坑，都实测踩过：
      #  a) exporter 被 supervise 反复拉起，日志末尾常是刚打印的 "Starting ..."，
      #     直接取最后一行会把真正的 fatal 盖掉（kafka）；
      #  b) Go panic 的堆栈里每一行栈帧都含 "panic" 字样，按关键字取最后一条会
      #     精准命中最没信息量的 `runtime/panic.go:785 +0x132`（mongodb_exporter，
      #     客户复测时「采集状态」里显示的就是这一行，等于没给原因）。
      # 所以：先专门捞 panic 首行，再退回关键字匹配（并排除栈帧行和缩进的续行）。
      reason=$(tail -n 200 "$logfile" 2>/dev/null | grep -a '^panic:' | tail -n 1)
      [ -z "$reason" ] && reason=$(tail -n 40 "$logfile" 2>/dev/null \
        | grep -aiE '^F[0-9]|error|failed|refus|denied|unauthor|timeout|no such host' \
        | grep -avE '\.go:[0-9]+ \+0x|^[[:space:]]' \
        | tail -n 1)
      [ -z "$reason" ] && reason=$(tail -n 40 "$logfile" 2>/dev/null | grep -av '^[[:space:]]*$' | tail -n 1)
      if [ -z "$reason" ]; then
        [ "$running" = true ] && reason="exporter :$port 有响应但立即中断" || reason="exporter 未监听 :$port"
      fi
      reason=$(humanize_reason "$reason" "$target" "$typ")
      reason="${reason}${note}"
      # 带上重启次数：一直在重启说明目标持续不可达，不是偶发抖动，用户据此判断该改配置了
      rc=$(cat "${logfile}.restarts" 2>/dev/null)
      if [ -n "$rc" ] && [ "$rc" -gt 1 ] 2>/dev/null; then reason="${reason}（已重试 ${rc} 次）"; fi
      reason=$(printf '%.300s' "$reason")
    elif check_metrics "$typ" "$body"; then
      running=true; collecting=true; reason=""
    else
      running=true; collecting=false; reason=$(fail_hint "$typ")
    fi
    [ "$first" = 1 ] || payload="$payload,"
    first=0
    payload="$payload{\"type\":\"$(json_escape "$typ")\",\"port\":$port,\"target\":\"$(json_escape "$target")\",\"running\":$running,\"collecting\":$collecting,\"reason\":\"$(json_escape "$reason")\"}"
  done
  payload="$payload]}"
  curl -sf -m 5 -X POST -H 'Content-Type: application/json' \
    --data-binary "$payload" "${SD_URL}/api/sd/agent-status${SD_TOK}" >/dev/null 2>&1 \
    || log "[status] 上报采集状态失败(不致命，UI 会显示为未知)"
}

# 每 60s 上报一次；exporter 刚起时状态还没稳，首轮等自检那 40s 走完
status_loop() { sleep 60; while :; do report_status; sleep 60; done; }

# check_metrics <type> <metrics 文本>：这一类的「真的采到了」判据
check_metrics() {
  case "$1" in
    kafka)         echo "$2" | grep -qE '^kafka_brokers [1-9]' ;;
    elasticsearch) echo "$2" | grep -qE '^elasticsearch_(clusterinfo_up|cluster_health_up) 1' ;;
    redis)         echo "$2" | grep -qE '^redis_up 1' ;;
    mysql)         echo "$2" | grep -qE '^mysql_up 1' ;;
    mongodb)       echo "$2" | grep -qE '^mongodb_ss_' ;;
    *)             return 0 ;;
  esac
}

# fail_hint <type>：进程活着但一条指标都取不到时的提示。
# 采集只认 UI「数据源」页，出错一律指向那里——1.5.0 前这里还会按模式指向
# ops.yaml 的 ENV_*，那是把人指到一个改了也不生效的地方。
fail_hint() {
  local where="到「数据源」页改这条的连接信息（改完即时生效，不用重启容器）"
  case "$1" in
    kafka)         echo "连不上 broker，${where}" ;;
    elasticsearch) echo "抓不到 ES 指标，最常见是凭据错——明道云私有部署自带 ES 的用户名是 md 不是 elastic，${where}" ;;
    redis)         echo "连不上 Redis，${where}" ;;
    mysql)         echo "连不上 MySQL，${where}" ;;
    mongodb)       echo "连不上 MongoDB 或账号无 clusterMonitor 权限，${where}" ;;
    *)             echo "" ;;
  esac
}

# ===================== 数据源服务发现 =====================
# 周期性从 ops-server 拉取「已启用且用途含 metrics」的数据源，按 ops-server 分配的
# 确定性端口拉起对应 exporter；某类型源集合发生变化才重建该类型的全部 exporter
# （按二进制名 pkill 后重启），无变化跳过。
# 端口由 ops-server 的 /api/sd/sources 返回的 port 字段给出（与 prometheus http_sd 一致）。

SD_URL="${ENV_OPS_SD_URL:=http://gateway:8081}"
SD_TOK=""; [ -n "$ENV_OPS_SD_TOKEN" ] && SD_TOK="&token=${ENV_OPS_SD_TOKEN}"

# 拉取某类型的数据源 JSON 数组（失败回空串）
sd_fetch() { curl -sf -m 10 "${SD_URL}/api/sd/sources?type=$1${SD_TOK}" 2>/dev/null; }

spawn_kafka_dyn() {
  local json="$1" n i port name brokers b sasl_en mech; local -a args
  n=$(echo "$json" | jq 'length')
  for ((i=0;i<n;i++)); do
    port=$(echo "$json" | jq -r ".[$i].port"); name=$(echo "$json" | jq -r ".[$i].name")
    brokers=$(echo "$json" | jq -r ".[$i].brokers // [] | join(\",\")")
    args=(); local OLDIFS="$IFS"; IFS=','; for b in $brokers; do [ -n "$b" ] && args+=("--kafka.server=$b"); done; IFS="$OLDIFS"
    sasl_en=$(echo "$json" | jq -r ".[$i].sasl.enabled")
    if [ "$sasl_en" = "true" ]; then
      args+=("--sasl.enabled" "--sasl.username=$(echo "$json" | jq -r ".[$i].sasl.username")" "--sasl.password=$(echo "$json" | jq -r ".[$i].sasl.password")")
      mech=$(echo "$json" | jq -r ".[$i].sasl.mechanism"); [ -n "$mech" ] && [ "$mech" != "null" ] && args+=("--sasl.mechanism=$mech")
      [ "$(echo "$json" | jq -r ".[$i].sasl.tls")" = "true" ] && args+=("--tls.enabled")
      [ "$(echo "$json" | jq -r ".[$i].sasl.skipTls")" = "true" ] && args+=("--tls.insecure-skip-verify")
    fi
    log "kafka exporter(dyn) :$port -> $brokers ($name)"
    supervise kafka "kafka:$name" "/usr/local/agent/kafka_exporter/logs/kafka_dyn_$port.log" \
      /usr/local/agent/kafka_exporter/kafka_exporter "${args[@]}" --web.listen-address=":$port"
    sc_add kafka "kafka|$port|$brokers|/usr/local/agent/kafka_exporter/logs/kafka_dyn_$port.log"
  done
}

spawn_es_dyn() {
  local json="$1" n i port url user pass uri proto endpoint
  n=$(echo "$json" | jq 'length')
  for ((i=0;i<n;i++)); do
    port=$(echo "$json" | jq -r ".[$i].port"); url=$(echo "$json" | jq -r ".[$i].url")
    user=$(echo "$json" | jq -r ".[$i].username"); pass=$(echo "$json" | jq -r ".[$i].password")
    if [ -n "$user" ] && [ "$user" != "null" ]; then
      proto="${url%%://*}"; endpoint="${url#*://}"; uri="${proto}://${user}:${pass}@${endpoint}"
    else uri="$url"; fi
    log "elasticsearch exporter(dyn) :$port -> $url"
    supervise elasticsearch "elasticsearch:$(echo "$json" | jq -r ".[$i].name")" "/usr/local/agent/elasticsearch_exporter/logs/es_dyn_$port.log" \
      /usr/local/agent/elasticsearch_exporter/elasticsearch_exporter --es.uri="$uri" --web.listen-address=":$port"
    sc_add elasticsearch "elasticsearch|$port|$url|/usr/local/agent/elasticsearch_exporter/logs/es_dyn_$port.log"
  done
}

spawn_redis_dyn() {
  local json="$1" n i port host rport pass addr
  n=$(echo "$json" | jq 'length')
  for ((i=0;i<n;i++)); do
    port=$(echo "$json" | jq -r ".[$i].port"); host=$(echo "$json" | jq -r ".[$i].host")
    rport=$(echo "$json" | jq -r ".[$i].redisPort"); pass=$(echo "$json" | jq -r ".[$i].password")
    addr="${host}:${rport}"
    log "redis exporter(dyn) :$port -> $addr"
    if [ -n "$pass" ] && [ "$pass" != "null" ]; then
      supervise redis "redis:$port" "/usr/local/agent/redis_exporter/logs/redis_dyn_$port.log" \
        /usr/local/agent/redis_exporter/redis_exporter --redis.addr="$addr" --redis.password="$pass" --web.listen-address=":$port"
    else
      supervise redis "redis:$port" "/usr/local/agent/redis_exporter/logs/redis_dyn_$port.log" \
        /usr/local/agent/redis_exporter/redis_exporter --redis.addr="$addr" --web.listen-address=":$port"
    fi
    sc_add redis "redis|$port|$addr|/usr/local/agent/redis_exporter/logs/redis_dyn_$port.log"
  done
}

spawn_mysql_dyn() {
  local json="$1" n i port name node mh mp user pass cnf
  local -a collector_args
  n=$(echo "$json" | jq 'length')
  for ((i=0;i<n;i++)); do
    mapfile -t collector_args < <(jq -r ".[$i].collectorArgs // [] | .[]" <<< "$json")
    port=$(echo "$json" | jq -r ".[$i].port"); name=$(echo "$json" | jq -r ".[$i].name")
    node=$(echo "$json" | jq -r ".[$i].nodes // [] | .[0] // \"\"")
    [ -z "$node" ] && { log "mysql 源 $name 无节点,跳过"; continue; }
    user=$(echo "$json" | jq -r ".[$i].username"); pass=$(echo "$json" | jq -r ".[$i].password")
    mh="${node%%:*}"; mp="${node#*:}"; [ "$mp" = "$node" ] && mp=3306
    cnf="/usr/local/agent/mysqld_exporter/dyn_$port.cnf"
    printf "[client]\nuser=%s\npassword=%s\nhost=%s\nport=%s\n" "$user" "$pass" "$mh" "$mp" > "$cnf"
    log "mysql exporter(dyn) :$port -> $mh:$mp ($name)"
    supervise mysql "mysql:$name" "/usr/local/agent/mysqld_exporter/logs/mysql_dyn_$port.log" \
      /usr/local/agent/mysqld_exporter/mysqld_exporter --config.my-cnf="$cnf" --web.listen-address=":$port" "${collector_args[@]}"
    sc_add mysql "mysql|$port|$mh:$mp|/usr/local/agent/mysqld_exporter/logs/mysql_dyn_$port.log"
  done
}

spawn_mongo_dyn() {
  local json="$1" n i port uri user pass authdb scheme rest
  local -a collector_args
  n=$(echo "$json" | jq 'length')
  for ((i=0;i<n;i++)); do
    mapfile -t collector_args < <(jq -r ".[$i].collectorArgs // [] | .[]" <<< "$json")
    port=$(echo "$json" | jq -r ".[$i].port"); uri=$(echo "$json" | jq -r ".[$i].uri")
    # UI 上的账号/密码/认证库是独立于 uri 的三个字段，ops-server 也照原样下发。
    # 这里必须拼进 URI —— 曾经只取 .uri，导致目标库一旦开鉴权就全是 Unauthorized：
    # 连接测试通过（走后端，凭据齐全）、exporter 进程在跑、Prometheus target 也 up，
    # 只有 mongodb_up=0，现象上极难往「账号没传到」上想。
    user=$(echo "$json" | jq -r ".[$i].username // \"\"")
    pass=$(echo "$json" | jq -r ".[$i].password // \"\"")
    authdb=$(echo "$json" | jq -r ".[$i].authDb // \"\"")
    [ "$user" = "null" ] && user=""; [ "$pass" = "null" ] && pass=""; [ "$authdb" = "null" ] && authdb=""

    scheme="${uri%%://*}"; rest="${uri#*://}"
    # uri 里已内嵌 user:pass@ 时以 uri 为准，不覆盖（兼容老配置与手工填法）
    if [ -n "$user" ] && [ "${rest%%/*}" = "${rest%%[@/]*}" ]; then
      # 账号密码可能含 @ : / ? & 等，必须 URL 编码后再拼，否则整条 URI 结构被拼坏
      user=$(printf '%s' "$user" | jq -sRr @uri)
      pass=$(printf '%s' "$pass" | jq -sRr @uri)
      uri="${scheme}://${user}:${pass}@${rest}"
    fi

    [[ "$uri" == mongodb://*/* || "$uri" == mongodb+srv://*/* ]] || uri="${uri}/"

    # authDb → authSource；注意 uri 可能已带 ?replicaSet=xxx，要用 & 续接
    if [ -n "$authdb" ] && [[ "$uri" != *authSource=* ]]; then
      case "$uri" in
        *\?*) uri="${uri}&authSource=${authdb}" ;;
        *)    uri="${uri}?authSource=${authdb}" ;;
      esac
    fi

    # 多节点副本集必须关掉 direct-connect：exporter(v0.42) 默认走直连，而直连与「URI 里
    # 多个 host」互斥，报 "a direct connection cannot be made if multiple hosts are
    # specified" 后一条指标都采不到。单节点保持默认，不改变原有行为。
    # 取 host 段：剥掉 scheme、剥掉可能存在的 user:pass@、再截到第一个 /
    _hosts="${uri#*://}"; _hosts="${_hosts##*@}"; _hosts="${_hosts%%/*}"
    _direct=""
    case "$_hosts" in
      *,*) _direct="--no-mongodb.direct-connect" ;;
    esac

    # 注意：日志只打端口不打 uri，避免密码进日志；但 --mongodb.uri 仍会出现在容器内
    # `ps` 的命令行里（mongodb_exporter 的固有形态，MySQL 那边靠 my.cnf 规避）。
    #
    # ⚠️ 不要再加回 --collector.currentopmetrics。currentOp 需要 inprog 权限，只给了
    # clusterMonitor 的账号拿不到；而 percona exporter(v0.42) 在这个 collector 失败时
    # 是在 HTTP handler 里 panic（descriptor is invalid → MustRegister panic），
    # 连接被直接掐断，于是整个 /metrics 全废、不是只少 currentop 这一块。
    # Prometheus 侧只看到 `EOF`，进程还在跑，agent 自检也只说"未监听"——三处线索
    # 没一处指向真正的原因。看板一个 currentop 指标都没用到，白担这个风险。
    log "mongodb exporter(dyn) :$port${_direct:+ (多节点副本集,关闭 direct-connect)}"
    supervise mongodb "mongodb:$port" "/usr/local/agent/mongodb_exporter/logs/mongo_dyn_$port.log" \
      /usr/local/agent/mongodb_exporter/mongodb_exporter --mongodb.uri="$uri" --web.listen-address=":$port" \
      --compatible-mode --collector.diagnosticdata --collector.replicasetstatus --collector.shards \
      ${_direct} "${collector_args[@]}"
    sc_add mongodb "mongodb|$port|$(echo "$json" | jq -r ".[$i].name")|/usr/local/agent/mongodb_exporter/logs/mongo_dyn_$port.log"
  done
}

declare -A SD_HASH
reconcile_once() {
  local t json h binary changed=0
  for t in kafka elasticsearch redis mysql mongodb; do
    json=$(sd_fetch "$t")
    [ -z "$json" ] && { log "[sd] 拉取 $t 失败,保留现有 exporter"; continue; }
    h=$(printf '%s' "$json" | md5sum | cut -d' ' -f1)
    [ "${SD_HASH[$t]}" = "$h" ] && continue
    SD_HASH[$t]="$h"
    case "$t" in
      kafka) binary=kafka_exporter ;; elasticsearch) binary=elasticsearch_exporter ;;
      redis) binary=redis_exporter ;; mysql) binary=mysqld_exporter ;; mongodb) binary=mongodb_exporter ;;
    esac
    log "[sd] $t 源集合变化,重建 exporter"
    # 必须走 stop_supervised：只 pkill 二进制的话，守护壳会立刻用「旧配置」把它拉回来
    stop_supervised "$t" "$binary"
    sc_reset "$t"
    sleep 1
    case "$t" in
      kafka) spawn_kafka_dyn "$json" ;; elasticsearch) spawn_es_dyn "$json" ;;
      redis) spawn_redis_dyn "$json" ;; mysql) spawn_mysql_dyn "$json" ;; mongodb) spawn_mongo_dyn "$json" ;;
    esac
    changed=1
  done
  # 整轮结束后只自检一次：按类型逐个触发会把结果打散成好几段、还互相穿插
  [ "$changed" = "1" ] && selfcheck &
  # 上报必须在父进程里做：注册表是 reconcile 在父进程更新的，子进程只有 fork 那一刻的快照
  report_status
}

# ===================== 入口分发 =====================
# 1.5.0 起只有一种模式：采集配置以 UI「数据源」页为唯一真相源。
# 曾经的 env 模式（按 ops.yaml 的 ENV_MYSQL_*/ENV_REDIS_*/… 起 exporter）已整段移除——
# 两套入口两套语法、只生效一次、配错零反馈，是最难排查的一类问题。
log "== 从 ${SD_URL} 拉取数据源,动态拉起 exporter =="
while true; do reconcile_once; sleep 30; done
