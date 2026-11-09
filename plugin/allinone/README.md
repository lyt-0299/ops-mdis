# ops-allinone —— ops 全栈单镜像（1.4.0 终态）

ops 监控栈全部组件打进**一个镜像**，运行时由 `ROLE` 环境变量分发成不同角色。
容器数量不变（仍各跑各的），但**只维护/打包/离线导出一个镜像**。

## 目录结构（自包含，单一来源）
```
plugin/allinone/
├── build.sh            发布脚本（使用根目录 Dockerfile；PUSH=0 只 build）
├── ops.yaml.example    部署示例：9 service 共用本镜像 + 各自 ROLE
├── entrypoint.sh       ROLE 分发入口
├── roles/              10 个角色启动脚本（gateway/prometheus/agent/nodeagent/
│                       grafana/loki/tempo/alloy/ksm/mongo）
├── conf/               各组件配置（prometheus.yml / loki/ / tempo.yaml /
│                       alloy/{base,docker-logs}.alloy / grafana provisioning+dashboards）
├── gateway/            gateway 运行时资产（nginx runtime + lib + bin + conf.tpl + css）
└── k8s/                k8s 部署清单（⚠️ 见下「k8s 模式待办」）
```

## 组件来源（升级=改一处）
- **下载类**（prometheus / kafka,es,redis,mysqld,mongodb exporter / node_exporter）：Dockerfile 内联 wget，版本由顶部 `ARG` 管。
- **上游镜像类**（grafana / loki / tempo / alloy / mongo / ksm）：`COPY --from` 官方镜像，版本由对应 `FROM` tag 管。
- **gateway**：前端(dist)+后端(ops-server) 由根目录 Dockerfile 在构建阶段编译；nginx 等运行时资产在 `gateway/`。
- **mongo**：mongod(4.4) 依赖 libssl1.1/libcrypto1.1，从官方 mongo 镜像抠入（debian12 无），已验证可起。

## 构建 / 部署
```bash
# 构建（发版）
cd plugin/allinone && ./build.sh                 # → 一个镜像 ops-allinone:1.5.12
# 在线部署
docker-compose -f ops.yaml up -d                 # 1 镜像，9 容器靠 ROLE 区分
# 离线部署
docker save .../ops-allinone:1.5.12 -o ops.tar    # 一个 tar
docker load -i ops.tar && docker-compose -f ops.yaml up -d
```

## 验证状态（local_50）
- 自包含单镜像 build ✅ 2.16GB；各角色 ROLE 启动 ✅
- 整栈 compose 拉起回归 ✅：MDIS UI / 慢查询 / prometheus 抓取 / grafana 出图 / 告警 API 全通，0 console error，gateway nginx 完整 env 下渲染正常
- Mongo 验证关 ✅

## k8s 模式（清单已切镜像，待环境验证）
`k8s/` 清单（`10-kube-state-metrics` / `20-node-exporter` / `30-prometheus-incluster` / `40-alloy-logs`）**已全部切到本单镜像 `ops-allinone:1.5.12` + `ROLE` 分发**。
- ksm / node-exporter / prometheus-incluster 已映射成本镜像 + 对应 `ROLE`；
- `40-alloy-logs.yaml` 用**专门的 k8s 日志采集配置**（与单机的 `conf/alloy/docker-logs.alloy` 不同），走独立 role/config。
- ⚠️ 仍需在真实 k8s 环境跑通验证后才算落地（单机 docker 形态已验证）。

## 与多镜像旧形态的关系
本目录已**取代** plugin/ 下原 9 个组件目录（gateway/prometheus/agent/...）。源码 server//frontend/ 不变；旧多镜像形态已退役。
