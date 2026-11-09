### 接口文档

---

#### 接口：认证接口

- **接口名称**：认证
- **接口路径**：`/api/auth`
- **请求方法**：POST

- **请求头**：
  - `Authorization`：传入的 Token 值

- **请求说明**：
  - 前端将 Token 放在 `Authorization` 请求头中进行验证。

- **响应说明**：
  - **成功响应**：
    - **状态码**：200
    - **说明**：认证成功，后端从响应中提取 Token 并设置 Cookie：`atoken=传入的Token值`。
  - **失败响应**：
    - **状态码**：401
    - **说明**：认证失败，无需返回内容。

---

#### 接口：获取 iframe URL 列表接口

- **接口名称**：获取 iframe URL 列表
- **接口路径**：`/api/iframe-urls`
- **请求方法**：GET

- **请求说明**：
  - 请求需携带第一个接口成功后设置的 Cookie `atoken`。

- **响应说明**：
  - **状态码**：200
  - **说明**：成功返回包含 iframe URL 的 JSON 对象。
  - **响应内容**：

    ```json
    {
      "resource": {
        "host": "/prometheus/grafana/d/hap-node-exporter/server-resource-monitoring?orgId=1&kiosk",
        "mysql": "/prometheus/grafana/d/MQWgroiiz/mysql-dashboard?orgId=1&kiosk",
        "mongodb": "/prometheus/grafana/d/fwPKIsnih/mongodb-dashboard?orgId=1&kiosk",
        "redis": "/prometheus/grafana/d/xDLNRKUWz/redis-dashboard?orgId=1&kiosk",
        "kafka": "/prometheus/grafana/d/jwPKIsniz/kafka-dashboard?orgId=1&kiosk",
        "elasticsearch": "/prometheus/grafana/d/4yyL6dBMk/elasticsearch-dashboard?orgId=1&kiosk",
        "k8s": "/prometheus/grafana/d/PwMJtdvnz/kubernetes-dashboard?orgId=1&kiosk"
      },
      "log": {
        "containerConsole": "/prometheus/grafana/d/ContainerCons/?orgId=1&kiosk",
        "serviceLogs": "/prometheus/grafana/d/ServiceLogs/?orgId=1&kiosk"
      },
      "trace": {
        "tempo": "/prometheus/grafana/d/TempoDashbrd/tempo-dashboard?orgId=1&kiosk"
      },
      "custom": [
        { "key": "custom/flink", "name": "Flink", "url": "/custom/flink/" },
        { "key": "custom/langfuse", "name": "Langfuse", "url": "http://langfuse:3000" }
      ]
    }
    ```

    > `custom` 是「自定义页面」（1.5.12 起，Flink 也是其中一条）：每条已启用的配置出一个菜单项，
    > `key` 同时是前端路由（`/custom/<slug>`）；`url` 按嵌法给——反代模式给平台内路径，
    > 直嵌模式原样给目标地址。一条都没配时是空数组，前端不显示「自定义」分组。
    > 子路径部署时各 URL 会带 `X-Forwarded-Path` 前缀（如 `/mdis`）。

---

#### 接口：获取历史慢查询列表

- **接口名称**：查询慢查询操作记录
- **接口路径**：`/api/slow-query/list`
- **请求方法**：POST

- **请求说明**：
  - 请求需携带第一个接口成功后设置的 Cookie `atoken`。
  - 请求体需包含以下参数：
    - `page`：设置页码
    - `pageSize`：设置返回的数据条数

- **请求体示例**：

    ```json
    {
      "page": 1,
      "pageSize": 10,
      "searchText": ""
    }
    ```

- **响应说明**：
  - **状态码**：200
  - **说明**：成功返回包含慢查询操作记录的 JSON 对象。
  - **响应内容**：

    ```json
    {
      "count": 21,  // 慢查询历史表的记录总数，不是指本次返回数据的计数
      "data": [
        {
          "_id": "676a5c1ca5c00dd12f81ac07",    // 这个忽略不用管
          "controlInfo": {
            "appid": "d75cbc56-9a05-4598-9f33-c124799a156e",
            "appname": "100万测试-我是应用名称",
            "controlid": "66b983a2de5c6129ee170e8b",
            "controlname": "field3",
            "groupid": "66b491cd342d5867ce607b99",
            "groupname": "100万测试-我是分组名称",
            "processlist": [
              {
                "flownodes": [
                  {
                    "_id": "66c0a45c51f551220b417482",
                    "filters": null,
                    "name": "查询工作表",
                    "type": 7
                  },
                  {
                    "_id": "66f38db694381f7ee338ec08",
                    "filters": null,
                    "name": "查询工作表-2号节点",
                    "type": 7
                  }
                ],
                "processid": "66f38dbf94381f7ee338ec2d",
                "processname": "field3等于命中-2号流程"
              },
              {
                "flownodes": [
                  {
                    "_id": "66c0a45c51f551220b417482",
                    "filters": null,
                    "name": "查询工作表",
                    "type": 7
                  }
                ],
                "processid": "66c0a48c51f551220b4174b0",
                "processname": "field3等于命中"
              }
            ],
            "worksheetid": "66b491d54d151ee8e5ddc009",
            "worksheetname": "100万测试-我是表名称"
          },
          "slowQuery": {
            "count": 52,
            "docsexamined": 1000002,
          "ns": "mdwsrows.ws66b491d54d151ee8e5ddc009",
            "op": "query",
            "opCommand": {
              "$db": "mdwsrows",
              "filter": {
                "66b983a2de5c6129ee170e8b": "命中",
                "status": 1
              },
              "find": "ws66b491d54d151ee8e5ddc009",
              "limit": 1,
              "lsid": {
                "id": {
                  "Subtype": 4,
                  "Data": "PD0kXkGAR8yv0SjqvX2LlQ=="
                }
              },
              "projection": {
                "discussunreads": 0,
                "keywords": 0,
                "owners": 0,
                "sharerange": 0,
                "status": 0,
                "unreads": 0,
                "users": 0,
                "wsutime": 0
              },
              "sort": {
                "utime": -1
              }
            },
            "planSummary": "COLLSCAN"
          },
          "timestamp": "2023-11-10T12:34:56Z"  // 查询记录的时间戳
        }
      ]
    }
    ```

---

#### 接口：立即执行慢查询分析，结果依然是通过 /api/slow-query/list 获取

- **接口名称**：
- **接口路径**：`/api/slow-query/analyze`
- **请求方法**：POST

- **请求说明**：
  - 请求需携带第一个接口成功后设置的 Cookie `atoken`。
  - 请求体需包含以下参数（暂不开放）：

- **请求体示例**：

    ```json
    {}
    ```

- **响应说明**：
  - **状态码**：200
  - **说明**：成功返回一个成功信息
  - **响应内容**：

    ```json
      {"message":"Task executed successfully."}
    ```

---

#### 接口：获取数据库慢查询记录开关是否开启

- **接口名称**：
- **接口路径**：`/api/slow-query/profiling-status`
- **请求方法**：POST

- **请求说明**：
  - 请求需携带第一个接口成功后设置的 Cookie `atoken`。

- **请求体示例**：

    ```json
    {"dbname": "mdwsrows"}
    ```

  - **响应说明**：
    - **状态码**：200
    - **说明**：成功返回一个成功信息
      - **响应内容**：

        ```json
        {
          "ok": 1,
          "sampleRate": 1,
          "slowms": 100,
          "was": 2    // 0是关闭，1和2都是开启
        }
        ```
        
---

#### 接口：创建索引接口

- **接口名称**：创建索引
- **接口路径**：`/api/index/create`
- **请求方法**：POST

- **请求说明**：
  - 请求体需包含 `dbname`（数据库名称）、`worksheetID`（工作表ID）、`controlID`（控件ID）三个参数。
  - 请求头需要携带 `Cookie` 中的 `atoken` 用于身份验证。

- **请求体示例**：
    ```json
    {
      "dbname": "mdwsrows",
      "worksheetID": "66b491d54d151ee8e5ddc009",
      "controlID": "66b983a2de5c6129ee170e8b"
    }
    ```

  - **响应说明**：
    - **状态码**：200
    - **说明**：异步接口，请求成功后加入索引创建任务队列。服务后台自动排队处理
      - **响应内容**：
        ```json
        {
          "code": 0,
          "message": "Task added to queue",
          "task": {
            "id": "1735786808423032000",
            "dbname": "mdwsrows",
            "worksheetID": "66b491d54d151ee8e5ddc009",
            "controlID": "545345",
            "status": "pending"
          },
          "queue": [
            {
            "id": "1735786808423032000",
            "dbname": "mdwsrows",
            "worksheetID": "66b491d54d151ee8e5ddc009",
            "controlID": "545345",
            "status": "processing"
            }
          ]
        }
        ```


- **错误响应**：
  - **状态码**：400
  - **说明**：请求体缺少必需参数或参数格式不正确。
  - **响应内容**：
    ```json
    {
      "error": "Invalid request body. 'dbname', 'worksheetID', and 'controlID' are required."
    }
    ```

  - **状态码**：401
  - **说明**：请求未携带有效的 `atoken`。
  - **响应内容**：
    ```json
    {
      "error": "Unauthorized"
    }
    ```

---

#### 接口：登出

- **接口路径**：`/api/logout`，**方法**：POST
- 清除 `atoken` cookie，返回 200。

---

#### 接口：会话有效性检查

- **接口路径**：`/api/session/check`，**方法**：GET（需 `atoken`）
- 校验当前会话 cookie 是否有效，前端路由守卫用；有效 200，失效 401。

---

#### 接口：全局参数

- **接口路径**：`/api/global-args`，**方法**：POST（需 `atoken`）
- 返回前端所需的全局配置参数（慢查询阈值、能力开关等），JSON 对象。

---

#### 接口：日志导出（Loki）

- **接口路径**：`/api/log/export`（及子路径形式 `/api/log/export/<文件名>`），**方法**：POST（需 `atoken`）
- 按服务/容器 + 时间范围从 Loki 拉取日志导出为文件；子路径末段是浏览器下载用的文件名。

---

### 告警子系统接口（`/api/alert/*`，1.4.0）

自研告警子系统的全部接口挂在 `/api/alert/` 下，复用 `atoken` 鉴权（外层 `AuthMiddleware` 统一校验）。**全站 kebab-case 路径 + JSON camelCase**。

#### 通用约定

- **CRUD 约定**：每类资源统一 `GET`（列表）/ `POST`（创建）/ `PUT /{id}`（更新）/ `DELETE /{id}`（删除）。
- **监控类生命周期**：监控类资源（`*-monitors`、`alert-rules`、`port-monitors`、`ssl-monitors`）额外有 `POST /{id}/refresh`（立即检查）、`POST /{id}/pause`、`POST /{id}/resume`、`GET /{id}/alert-history`、部分含 `GET /{id}/instances`、`POST|DELETE /{id}/silences`（静默）。
- **错误响应**：统一 `{ "key": "<英文机器键>", "detail": "<可选底层文案>" }`，`key` 形如 `alert_rule_not_found`/`common_invalid_param`；**只发英文 key，展示文案由前端映射**。
- **状态/级别为枚举值**：如状态 `firing`/`resolved`、级别 `warning`/`critical`，中文展示文案由前端按枚举映射，后端不返回中文。

#### 接口目录（按资源分组）

| 资源 | 路径前缀 | 说明 |
|---|---|---|
| 告警规则 | `/api/alert/alert-rules` | PromQL 告警规则；含 refresh/pause/resume/instances/silences/alert-history；`GET /api/alert/metric-templates` 取指标模板 |
| 端口连通性监控 | `/api/alert/port-monitors` | TCP 端口探测 |
| SSL 证书监控 | `/api/alert/ssl-monitors` | 证书到期探测 |
| Kafka 堆积任务 | `/api/alert/tasks` | 消费堆积监控；含 refresh/pause/resume/history/alert-history |
| Kafka 重平衡监控 | `/api/alert/kafka-rebalance-monitors` | 重平衡事件监控 |
| MongoDB 监控 | `/api/alert/mongo-monitors` | 含 instances/silences |
| MySQL 监控 | `/api/alert/mysql-monitors` | 含 instances/silences |
| 通知渠道 | `/api/alert/webhooks` | 飞书/钉钉/企微群机器人 CRUD |
| 数据源 | `/api/alert/datasources` | 统一数据源 CRUD + `POST /test`（连接测试）+ `POST /probe`（能力探测）+ `POST /test-query`（PromQL 预览）。1.5.9 起 `/test` 与 `/probe` 响应多带一个 `scrape` 块（见下） |
| 数据源聚合视图 | `/api/alert/sources` | 跨六类聚合列表 + `POST /{kind}/{id}/toggle`（启用/停用） |
| 数据源批量导入 | `POST /api/alert/sources/import` | YAML/JSON 批量导入，`?dryRun=true` 只校验不写库；按 `(kind,name)` 幂等 upsert，逐条返回校验与连接探测结果 |
| 导入模板 | `GET /api/alert/sources/import-template` | 下载带注释的 YAML 模板（六类数据源 + 自定义页面示例） |
| 数据源导出 | `GET /api/alert/sources/export` | 导出全部数据源为 YAML（可被上面的导入原样吃回去）。`?includeSecrets=true` 才带明文密码，默认密码留空——留空导回同一套平台会沿用库里原密码。1.5.12 起也包含自定义页面（`kind: customPage`，无密码字段，含 `id`/`mode`/`injectCss`） |
| 自定义页面 | `/api/alert/custom-pages` | `GET` 列表 + `POST` 新建 + `PUT/DELETE /{id}`。字段 `{name, url, mode, enabled}`，`mode` 取 `proxy`(经平台反代，见下 `/api/custom-proxy/`) 或 `iframe`(前端直接嵌)。`id`(=访问路径 slug)按名称自动生成、建后不变 |
| 各类实例（仅登记/看图） | `/api/alert/{kafka,mongo,mysql,redis,es,host,k8s-cluster}-instances` | 被监控实例 CRUD（Redis/ES 仅看图；K8s 为 static 外接集群，含 KSM/Node/Kubelet 目标 + BearerToken） |
| 全局告警历史 | `/api/alert/alert-history` | `GET` 列表 + `POST /batch-delete` + `POST /delete-all` + `GET /source-types` |

#### 示例：创建告警规则

- **路径**：`POST /api/alert/alert-rules`（需 `atoken`）

```json
{
  "name": "节点内存使用率过高",
  "datasourceId": "builtin-prometheus",
  "expr": "(1 - node_memory_MemAvailable_bytes / node_memory_MemTotal_bytes) > 0.9",
  "level": "warning",
  "intervalSec": 60,
  "webhookIds": ["<渠道id>"]
}
```

- **成功**：`200`，返回创建后的规则对象（含 `id`）。
- **失败**：如 PromQL 非法 → `400 { "key": "alert_invalid_promql", "detail": "..." }`。

#### 示例：立即检查

- **路径**：`POST /api/alert/alert-rules/{id}/refresh` → `200`，触发一次即时评估，结果落 `GET /{id}/alert-history`。

#### 自定义监控

| 接口 | 用途 |
|---|---|
| `GET /api/alert/promql/jobs` | 在内置 Prometheus 最近一小时的数据中发现采集任务 |
| `GET /api/alert/promql/metrics?job=mongodb&search=collstats` | 按采集任务和名称片段查找指标 |
| `POST /api/alert/promql/query` | 以 `{"promql":"mongodb_up","rangeSeconds":3600}` 查询。范围为 0 或省略时执行即时查询，历史查询最长 7 天 |
| `GET /api/alert/monitor-panels` | 读取平台共享面板 |
| `POST /api/alert/monitor-panels`、`PUT /api/alert/monitor-panels/{id}` | 保存面板，字段为 `name`、`promql` 和 `view`，显示方式为 `chart` 或 `table` |
| `DELETE /api/alert/monitor-panels/{id}` | 删除面板 |

查询返回 Prometheus 的 `resultType` 和 `result`。历史查询另带以秒为单位的 `start`、`end` 和 `step`。曲线最多返回 20 个序列，表格最多返回 100 个序列，超限时标记 `truncated: true`。

MongoDB 和 MySQL 数据源可设置 `metricCollection`，例如 `{"collectors":["collections","indexes"],"scope":["app.events"]}`。MongoDB 可选 `databases`、`collections`、`indexes`、`operations`，MySQL 可选 `tables`、`tableIO`、`indexIO`、`tableLocks`、`statements`、`innodb`。集合容量、索引访问及表容量需要指定范围，填写 `["*"]` 则使用 exporter 的全范围发现或采集。扩展采集依赖目标数据库版本及监控账号权限，数据源须启用且勾选「看图」。

#### 采集实况核验（`scrape`，1.5.9）

`POST /api/alert/datasources/test` 与 `POST /api/alert/datasources/probe` 的响应里都带：

```json
{ "ok": true, "connect": { "ok": true },
  "scrape": { "state": "noData", "key": "alert_scrape_no_series", "promql": "mongodb_up{job=\"mongodb\",instance=\"mongo-rs\"}" } }
```

- `state`：`ok`（Prometheus 里有该源指标）/ `noData`（一条 series 都没有）/ `down`（抓到 exporter 但其自述连不上目标，指标值为 0）/ `unknown`（查不动内置 Prometheus）/ `skipped`（该源停用、未勾「看图」用途，或该类型无采集概念）。
- `key` 是英文机器键，文案由前端 `apiError.js` 映射；`promql` 原样带回，可直接粘进「PromQL 预览」自查。
- 存在的意义：连接测试只验 TCP 可达 + 认证通过，**连得上不等于面板有数据**——中间还隔着 SD 下发、exporter 起没起、抓取有没有报错三道。判据与 `roles/agent.sh` 的 `check_metrics` 对齐（`mysql_up` / `mongodb_up` / `redis_up` / `kafka_brokers` / `elasticsearch_*_up` / `up`）。

---

### 服务发现接口（`/api/sd/*`）

供 ops-prometheus / ops-agent 内网对账，用 `ENV_OPS_SD_TOKEN` 校验（非 `atoken`）。
挂在 ops-server 直接监听的 8081 上，不经网关 nginx（48881 没有这些路由）。

| 路径 | 方法 | 说明 |
|---|---|---|
| `/api/sd/sources` | GET | 供 agent 对账的已启用数据源清单 |
| `/api/sd/prom/{job}` | GET | http_sd：按 job 返回抓取目标 |
| `/api/sd/promfile/{job}` | GET | file_sd：按 job 返回目标（含 k8s-ksm / k8s-node） |
| `/api/sd/k8s-kubelet` | GET | 各 K8s 集群 kubelet manifest（含解密后的 token） |

---

### 自定义页面动态反代（`/api/custom-proxy/`）

- **路径**：`/api/custom-proxy/<slug>/*`，需 `atoken`。
- ops-server 按 `<slug>` 查自定义页面配置，动态反代到目标 Web UI，剥离 `/api/custom-proxy/<slug>/` 前缀；
  该页面配了 `injectCss` 时在 HTML 的 `</head>` 前注入。前端菜单经 nginx `${BASE}/custom/<slug>/` → 此接口访问。
- 未配置 / 已停用 / 该条是直嵌（`mode=iframe`）时返回 503 —— 这三种都不该走反代。
- 1.5.12 前这里是 `/api/flink-proxy/`（写死的 Flink 单例，配置在 `/api/alert/flink-config`）。
  升级时旧配置会自动迁成 `slug=flink` 的一条自定义页面，`/flink` 路由重定向到 `/custom/flink`。

---

### 注意事项

- 所有接口均需认证才能访问（`/api/sd/*` 用 `ENV_OPS_SD_TOKEN`，其余用 `atoken`）。
- **文案归前端**：后端只返回枚举值 / 英文机器键，中文展示文案由前端按 key 映射多语言；后端**绝不返回中文**（第三方/底层透传文本如 Loki 日志行、mongo 报错除外）。
- 接口返回 JSON 全站 camelCase；旧 snake/无分隔路径（`slowops`/`iframurls`/`getglobalargs` 等）已废。

---

### 版本信息

- **文档版本**：1.4.0
- **更新日期**：2026-07-01