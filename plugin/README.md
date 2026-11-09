# OPS 运维平台部署文档（1.5.0）

MDIS（运维平台）是一套监控诊断系统：主机 / 中间件资源监控、慢查询分析、日志与链路可观测性、自研告警子系统，统一在一个 Web 界面。本文面向部署运维人员。

> 开发/联调环境的最小起步与避坑另见 [`allinone/DEPLOY.md`](allinone/DEPLOY.md)；单镜像形态的内部结构见 [`allinone/README.md`](allinone/README.md)。

---

## 1. 架构总览（1.4.0：单镜像 all-in-one）

**1.4.0 起全栈打进一个镜像 `ops-allinone`**，运行时由 `ROLE` 环境变量分发成不同角色容器。容器数量不变（仍各跑各的），但**只维护 / 打包 / 离线导出一个镜像**。

| 容器名 | ROLE | 职责 | 对外端口 |
|---|---|---|---|
| `script-gateway-1` | `gateway` | 前端 React + 后端 Go（ops-server）+ 反代 nginx（token 鉴权）+ 自研告警子系统 | `48881` |
| `script-ops-prometheus-1` | `prometheus` | Prometheus 3.1 指标存储与抓取 | `9090`（内网） |
| `script-ops-grafana-1` | `grafana` | Grafana 12.2 统一可观测性界面 | `3000`（内网，经 gateway 反代） |
| `script-agent-1` | `agent` | 中间件 exporter 集合（Kafka/ES/Redis/MySQL/MongoDB），单实例多目标 | 各 exporter 端口（内网） |
| `script-nodeagent-1` | `nodeagent` | Node Exporter 主机指标（host 网络） | `59100` |
| `script-ops-loki-1` | `loki` | Loki 日志存储（fs / S3 双后端） | `3100` |
| `script-ops-tempo-1` | `tempo` | Tempo 分布式链路存储 | `3200`（内网） |
| `script-ops-alloy-1` | `alloy` | Grafana Alloy：OTLP 接收（4317/4318）+ Docker stdout 采集 | `4317` `4318` |
| `script-ops-mongo-1` | `mongo` | 告警子系统自有 MongoDB（存规则/状态/历史/渠道/数据源，与被监控目标解耦） | 内网 |
| （K8s 部署）`ops-ksm` | `ksm` | kube-state-metrics，K8s 集群对象状态 | 内网 |

对外唯一入口是 gateway 的 `48881`；其余组件仅在 docker network（默认 `script_default`）内互通。

### 组件版本（单点管理，改一处重 build）

| 组件 | 版本 | 来源 |
|---|---|---|
| Prometheus | 3.1.0 | Dockerfile `ARG` |
| node_exporter | 1.8.2 | Dockerfile `ARG` |
| kafka / es / redis / mysql / mongodb exporter | 1.6.0 / 1.5.0 / 1.44.0 / 0.15.1 / 0.42.0 | Dockerfile `ARG` |
| Grafana | 12.2.1 | `FROM` tag |
| Loki / Tempo / Alloy | 3.5.8 / 2.9.0 / v1.11.3 | `FROM` tag |
| MongoDB（告警存储） | 4.4.28 | `FROM` tag |
| kube-state-metrics | 2.10.1 | `FROM` tag |

---

## 2. 拿到镜像

### 方式一：直接 pull（推荐）

```bash
docker pull registry.cn-hangzhou.aliyuncs.com/mdpublic/ops-allinone:1.5.12
```

> ⚠️ 近期功能迭代常以 `PUSH=0` 只在构建机本地出镜像（tag 可能带后缀），registry 上的 `:1.4.0` 未必是最新。要最新功能找镜像维护者确认 tag，或走方式二自建。

### 方式二：自己构建

```bash
cd plugin/allinone
PUSH=0 ./build.sh              # 只 build 不推；IMAGE=... 覆盖 tag
```

`build.sh`：yarn 编前端 `dist/` → go 编后端 `ops-server` → 以 `plugin/allinone/` 为上下文 build 单镜像。需要本机 Go 1.23.3 + Node/yarn；Dockerfile 内置境外下载代理拉 prometheus/各 exporter，上游镜像走 daocloud 源，构建机需能连通。

---

## 3. 中间件账号权限

使用外部中间件时，各自所需最小权限：

| 中间件 | 权限 |
|---|---|
| Kafka | 读取 Kafka 元数据 |
| Elasticsearch | 读取索引（exporter 只读；结构化日志入 Loki 需写索引则另配） |
| Redis | 读取 |
| MySQL | `SELECT`、`SHOW DATABASES`、`PROCESS`、`REPLICATION CLIENT` |
| MongoDB（被监控/慢查询） | 只读观测（读 `system.profile`、`mdwsrows` 元数据）；如需开 profiling 则相应库权限 |
| 自定义页面（Flink 等外链 Web UI） | 反代模式：允许后台服务器访问其 Web UI；直嵌模式：允许浏览器访问，且目标未禁止被 iframe 嵌入 |

> 告警子系统自有的 `ops-mongo` 是 ops 栈内建容器，无需外部账号。

---

## 4. 单机部署（Docker Compose）

**完整可部署蓝本见 [`allinone/ops.yaml.example`](allinone/ops.yaml.example)**（9 service 共用镜像 + 各自 `ROLE`，占位符按实际替换）。下面说明结构与关键改动，实际部署直接拷贝该蓝本改。

### 4.1 结构（9 service，同镜像不同 ROLE）

用 YAML 锚点把公共 env 复用到需要它的角色（gateway/prometheus/grafana/agent/nodeagent）：

```yaml
version: '3'
services:
  gateway:
    image: registry.cn-hangzhou.aliyuncs.com/mdpublic/ops-allinone:1.5.12
    ports: ["48881:48881"]
    environment: &common_env
      TZ: Asia/Shanghai
      ENV_OPS_TOKEN: your-ops-token          # 登录令牌，9 容器须一致
      ENV_OPS_SUB_PATH: /mdis                # 子路径前缀；根路径访问设空串 ''
      # —— 告警子系统（1.4.0）——
      ENV_OPS_MONGODB_URI: mongodb://ops-mongo:27017
      ENV_MONGODB_COLLECTION_PREFIX: mdis_
      ENV_ALERT_CRYPTO_KEY: your-32-byte-random-secret   # openssl rand -hex 16
      # —— 数据源服务发现 ——
      ENV_OPS_SD_URL: http://gateway:8081     # ops-server 直连端口，不是网关的 48881
      # ⚠️ 被监控对象（MySQL/Mongo/Redis/Kafka/ES/主机/K8s）与自定义页面不在这里配，
      #    装完打开「数据源」页添加，或下载 YAML 模板批量导入。见 §6。
      ROLE: gateway
    depends_on: [ops-prometheus, ops-grafana, ops-loki, ops-tempo, ops-alloy, ops-mongo]

  ops-prometheus:
    image: registry.cn-hangzhou.aliyuncs.com/mdpublic/ops-allinone:1.5.12
    volumes: ["./volume/data/prometheus/server/:/data/prometheus/server/"]
    environment:
      <<: *common_env
      ROLE: prometheus

  ops-grafana:
    image: registry.cn-hangzhou.aliyuncs.com/mdpublic/ops-allinone:1.5.12
    volumes: ["./volume/data/prometheus/grafana/:/var/lib/grafana/"]
    environment:
      <<: *common_env
      GF_SECURITY_ALLOW_EMBEDDING: 'true'    # 允许 iframe 嵌入
      GF_AUTH_ANONYMOUS_ENABLED: 'true'
      GF_AUTH_ANONYMOUS_ORG_ROLE: Editor
      GF_USERS_DEFAULT_THEME: light
      ROLE: grafana

  ops-mongo:                                  # 告警子系统自有存储
    image: registry.cn-hangzhou.aliyuncs.com/mdpublic/ops-allinone:1.5.12
    volumes: ["./volume/data/mongo/:/data/db"]
    restart: always
    environment: { ROLE: mongo }

  ops-loki:
    image: registry.cn-hangzhou.aliyuncs.com/mdpublic/ops-allinone:1.5.12
    ports: ["3100:3100"]
    volumes: ["./volume/data/loki/:/loki/"]
    environment: { ENV_LOKI_RETENTION: 720h, ROLE: loki }

  ops-tempo:
    image: registry.cn-hangzhou.aliyuncs.com/mdpublic/ops-allinone:1.5.12
    volumes: ["./volume/data/tempo/:/var/tempo/"]
    environment: { ENV_TEMPO_RETENTION: 720h, ROLE: tempo }

  ops-alloy:
    image: registry.cn-hangzhou.aliyuncs.com/mdpublic/ops-allinone:1.5.12
    ports: ["4317:4317", "4318:4318"]
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock
      - ./volume/data/alloy/:/var/lib/alloy/data/
    environment:
      ENV_TEMPO_GRPC_URL: http://ops-tempo:4317
      ENV_PROMETHEUS_REMOTE_WRITE_URL: http://ops-prometheus:9090/prometheus/server/api/v1/write
      ENV_LOKI_PUSH_URL: http://ops-loki:3100/loki/api/v1/push
      ROLE: alloy

  agent:
    image: registry.cn-hangzhou.aliyuncs.com/mdpublic/ops-allinone:1.5.12
    environment:
      <<: *common_env
      ROLE: agent

  nodeagent:
    image: registry.cn-hangzhou.aliyuncs.com/mdpublic/ops-allinone:1.5.12
    volumes: ["/:/host:ro,rslave"]
    network_mode: host
    pid: host
    environment:
      <<: *common_env
      ROLE: nodeagent

# 直接复用 HAP 所在的网络，这样才能用服务名（如 sc）访问被监控的中间件。
# ⚠️ 键名必须是 default：compose 只会把「没写 networks 的 service」接进名为 default
# 的网络。此前这里写成 `script_default:`，顶层声明了却没有任何 service 引用它，
# 九个容器实际都进了 <目录名>_default，与 HAP 不在同一张网——填 mongodb://sc:27017
# 这类服务名一律解析不到，表现为「连接测试不过 / 面板没数据」，而配置看着完全正确。
# name 填 docker network ls 里 HAP 那个网络的实际名字（安装目录叫 script 时即 script_default）。
networks:
  default:
    external: true
    name: script_default
```

> `ops.yaml.example` 里是把公共 env **平铺展开**在每个 service（不用锚点），二者等价，选一种即可。

### 4.2 网络

末尾的 `networks.default` 直接复用 HAP 所在的网络，`name` 填 `docker network ls` 里 HAP 那个网络的实际名字（HAP 安装目录叫 `script` 时即 `script_default`）。这样起出来的容器和 HAP 在同一张网，数据源里才能用服务名（如 `mongodb://sc:27017`）填被监控中间件。

⚠️ 键名必须是 `default`。compose 只把「没写 `networks` 的 service」接进名为 `default` 的网络；写成 `script_default:` 的话顶层声明了却没有任何 service 引用，九个容器实际会进 `<目录名>_default`，与 HAP 不在同一张网——服务名一律解析不到，表现为「连接测试不过 / 面板没数据」，而配置看着完全正确。

机器上没有 HAP、要独立起一套时，把 `external: true` 和 `name` 两行删掉，让 compose 自己建网即可（此时被监控目标只能填 IP，没有服务名可用）。

### 4.3 子路径 vs 根路径

- **子路径**（贴生产，外层 openresty/nginx 反代）：`ENV_OPS_SUB_PATH: /mdis`，对外 `http://<域名>/mdis/web/`。外层反代加：
  ```nginx
  location /mdis/ {
      proxy_pass http://127.0.0.1:48881;
      proxy_http_version 1.1;
      proxy_set_header Upgrade $http_upgrade;
      proxy_set_header Connection "upgrade";
  }
  ```
  > `proxy_http_version 1.1` + WebSocket header 必配，否则 Grafana 实时功能异常。
- **根路径**（直接端口访问）：`ENV_OPS_SUB_PATH` 设空串 `''`，访问 `http://<ip>:48881/web/`。

### 4.4 启动 / 验证

```bash
docker compose -f ops.yaml up -d
docker compose -f ops.yaml ps                                   # 9 容器 Up
curl -s -o /dev/null -w '%{http_code}\n' http://<ip>:48881/mdis/web/   # 期望 200
```

浏览器开对应地址，用 `ENV_OPS_TOKEN` 登录。

> ✅ **1.4.0 懒连接**：中间件（mongo/mysql/redis/kafka/es）连不上不致命，gateway 照常启动，相关页面在配数据源时才需真实地址。`ENV_OPS_MONGODB_URI`（告警存储）连不上只是禁用告警子系统，不崩入口。

---

## 5. 告警子系统（1.4.0 自研）

MDIS 1.4.0 起内置**自研告警子系统**（取代旧的 Grafana 自带告警 iframe）：主机/端口/SSL/Kafka 堆积/Kafka 重平衡/MongoDB/MySQL 七类监控引擎 + 降噪状态机 + 多渠道 webhook。

- **存储**：专用 `ops-mongo` 容器（`ENV_OPS_MONGODB_URI`，库 `mdservicedata`，集合前缀 `ENV_MONGODB_COLLECTION_PREFIX`，默认 `mdis_`），与被监控目标解耦。
- **凭据加密**：渠道密钥用 AES 加密，密钥 `ENV_ALERT_CRYPTO_KEY`（32 字节，`openssl rand -hex 16`）。
- **通知渠道**：飞书 / 钉钉 / 企业微信群机器人，**在 UI「通知渠道」页配置**（不走环境变量）。
- **接口**：`/api/alert/*`，复用 gateway 的 `atoken` 鉴权。

> 邮件相关的 `ENV_SMTP_*` 只作用于 **Grafana 自身**（`GF_SMTP_*`），是可选/遗留项；MDIS 告警不依赖它，默认可不配。

---

## 6. 数据源管理（1.5.0：唯一入口）

> **判据只有一条：`ops.yaml` 只描述「平台自己要用什么」，不描述「要监控谁」。**
> 被监控对象一律在 UI「数据源」页配置。

「数据源」页管理这几类，每条带 `enabled` / `purposes`：

| 类型 | 说明 |
|---|---|
| Prometheus | 平台自带，内置单例、只读（唯一保留 `builtin` 身份的） |
| MySQL / MongoDB / Redis / Kafka / Elasticsearch | 可配多个 |
| 主机（node_exporter） | 可配多个 |
| Kubernetes 集群（static 外接） | 多集群，UI 配 KSM/Node/Kubelet 目标 + BearerToken（见 §9） |
| 自定义页面 | 可配多个：把任意 Web UI（Flink、Langfuse、内部看板…）挂进左侧「自定义」菜单。每条有名称 + 地址 + 嵌法 + 开关；不配则整组不显示 |

`purposes` 取值：`metrics`（纳入指标采集/看图）、`alert`（纳入告警）、`diagnose`（仅 MongoDB：慢查询诊断目标）。

改完**即时生效**：ops-agent 每 30s 从 ops-server 的 `/api/sd/sources` 对账，
按需拉起/回收 exporter；ops-prometheus 的 file_sd 同样由注册中心下发，热加载。
改数据源不用重启任何容器。

### 批量导入

数据源页右上角「批量导入」，支持 YAML / JSON：

- 先「下载 YAML 模板」（`GET /api/alert/sources/import-template`），六类数据源 + 自定义页面各一条带注释的示例
- 建议先跑「仅校验」（`?dryRun=true`）：只做格式校验 + 逐条探测连接，不写库
- 按 `(类型, 名称)` 幂等 upsert——同名条目是更新而不是新建，同一份文件可反复导入
- 单条失败不影响其余条目
- **探测不通不阻止导入**（配置可以先落地、服务稍后再起），但结果里逐条标出
- **自定义页面（`kind: customPage`）也走这套**（1.5.12 起）：导出/导入都包含它，
  重装后外链入口不用一条条手敲。`mode` 会原样带走 —— 只能直嵌的页面被恢复成反代后
  打开是一片空白且看不出哪错了，所以别手动改这个字段

自动化部署走同一个接口即可，不必经过页面：

```bash
curl -X POST "http://<gateway>:48881/mdis/api/alert/sources/import" \
     -H "Content-Type: application/yaml" \
     --data-binary @datasources.yaml
```

### 1.5.0 变更：移除 ENV_* 配置数据源

1.4.x 支持在 `ops.yaml` 用 `ENV_MYSQL_*` / `ENV_REDIS_*` / … 配置被监控对象，
首次启动播种成数据源。**1.5.0 整段移除**，原因是它构成了第二个入口：

- 两套语法：ENV 多目标用 `;` 分隔，UI 用换行/逗号
- 只生效一次：播种后改 `ops.yaml` 不再生效，而配置文件还摆在那里
- 配错零反馈：写错的地址照样落库，UI 看着正常、采集器不报错

实际踩过的例子：三节点 MySQL 写成 `ENV_MYSQL_HOST=h1,h2,h3` +
`ENV_MYSQL_PORT=3306,3307,3308`，播种出一条
`nodes=["h1,h2,h3:3306,3307,3308"]` 的假地址，一层层往回刨才能发现。

现在这些变量**不再被读取**。ops-server 启动时会扫一遍，
配了就在日志里明确列出并提示去数据源页配——不留静默忽略。

---

## 7. 环境变量总表

> 权威口径 = `ops.yaml.example` + 代码实际读取。空默认值表示不填则该功能关闭/不生效。

### 7.1 平台核心

| 变量 | 必填 | 默认 | 说明 |
|---|---|---|---|
| `ROLE` | 是 | — | 角色分发：`gateway`/`prometheus`/`grafana`/`agent`/`nodeagent`/`loki`/`tempo`/`alloy`/`mongo`/`ksm` |
| `ENV_OPS_TOKEN` | 是 | `admin` | 登录令牌，各容器须一致 |
| `ENV_OPS_SUB_PATH` | 否 | 空 | 子路径前缀（如 `/mdis`），空串=根路径 |
| `ENV_OPS_PORT` | 否 | `8081` | 后端 ops-server 端口（容器内） |
| `ENV_OPS_GATEWAY_PORT` | 否 | `48881` | 网关 nginx 对外端口 |
| `TZ` | 否 | — | 时区，建议 `Asia/Shanghai` |

### 7.2 告警子系统

| 变量 | 必填 | 默认 | 说明 |
|---|---|---|---|
| `ENV_OPS_MONGODB_URI` | 启用告警必填 | — | 告警自有存储 ops-mongo，如 `mongodb://ops-mongo:27017` |
| `ENV_MONGODB_COLLECTION_PREFIX` | 否 | `mdis_` | 告警集合前缀 |
| `ENV_ALERT_CRYPTO_KEY` | 启用告警必填 | — | 渠道凭据 AES 密钥，32 字节 |

### 7.3 慢查询目标 MongoDB

> 无环境变量。慢查询目标 = 「数据源」页里勾了 **`diagnose`** 用途的 MongoDB 实例。
> 配多个 = HAP 分片场景：含 `mdworksheet` 元数据的那台自动作为翻译源，其余作扫描分片。
> 一个都没配时慢查询页不可用，启动日志会明确说明。

### 7.4 Prometheus

| 变量 | 必填 | 默认 | 说明 |
|---|---|---|---|
| `ENV_PROMETHEUS_SERVER` | 否 | `http://ops-prometheus:9090` | Prometheus 地址 |
| `ENV_PROMETHEUS_INTERNAL_URL` | 否 | — | Prometheus 内部子路径 URL（remote_write / SD 用） |
| `ENV_PROMETHEUS_GRAFANA` | 否 | `http://ops-grafana:3000` | Grafana 地址（gateway 反代用） |
| `ENV_PROMETHEUS_RETENTION` | 否 | — | 指标保留时长 |

### 7.5 数据源服务发现

| 变量 | 必填 | 默认 | 说明 |
|---|---|---|---|
| `ENV_OPS_SD_URL` | 是 | `http://gateway:8081` | ops-server SD 端点。⚠️ 是 ops-server 直接监听的 **8081**，不是网关对外的 48881（那是 nginx，没有 `/api/sd` 路由）。K8s 部署时 Service 必须同时暴露这两个端口 |
| `ENV_OPS_SD_TOKEN` | 否 | — | SD 接口鉴权 token（空=内网放行） |
| `ENV_AGENT_SD_HOST` | 否 | `agent` | agent 服务名（SD 目标回填用） |

> 被监控中间件（MySQL/Redis/Kafka/ES/MongoDB/主机）**没有环境变量**，全部在「数据源」页配置，见 §6。

### 7.6 K8s 监控

| 变量 | 默认 | 说明 |
|---|---|---|
| `ENV_K8S_MONITOR_MODE` | `off` | `off` / `static`（单机外接）/ `incluster`（集群内置） |
| `ENV_K8S_KSM_NAMESPACE` | `mdis-monitoring` | incluster：kube-state-metrics 所在命名空间 |

> `static` 模式下具体集群（KSM/Node/kubelet 目标 + BearerToken）在「数据源」页
> 添加「Kubernetes 集群」数据源，支持多集群、热加载。

### 7.7 自定义页面（Flink / Langfuse / 任意内部 Web UI）

> 无环境变量。在「数据源」页点「新增 → 自定义页面」，填名称 + 地址 + 嵌入方式，
> 保存后左侧「自定义」分组即出现该入口（停用或删除即消失）。可配任意多条。
>
> **两种嵌入方式怎么选**：
> - **反代**（默认）：请求经 gateway → ops-server 转到目标，浏览器看到的是同源地址、
>   带平台鉴权，目标不必对浏览器可达。适合资源用相对路径的服务（Flink 就是这类）。
> - **直接嵌入**：浏览器直接加载目标地址。要求目标对浏览器可达、且没有
>   `X-Frame-Options` / CSP `frame-ancestors` 限制。资源走绝对路径的应用
>   （Next.js 那种 `/_next/*`，如 Langfuse）只能用这种——反代到子路径下静态资源会 404。
>
> 1.5.11 及以前这里是写死的「Flink 入口」单例，升级后自动迁成一条自定义页面
> （名称 Flink、访问路径 `/custom/flink`），无需手工重配。

### 7.8 可观测性（Loki / Tempo / Alloy / S3）

| 变量 | 默认 | 说明 |
|---|---|---|
| `ENV_LOKI_URL` | — | **HAP 侧**结构化日志 push 目标（`http://ops-loki:3100`），HAP 容器配 |
| `ENV_LOKI_PUSH_URL` | — | alloy → loki 推送地址 |
| `ENV_LOKI_RETENTION` | `720h` | Loki 日志保留时长 |
| `ENV_TEMPO_GRPC_URL` | — | alloy → tempo OTLP gRPC（`http://ops-tempo:4317`） |
| `ENV_TEMPO_URL` | `http://ops-tempo:3200` | Tempo 查询地址（gateway 反代用） |
| `ENV_TEMPO_RETENTION` | `720h` | Tempo trace 保留时长 |
| `ENV_ALLOY_URL` | `http://ops-alloy:12345` | Alloy 地址（gateway 反代用） |
| `ENV_PROMETHEUS_REMOTE_WRITE_URL` | — | alloy metrics remote_write 到 prometheus |
| `ENV_S3_ENDPOINT` | — | **填了就切 S3 后端，不填走 fs**（Loki / Tempo 共用）。填 `host:port`，**不要带 `http://`**——Tempo 的 minio-go 只吃 `host:port`，带 scheme 直接 CrashLoop；Loki 侧的协议由 `loki-config-s3.yaml` 的 `insecure: true` 控制，同样不需要 scheme。写了 scheme 也不会崩（`roles/tempo.sh` 会剥掉），但以不带为准 |
| `ENV_S3_BUCKET_LOKI` | `loki` | 日志桶。⚠️ **部署前须先在对象存储里建好**，桶不存在时 Loki 报 `NoSuchBucket` 直接 CrashLoop |
| `ENV_S3_BUCKET_TEMPO` | `tempo` | 链路桶，**必须与 Loki 不同**。同样需先建好 |
| `ENV_S3_BUCKET` | — | 兼容旧部署的通用桶名，两个专属变量都没设时才回退到它。⚠️ **新部署别用**：它会同时喂给 Loki 和 Tempo，两者落进同一个桶。处于这种状态时 Loki/Tempo 启动日志会打 `[s3] ⚠ Loki 与 Tempo 正共用同一个 bucket …` 提示拆分 |
| `ENV_S3_ACCESS_KEY` / `_SECRET_KEY` | — | S3 凭据 |

### 7.9 进阶 / 可选

| 变量 | 说明 |
|---|---|
| `ENV_GATEWAY_PROFILE_SLOW_MS` / `_DOCS_EXAMINED` / `_CAPTURE_INTERVAL_MS` / `_LOG_EXPIRE_MS` | 慢查询采集阈值/间隔/保留调优 |
| `ENV_MONGODB_META_OVERRIDES` | 慢查询元数据映射覆盖 |
| `ENV_SMTP_*` | Grafana 自身邮件（`GF_SMTP_*`）；MDIS 告警不用，可选/遗留 |

---

## 8. 可观测性配置

### 数据流总览

```
HAP logservice / housekeeper  ──HTTP push──▶  ops-loki   ──▶  Grafana
Docker 容器 stdout            ──ops-alloy──▶  ops-loki   ──▶  Grafana
应用 OTLP（gRPC/HTTP）        ──ops-alloy──▶  ops-tempo  ──▶  Grafana Explore
node_exporter / 各 exporter    ──────────▶  ops-prometheus ──▶  Grafana
```

- **容器控制台**面板：自动采集所有 Docker 容器 stdout（Alloy 已排除 ops 自身容器），零配置。
- **服务日志**面板：需 HAP 微服务 push 结构化日志到 ops-loki（见下）。
- **链路追踪**：k8s + Istio sidecar 自动产生 spans 已验证；docker-compose 单机需应用内置 OTel SDK 推到 `ops-alloy:4317/4318`。

### 对接 HAP 服务日志

HAP 一体化容器的 `appsettings.json` 启动时由 entrypoint 用环境变量渲染，客户在 HAP 服务 docker-compose 加：

```yaml
environment:
  ENV_LOKI_URL: "http://ops-loki:3100"
```

| `appsettings.json` 字段 | 环境变量 | 行为 |
|---|---|---|
| `LokiUrl` | `ENV_LOKI_URL` | Loki 接收端 URL |
| `StoreInLoki` | `ENV_LOG_ENABLE_LOKI` | entrypoint 自动推：`ENV_LOKI_URL` 非空=`true` |
| `RedisUrl` | `ENV_REDIS_LOG` | Redis 连接串（同时存 Redis 时用） |
| `StoreInRedis` | `ENV_LOG_ENABLE_REDIS` | entrypoint 自动推：`ENV_REDIS_LOG` 非空=`true` |

**前提**：HAP 与 ops 同主机、同 docker network，`ops-loki` DNS 名才能解析。跨主机则给 ops-loki 加 `ports: ["3100:3100"]`，`ENV_LOKI_URL` 改宿主机 IP。

> ⚠️ `appsettings.json` 的 sed 渲染只在**首次启动**（占位符还在时）生效；之后改 env 重启不生效，需 `docker compose down && up -d` 重建容器。

### Loki 存储后端（fs / S3）

entrypoint 按 `ENV_S3_ENDPOINT` 是否设置自动切换。默认 fs（容器内 `/loki/` → 宿主 `./volume/data/loki/`）；填 S3 参数（见 §7.9）即切 S3。

### Alloy 接收端口

| 端口 | 协议 | 用途 |
|---|---|---|
| `4317` | OTLP gRPC | 应用 OTLP push（traces/metrics/logs） |
| `4318` | OTLP HTTP | 同上，HTTP 协议 |

应用用 OpenTelemetry SDK 推到 `ops-alloy:4317`，alloy fan-out：traces→Tempo、metrics→Prometheus（remote_write）、logs→Loki。

---

## 9. K8s 监控接入

采集集群对象状态（kube-state-metrics）、节点资源（node_exporter）、容器资源（cadvisor，仅 incluster）。两种模式：

- **static（单机外接）**：MDIS 以 docker-compose 部署在客户集群外。给 ops-prometheus 配 `ENV_K8S_MONITOR_MODE=static`，然后在 UI「数据源」页添加「Kubernetes 集群」数据源，填 KSM/Node/Kubelet 目标（NodePort 与节点 `:10250`）+ BearerToken（需 `nodes/proxy` 权限）。支持多集群、热加载；ops 需网络可达节点相应端口。
- **incluster（集群内置）**：ops-prometheus 作为 Pod 跑在被监控 k8s 内，用 ServiceAccount + apiserver proxy 自动发现 kubelet/cadvisor/kube-state-metrics，无需暴露 kubelet、无需 kubeconfig。

前端「资源 → K8s」菜单展示对应 Grafana 仪表盘（uid `k8s-cluster`）。

> K8s 部署清单在 [`allinone/k8s/`](allinone/k8s/)（已切 allinone 镜像 + `ROLE`；k8s 环境完整验证进行中，以清单实际为准）。控制面（apiserver/scheduler/cm）与独立 Alertmanager 不在范围。

---

## 10. 常用运维命令

```bash
# 看某角色日志
docker logs script-gateway-1 --tail 50
docker exec script-gateway-1 cat /usr/local/nginx/conf/conf.d/gateway.conf   # 渲染后的 nginx 配置

# 改镜像/配置后重建（只重建变更 service）
docker compose -f ops.yaml up -d
docker compose -f ops.yaml restart gateway

# 离线导出/导入（单镜像）
docker save registry.cn-hangzhou.aliyuncs.com/mdpublic/ops-allinone:1.5.12 -o ops.tar
docker load -i ops.tar
```

---

## 11. 部署后回归（推荐）

改完功能/重建镜像后跑整体 E2E（Playwright），退出码 0 = 全过：

```bash
# 子路径形态
python3 test/mdis_regression.py --base http://<host>:9080 --subpath /mdis --token <ENV_OPS_TOKEN>
# 根路径 / NodePort 直连
python3 test/mdis_regression.py --base http://<host>:48881 --token <ENV_OPS_TOKEN>
```

覆盖：登录、全页面走查、Grafana 面板可达、数据源 CRUD、PromQL 预览、告警全链路、慢查询/全局参数 API、页面 JS 零错误。

---

## 12. 踩坑速查

| 现象 | 原因 / 解法 |
|---|---|
| `up` 报 network `script_default` not found | 本机没装 HAP → 删掉末尾 `networks.default` 的 `external: true` 与 `name` 两行，让 compose 自己建网 |
| 整页空白（子路径） | `<base href>` 由 gateway entrypoint 按 `ENV_OPS_SUB_PATH` 启动注入，非 build 产物；用正式 `build.sh` 镜像即可 |
| 登录提示 token 错 | `ENV_OPS_TOKEN` 各容器须一致；不设默认 `admin` |
| 告警页报错/无数据 | `ops-mongo` 没起或 `ENV_OPS_MONGODB_URI` 不对；`ENV_ALERT_CRYPTO_KEY` 必须 32 字节 |
| 慢查询页空 | 「数据源」页没有勾了「诊断」用途的 MongoDB 实例（懒连接，不影响启动） |
| 资源监控 iframe 空白 | `ops-prometheus`/`ops-grafana` 没起或被删 |
| 「自定义」菜单不显示 | 「数据源」页一条自定义页面都没配、或都停用了（预期行为） |
| 自定义页面打开是空白 | 目标禁止被 iframe 嵌入（浏览器控制台会有 `frame-ancestors`/`X-Frame-Options` 报错）→ 改用「反代」，或在目标侧放开；反代模式下静态资源 404 则相反——改用「直接嵌入」 |
| 服务日志改 `ENV_LOKI_URL` 不生效 | sed 只首启生效 → HAP 容器 `down && up -d` 重建 |

---

## 附：接口文档

后端 REST API 见 [`../server/API.md`](../server/API.md)（1.4.0 全站 kebab-case + JSON camelCase，含告警子系统 `/api/alert/*`）。
