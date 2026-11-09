# ops-allinone: build from the repository root (linux/amd64).
# Each container selects its runtime role through ROLE.

# ---------------- 组件版本（单点管理）----------------
ARG PROM_VER=3.1.0
ARG NODE_EXPORTER_VER=1.8.2
ARG KAFKA_EXPORTER_VER=1.6.0
ARG ES_EXPORTER_VER=1.5.0
ARG REDIS_EXPORTER_VER=1.44.0
ARG MYSQL_EXPORTER_VER=0.15.1
ARG MONGODB_EXPORTER_VER=0.42.0

FROM docker.io/library/node:22-bookworm-slim AS build-frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/yarn.lock ./
RUN yarn install --frozen-lockfile --non-interactive
COPY frontend/ ./
RUN yarn build

FROM docker.io/library/golang:1.25-bookworm AS go-toolchain

FROM go-toolchain AS build-backend
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=readonly -trimpath -o /out/ops-server .

# ---------------- 官方镜像源阶段（COPY --from）----------------
FROM docker.io/grafana/grafana:12.2.1            AS src-grafana
FROM docker.io/grafana/loki:3.5.8               AS src-loki
FROM docker.io/grafana/tempo:2.9.0              AS src-tempo
FROM docker.io/grafana/alloy:v1.11.3            AS src-alloy
FROM docker.io/library/mongo:4.4.28             AS src-mongo
FROM registry.k8s.io/kube-state-metrics/kube-state-metrics:v2.10.1 AS src-ksm
FROM docker.io/prom/prometheus:v${PROM_VER} AS src-prometheus
FROM docker.io/prom/node-exporter:v${NODE_EXPORTER_VER} AS src-node-exporter
FROM docker.io/danielqsj/kafka-exporter:v${KAFKA_EXPORTER_VER} AS src-kafka-exporter
FROM docker.io/prometheuscommunity/elasticsearch-exporter:v${ES_EXPORTER_VER} AS src-es-exporter
FROM docker.io/oliver006/redis_exporter:v${REDIS_EXPORTER_VER} AS src-redis-exporter
FROM docker.io/prom/mysqld-exporter:v${MYSQL_EXPORTER_VER} AS src-mysql-exporter
FROM docker.io/percona/mongodb_exporter:${MONGODB_EXPORTER_VER} AS src-mongo-exporter

# Debian 基础镜像先借用 Go 镜像中的 CA 证书访问官方 HTTPS 软件源。
# 保留 Debian 原有的 Signed-By、软件源套件与签名校验。
FROM docker.io/library/debian:12.10 AS debian-base
COPY --from=go-toolchain /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
RUN sed -i 's|^URIs: http://deb.debian.org/|URIs: https://deb.debian.org/|' /etc/apt/sources.list.d/debian.sources

# ==================== 最终镜像 ====================
FROM debian-base
ENV LANG=en_US.UTF-8
RUN printf '%s\n' 'en_US.UTF-8 UTF-8' 'zh_CN.UTF-8 UTF-8' > /etc/locale.gen \
    && apt-get -o Acquire::Retries=3 -o APT::Update::Error-Mode=any update \
    && apt-get install -y vim iproute2 dnsutils procps locales ca-certificates gettext jq curl libcurl4 \
    && apt-get clean && rm -rf /var/lib/apt/lists/*

# ---- 从上游镜像提取二进制，保留各 ROLE 脚本使用的路径 ----
COPY --from=src-prometheus     /bin/prometheus /bin/promtool /LICENSE /NOTICE /usr/local/prometheus/server/
COPY --from=src-node-exporter /bin/node_exporter          /usr/local/agent/node_exporter/node_exporter
COPY --from=src-kafka-exporter /bin/kafka_exporter        /usr/local/agent/kafka_exporter/kafka_exporter
COPY --from=src-es-exporter    /bin/elasticsearch_exporter /usr/local/agent/elasticsearch_exporter/elasticsearch_exporter
COPY --from=src-redis-exporter /redis_exporter            /usr/local/agent/redis_exporter/redis_exporter
COPY --from=src-mysql-exporter /bin/mysqld_exporter       /usr/local/agent/mysqld_exporter/mysqld_exporter
COPY --from=src-mongo-exporter /mongodb_exporter          /usr/local/agent/mongodb_exporter/mongodb_exporter
# ---- 其余官方组件 ----
COPY --from=src-loki  /usr/bin/loki  /usr/local/bin/loki
COPY --from=src-tempo /tempo         /usr/local/bin/tempo
COPY --from=src-alloy /bin/alloy     /usr/local/bin/alloy
COPY --from=src-ksm   /kube-state-metrics /usr/local/bin/kube-state-metrics
COPY --from=src-grafana /usr/share/grafana /usr/share/grafana
COPY --from=src-grafana /etc/grafana       /etc/grafana
COPY --from=src-mongo /usr/bin/mongod                        /usr/local/bin/mongod
COPY --from=src-mongo /lib/x86_64-linux-gnu/libssl.so.1.1    /usr/lib/x86_64-linux-gnu/
COPY --from=src-mongo /lib/x86_64-linux-gnu/libcrypto.so.1.1 /usr/lib/x86_64-linux-gnu/

# ---- 各组件配置（本目录 conf/，单一来源）----
COPY plugin/allinone/conf/prometheus.yml                    /usr/local/prometheus/server/prometheus.yml
COPY plugin/allinone/conf/loki/                             /etc/loki/
COPY plugin/allinone/conf/tempo.yaml                        /etc/tempo/tempo.yaml
COPY plugin/allinone/conf/tempo-s3.yaml                     /etc/tempo/tempo-s3.yaml
COPY plugin/allinone/conf/alloy/                            /etc/alloy/
COPY plugin/allinone/conf/grafana/datasource-default.yaml   /etc/grafana/provisioning/datasources/datasource-default.yaml
COPY plugin/allinone/conf/grafana/dashboard-provisioning.yaml /etc/grafana/provisioning/dashboards/dashboard-provisioning.yaml
COPY plugin/allinone/conf/grafana/dashboardFile/            /usr/share/grafana/dashboardFile/

# ---- gateway 运行时资产（本目录 gateway/）----
COPY plugin/allinone/gateway/runtime/        /usr/local/
COPY plugin/allinone/gateway/lib/            /usr/lib/x86_64-linux-gnu/
COPY plugin/allinone/gateway/bin/            /usr/bin/
COPY plugin/allinone/gateway/gateway.conf.tpl /usr/local/nginx/conf/conf.d/
COPY plugin/allinone/gateway/css/            /usr/local/nginx/html/
COPY --from=build-frontend /src/frontend/dist/ /usr/local/ops/dist/
COPY --from=build-backend /out/ops-server       /usr/local/ops/ops-server

# ---- 全部角色脚本（本目录 roles/，10 个一次性拷入）----
COPY plugin/allinone/roles/ /roles/

RUN mkdir -p /data/logs/weblogs/ /data/mdis/prometheus /data/mdis/grafana /data/mdis/loki /data/mdis/tempo /data/mdis/mongo /data/mdis/alloy \
    && for exporter in node_exporter kafka_exporter elasticsearch_exporter redis_exporter mysqld_exporter mongodb_exporter; do \
        mkdir -p "/usr/local/agent/${exporter}/logs" || exit 1; \
    done

# ---- 统一入口：按 ROLE 分发 ----
COPY plugin/allinone/entrypoint.sh /entrypoint.sh
RUN chmod 0755 /entrypoint.sh
ENTRYPOINT ["bash", "/entrypoint.sh"]
