#!/bin/sh
set -eu

# dashboard JSON 存放在镜像内 /usr/share/grafana/dashboardFile/，启动时替换 datasource 占位
find /usr/share/grafana/dashboardFile -name "*.json" 2>/dev/null | while read f; do
    sed -i 's/"datasource": null/"datasource": "DS_PROMETHEUS"/g' "$f"
done

# 子路径配置：gateway 反代 grafana 时不剥前缀，grafana 必须始终从
# ${ENV_OPS_SUB_PATH}/prometheus/grafana/ 服务（子路径为空时即 /prometheus/grafana/，
# 之前仅在子路径非空时设置，导致直接端口访问模式下 grafana 全部 404）
export GF_SERVER_ROOT_URL="${GF_SERVER_ROOT_URL:-%(protocol)s://%(domain)s:%(http_port)s${ENV_OPS_SUB_PATH-/mdis}/prometheus/grafana/}"
export GF_SERVER_SERVE_FROM_SUB_PATH="${GF_SERVER_SERVE_FROM_SUB_PATH:-true}"
export GF_LIVE_ALLOWED_ORIGINS="${GF_LIVE_ALLOWED_ORIGINS:-*}"

# Grafana 的默认界面语言，适用于匿名嵌入；部署可通过同名变量覆盖。
export GF_USERS_DEFAULT_LANGUAGE="${GF_USERS_DEFAULT_LANGUAGE:-zh-Hans}"

export GF_SMTP_ENABLED="${ENV_SMTP_ENABLED:-false}"
export GF_SMTP_HOST="${ENV_SMTP_HOST:-localhost}:${ENV_SMTP_PORT:-25}"
export GF_SMTP_USER="${ENV_SMTP_USER:-}"
export GF_SMTP_PASSWORD="${ENV_SMTP_PASSWORD:-}"
export GF_SMTP_SKIP_VERIFY="${ENV_SMTP_SKIPVERIFY:-false}"
export GF_SMTP_FROM_ADDRESS="${ENV_SMTP_FROM_ADDR:-admin@grafana.localhost}"
export GF_SMTP_FROM_NAME="${ENV_SMTP_FROM_NAME:-Grafana}"

exec /usr/share/grafana/bin/grafana server     --homepath=/usr/share/grafana     --config=/etc/grafana/grafana.ini     cfg:default.paths.data=/data/mdis/grafana     cfg:default.paths.logs=/var/log/grafana     cfg:default.paths.plugins=/data/mdis/grafana/plugins     cfg:default.paths.provisioning=/etc/grafana/provisioning
