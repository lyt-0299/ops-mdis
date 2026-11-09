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

FROM docker.io/library/golang:1.25-bookworm AS build-backend
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

# ---------------- 下载阶段：prometheus / 各 exporter / node_exporter ----------------
FROM docker.io/library/debian:12.10 AS src-download
ARG PROM_VER NODE_EXPORTER_VER KAFKA_EXPORTER_VER ES_EXPORTER_VER REDIS_EXPORTER_VER MYSQL_EXPORTER_VER MONGODB_EXPORTER_VER
RUN sed -i 's/deb.debian.org/mirrors.tuna.tsinghua.edu.cn/' /etc/apt/sources.list.d/debian.sources \
    && apt-get update && apt-get install -y wget ca-certificates && rm -rf /var/lib/apt/lists/*
RUN mkdir -p /out/prometheus/server \
    && wget -q https://github.com/prometheus/prometheus/releases/download/v${PROM_VER}/prometheus-${PROM_VER}.linux-amd64.tar.gz \
    && tar -zxf prometheus-${PROM_VER}.linux-amd64.tar.gz --strip-components=1 -C /out/prometheus/server \
    && rm prometheus-${PROM_VER}.linux-amd64.tar.gz
RUN mkdir -p /out/agent/node_exporter/logs \
    && wget -q https://github.com/prometheus/node_exporter/releases/download/v${NODE_EXPORTER_VER}/node_exporter-${NODE_EXPORTER_VER}.linux-amd64.tar.gz \
    && tar -xzf node_exporter-${NODE_EXPORTER_VER}.linux-amd64.tar.gz \
    && mv node_exporter-${NODE_EXPORTER_VER}.linux-amd64/node_exporter /out/agent/node_exporter/ \
    && rm -rf node_exporter-${NODE_EXPORTER_VER}.linux-amd64*
RUN mkdir -p /out/agent/kafka_exporter/logs /out/agent/elasticsearch_exporter/logs \
             /out/agent/redis_exporter/logs /out/agent/mysqld_exporter/logs /out/agent/mongodb_exporter/logs \
    && wget -q https://github.com/danielqsj/kafka_exporter/releases/download/v${KAFKA_EXPORTER_VER}/kafka_exporter-${KAFKA_EXPORTER_VER}.linux-amd64.tar.gz \
    && tar -xzf kafka_exporter-${KAFKA_EXPORTER_VER}.linux-amd64.tar.gz \
    && mv kafka_exporter-${KAFKA_EXPORTER_VER}.linux-amd64/kafka_exporter /out/agent/kafka_exporter/ \
    && wget -q https://github.com/prometheus-community/elasticsearch_exporter/releases/download/v${ES_EXPORTER_VER}/elasticsearch_exporter-${ES_EXPORTER_VER}.linux-amd64.tar.gz \
    && tar -xzf elasticsearch_exporter-${ES_EXPORTER_VER}.linux-amd64.tar.gz \
    && mv elasticsearch_exporter-${ES_EXPORTER_VER}.linux-amd64/elasticsearch_exporter /out/agent/elasticsearch_exporter/ \
    && wget -q https://github.com/oliver006/redis_exporter/releases/download/v${REDIS_EXPORTER_VER}/redis_exporter-v${REDIS_EXPORTER_VER}.linux-amd64.tar.gz \
    && tar -xzf redis_exporter-v${REDIS_EXPORTER_VER}.linux-amd64.tar.gz \
    && mv redis_exporter-v${REDIS_EXPORTER_VER}.linux-amd64/redis_exporter /out/agent/redis_exporter/ \
    && wget -q https://github.com/prometheus/mysqld_exporter/releases/download/v${MYSQL_EXPORTER_VER}/mysqld_exporter-${MYSQL_EXPORTER_VER}.linux-amd64.tar.gz \
    && tar -xzf mysqld_exporter-${MYSQL_EXPORTER_VER}.linux-amd64.tar.gz \
    && mv mysqld_exporter-${MYSQL_EXPORTER_VER}.linux-amd64/mysqld_exporter /out/agent/mysqld_exporter/ \
    && wget -q https://github.com/percona/mongodb_exporter/releases/download/v${MONGODB_EXPORTER_VER}/mongodb_exporter-${MONGODB_EXPORTER_VER}.linux-amd64.tar.gz \
    && tar -xzf mongodb_exporter-${MONGODB_EXPORTER_VER}.linux-amd64.tar.gz \
    && mv mongodb_exporter-${MONGODB_EXPORTER_VER}.linux-amd64/mongodb_exporter /out/agent/mongodb_exporter/ \
    && rm -rf *_exporter-*

# ==================== 最终镜像 ====================
FROM docker.io/library/debian:12.10
ENV LANG=en_US.UTF-8
RUN echo "en_US.UTF-8 UTF-8\nzh_CN.UTF-8 UTF-8" > /etc/locale.gen \
    && sed -i 's/deb.debian.org/mirrors.tuna.tsinghua.edu.cn/' /etc/apt/sources.list.d/debian.sources \
    && apt-get update \
    && apt-get install -y vim iproute2 dnsutils procps locales ca-certificates gettext jq curl libcurl4 \
    && apt-get clean && rm -rf /var/lib/apt/lists/*

# ---- 下载类二进制 ----
COPY --from=src-download /out/prometheus/ /usr/local/prometheus/
COPY --from=src-download /out/agent/      /usr/local/agent/
# ---- 官方二进制 ----
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

RUN mkdir -p /data/logs/weblogs/ /data/mdis/prometheus /data/mdis/grafana /data/mdis/loki /data/mdis/tempo /data/mdis/mongo /data/mdis/alloy

# ---- 统一入口：按 ROLE 分发 ----
COPY plugin/allinone/entrypoint.sh /entrypoint.sh
RUN chmod 0755 /entrypoint.sh
ENTRYPOINT ["bash", "/entrypoint.sh"]
