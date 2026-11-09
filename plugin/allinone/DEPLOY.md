# MDIS 运维平台 —— 开发测试环境部署手册（1.5.0）

面向想自己拉一套 MDIS（ops 监控平台）来开发/联调/验证的同事。
两种形态都覆盖：**A. 独立单机最小环境**（自己起一套全新的）/ **B. 接入已有 HAP 栈**（贴生产形态）。

> 形态说明见 [`README.md`](./README.md)：1.4.0 起全栈打进**一个镜像** `ops-allinone`，运行时由 `ROLE` 环境变量分发成 9 个角色容器。部署蓝本是 [`ops.yaml.example`](./ops.yaml.example)，本文是它的「怎么用 + 避坑」。

---

## 0. 前置条件

- Docker + Docker Compose（v2，`docker compose` 子命令）
- 一台能跑 9 个容器的机器（镜像 2.16GB，建议 ≥4C8G、磁盘 ≥20G）
- **镜像来源二选一**（见 §1）：能 `docker pull` 阿里云 registry，或本机有 Go 1.23 + yarn 自己构建

---

## 1. 拿到镜像

### 优先：直接 pull（推荐）

```bash
docker pull registry.cn-hangzhou.aliyuncs.com/mdpublic/ops-allinone:1.5.12
```

> ⚠️ 近期功能迭代多以 `PUSH=0` 只在构建机本地出镜像（tag 带后缀如 `1.4.0-i18n`），registry 上的 `:1.4.0` 可能不是最新功能。要最新的，找镜像维护者确认 tag，或走下面自己构建。

### 备选：自己构建

```bash
cd plugin/allinone
PUSH=0 ./build.sh                    # 只 build 不推；IMAGE=... 可覆盖 tag
```

`build.sh` 做三件事：yarn 编前端 `dist/` → go 编后端 `ops-server` → 以本目录为上下文 build 单镜像。

> **构建前置坑**：
> - 需要 Go 1.23.3 + Node/yarn 在本机；
> - 根目录 Dockerfile 从上游容器镜像提取 Prometheus 和 exporter。云构建可启用海外构建或海外源加速。
> - 首次构建较慢（拉 6 个上游镜像 + 下载组件）。

---

## 2. 形态 A：独立单机最小环境（最常用）

目标：自己 docker 起一套全新的，不依赖任何现成的网络/中间件，能登录、看页面、验告警。

### 2.1 准备网络

`ops.yaml.example` 末尾的 `networks.default` 复用 HAP 所在的网络（`name: script_default`）。本机没有 HAP、要独立起一套时，把 `external: true` 和 `name` 两行删掉让 compose 自己建网即可；此时没有服务名可用，被监控目标只能填 IP。

⚠️ 键名必须是 `default`——compose 只把没写 `networks` 的 service 接进名为 `default` 的网络。写成 `script_default:` 会「声明了但没人用」，容器实际进 `<目录名>_default`，与 HAP 不同网，`mongodb://sc:27017` 这类服务名解析不到。

### 2.2 写 compose

拷贝 `ops.yaml.example` 为 `ops.yaml`，**最小改动**如下（其余占位符联调时再填）：

| 变量 | 改成 | 说明 |
|---|---|---|
| `ENV_OPS_TOKEN` | 自定义一个 token（如 `dev123`） | 登录令牌；不设默认 `admin` |
| `ENV_OPS_SUB_PATH` | 不填（默认 `/mdis`） | 默认子路径 `/mdis`，代理层可直接反代；想根路径访问显式设空串 `''` |
| `ENV_ALERT_CRYPTO_KEY` | 32 字节随机串 | 告警渠道凭据 AES 加密，`openssl rand -hex 16` |
| `image:` 各行 | 你 §1 拿到的 tag | 9 处统一 |

**被监控中间件不用在 ops.yaml 里配**（1.5.0 起）。起栈时数据源页是空的，装完在 UI 里加，
或下载 YAML 模板批量导入。所以最小环境这一步没有任何中间件变量要填。

> ✅ 连不上不致命：`ENV_OPS_MONGODB_URI`（告警自有存储 ops-mongo）连不上只是禁用告警子系统，
> 不崩整个入口；数据源页里的目标同样是懒连接，配了连不上也只影响对应页面。

### 2.3 起栈

```bash
docker compose -f ops.yaml up -d
docker compose -f ops.yaml ps          # 9 个容器都 Up
```

### 2.4 验证

```bash
# UI 就绪（默认子路径 /mdis；若设空串则是 /web/）
curl -s -o /dev/null -w '%{http_code}\n' http://<host>:48881/mdis/web/   # 期望 200
docker logs script-gateway-1 --tail 20 | grep 'Server is running'      # 后端 8081 起来
```

浏览器开 `http://<host>:48881/mdis/web/`，用 `ENV_OPS_TOKEN` 登录。

### 2.5 最小子集（只想看 UI，不要全套监控）

严格说能登录看页面只需 **gateway + ops-mongo**（告警子系统存储）两个容器；
`ops-prometheus`/`ops-grafana` 提供资源监控 iframe 页，`loki`/`tempo`/`alloy`/`agent`/`nodeagent` 提供日志/链路/exporter 数据。按需删减 compose 里的 service 即可（注意删 service 时同步删 gateway 的 `depends_on`）。

---

## 3. 形态 B：接入已有 HAP 栈（贴生产，如 local_50）

和 HAP 一体化栈并存、共享 `script_default` 网、监控同一批中间件时：

- **网络**：用现成的 `script_default`（HAP 栈已建），compose 末尾保持 `networks.default` 的 `external: true` + `name: script_default`，**不要**再 `docker network create`。
- **子路径**：外层有 openresty 反代时 `ENV_OPS_SUB_PATH: /mdis`，对外 `http://<host>:9080/mdis/web/`（48881 只在内网）。
  - ⚠️ 子路径下 `index.html` 的 `<base href="/mdis/web/">` 由 gateway entrypoint 按 `ENV_OPS_SUB_PATH` **启动时注入**，不是 build 产物。热替换前端进容器要手动补这行，否则整页空白（正式 `build.sh` 出的镜像无此问题）。
- **中间件**：不在 ops.yaml 配。起栈后进「数据源」页添加真实被监控目标（只读观测），
  或下载 YAML 模板批量导入（导入时会逐条测连接）。慢查询目标 = 勾了「诊断」用途的 MongoDB。
- **`ENV_OPS_MONGODB_URI` 是平台自有库**（`ops-mongo` 容器，库 `mdservicedata`，存告警规则/数据源/慢查询历史），
  不是被监控对象。1.5.0 前另有一个只差三个字母的 `ENV_MONGODB_URI` 指被监控 mongo，最容易混——该变量已废弃。
- 完整环境变量清单见 [`../README.md`](../README.md)（可观测性配置章节）。

local_50 现网实际部署在 `/data/mingdao/script/`，活动栈 `ops-allinone.yaml`（含真实密钥，不入库）。

---

## 4. 常用运维命令

```bash
# 看某角色日志
docker logs script-gateway-1 --tail 50
docker exec script-gateway-1 cat /usr/local/nginx/conf/conf.d/gateway.conf   # 看渲染后的 nginx 配置

# 改了镜像/配置后重建（只重建变更 service）
docker compose -f ops.yaml up -d

# 单独重启某角色
docker compose -f ops.yaml restart gateway
```

容器名对照：`script-gateway-1`、`script-agent-1`、`script-nodeagent-1`、`script-ops-{prometheus,grafana,alloy,loki,tempo,mongo}-1`。

---

## 5. 部署后回归（可选但推荐）

改完功能/重建镜像后跑整体 E2E（Playwright），退出码 0 = 全过：

```bash
# 独立环境（默认子路径 /mdis）
python3 test/mdis_regression.py --base http://<host>:48881 --subpath /mdis --token <ENV_OPS_TOKEN>
# 子路径形态
python3 test/mdis_regression.py --base http://<host>:9080 --subpath /mdis --token <ENV_OPS_TOKEN>
```

覆盖：登录、全页面走查、Grafana 面板可达、数据源 CRUD、PromQL 预览、告警全链路、慢查询/全局参数 API、页面 JS 零错误。

---

## 6. 踩坑速查

| 现象 | 原因 / 解法 |
|---|---|
| `up` 报 network `script_default` not found | 本机没装 HAP → 删掉末尾 `networks.default` 的 `external: true` 与 `name` 两行，让 compose 自己建网 |
| 整页空白（子路径） | `<base href>` 没注入 → 用正式 `build.sh` 镜像，或手动补 §3 |
| 登录提示 token 错 | `ENV_OPS_TOKEN` 不设默认 `admin`；9 个 service 的 token 要一致 |
| 告警页报错/无数据 | `ops-mongo` 没起或 `ENV_OPS_MONGODB_URI` 不对；`ENV_ALERT_CRYPTO_KEY` 必须 32 字节 |
| 慢查询页空 | 「数据源」页没有勾了「诊断」用途的 MongoDB 实例（懒连接，不影响启动） |
| 资源监控 iframe 空白 | `ops-prometheus`/`ops-grafana` 没起，或这俩 service 被删了 |
| 拉取上游镜像超时 | 检查构建机访问 Docker Hub 和 Kubernetes 官方镜像仓库的网络，ACR 可启用海外构建或海外源加速 |
