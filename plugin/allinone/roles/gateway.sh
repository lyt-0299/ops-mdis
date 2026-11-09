# 变量定义
ENV_OPS_TOKEN="${ENV_OPS_TOKEN:=admin}"
ENV_OPS_PORT="${ENV_OPS_PORT:=8081}"
ENV_OPS_GATEWAY_PORT="${ENV_OPS_GATEWAY_PORT:=48881}"
# 默认子路径 /mdis（代理层可直接反代）；env 覆盖成别的路径，或显式设空串 '' 走根路径
ENV_OPS_SUB_PATH="${ENV_OPS_SUB_PATH=/mdis}"
ENV_PROMETHEUS_SERVER="${ENV_PROMETHEUS_SERVER:=http://prometheus:9090}"
ENV_PROMETHEUS_GRAFANA="${ENV_PROMETHEUS_GRAFANA:=http://ops-grafana:3000}"
ENV_LOKI_URL="${ENV_LOKI_URL:=http://ops-loki:3100}"
ENV_TEMPO_URL="${ENV_TEMPO_URL:=http://ops-tempo:3200}"
ENV_ALLOY_URL="${ENV_ALLOY_URL:=http://ops-alloy:12345}"

OPS_BASE_PATH="$ENV_OPS_SUB_PATH"
OPS_API_PATH="$ENV_OPS_SUB_PATH/api"

# 自动读取容器 DNS resolver (兼容 Docker bridge/host/K8s 等不同部署环境)
# Docker bridge 默认是 127.0.0.11；K8s 是 cluster DNS (kube-dns)；host 模式是宿主机 DNS
NGINX_RESOLVER=$(awk '/^nameserver/ {printf "%s ", $2}' /etc/resolv.conf | sed 's/ $//')
if [ -z "$NGINX_RESOLVER" ]; then
  NGINX_RESOLVER="127.0.0.11"  # fallback
fi

export ENV_OPS_TOKEN
export OPS_PORT=$ENV_OPS_PORT
export OPS_GATEWAY_PORT=$ENV_OPS_GATEWAY_PORT
export OPS_BASE_PATH=$OPS_BASE_PATH
export OPS_API_PATH=$OPS_API_PATH
export ENV_PROMETHEUS_SERVER
export ENV_PROMETHEUS_GRAFANA
export ENV_LOKI_URL
export ENV_TEMPO_URL
export ENV_ALLOY_URL
export NGINX_RESOLVER

update-ca-certificates

mkdir -p /usr/local/nginx/logs/

envsubst '$OPS_GATEWAY_PORT,$OPS_BASE_PATH,$OPS_API_PATH,$OPS_PORT,$ENV_OPS_TOKEN,$ENV_PROMETHEUS_SERVER,$ENV_PROMETHEUS_GRAFANA,$ENV_LOKI_URL,$ENV_TEMPO_URL,$ENV_ALLOY_URL,$NGINX_RESOLVER' \
  < /usr/local/nginx/conf/conf.d/gateway.conf.tpl \
  > /usr/local/nginx/conf/conf.d/gateway.conf

# 子路径裸访问重定向：仅在 ENV_OPS_SUB_PATH 非空时注入；为空时 location 会出现语法错误
if [ -n "$ENV_OPS_SUB_PATH" ]; then
  SUBPATH_REDIRECT="location = ${ENV_OPS_SUB_PATH} { return 302 ${ENV_OPS_SUB_PATH}/web/; }\n    location = ${ENV_OPS_SUB_PATH}/ { return 302 ${ENV_OPS_SUB_PATH}/web/; }"
  sed -i "s|# {{SUBPATH_REDIRECT_BARE}}|${SUBPATH_REDIRECT}|" /usr/local/nginx/conf/conf.d/gateway.conf
else
  sed -i '/# {{SUBPATH_REDIRECT_BARE}}/d' /usr/local/nginx/conf/conf.d/gateway.conf
fi

# 向 index.html 注入 <base> 标签，使相对路径基于子路径正确解析
sed -i "s|<head>|<head><base href=\"${OPS_BASE_PATH}/web/\">|" /usr/local/ops/dist/web/index.html

/usr/local/nginx/sbin/nginx

# K8s 等无启动顺序保障的环境里 gateway 可能先于 ops-mongo 就绪，
# 而 ops-server 初始化告警子系统连不上 mongo 会直接禁用告警（不重试），
# 故启动前最多等 60s ops-mongo TCP 可达（超时仍继续，不阻塞主流程）。
if [ -n "$ENV_OPS_MONGODB_URI" ]; then
  _hostport=$(echo "$ENV_OPS_MONGODB_URI" | sed -e 's|^mongodb://||' -e 's|^.*@||' -e 's|[/?].*$||' -e 's|,.*$||')
  _host=${_hostport%%:*}
  _port=${_hostport##*:}
  [ "$_port" = "$_host" ] && _port=27017
  for _i in $(seq 1 30); do
    # 必须套 timeout：裸 /dev/tcp 无连接超时，启动竞态(如 k8s service 尚无 endpoint)会挂死整个入口
    if timeout 2 bash -c "exec 3<>/dev/tcp/${_host}/${_port}" 2>/dev/null; then
      echo "$(date +%F\ %T) - [gateway] ops-mongo ${_host}:${_port} 已就绪"
      break
    fi
    echo "$(date +%F\ %T) - [gateway] 等待 ops-mongo ${_host}:${_port} ... ($_i/30)"
    sleep 2
  done
fi

/usr/local/ops/ops-server

wait
