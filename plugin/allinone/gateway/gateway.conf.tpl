# 资源页继续使用嵌入样式。自定义看板保留 Grafana 的布局和编辑控件。
map $uri $mdis_grafana_style {
    default '<link rel="stylesheet" type="text/css" href="${OPS_BASE_PATH}/prometheus/grafana/custem.css">';
    ~^${OPS_BASE_PATH}/prometheus/grafana/d/mdis-custom-monitoring(?:/|$) "";
}

server {
    listen ${OPS_GATEWAY_PORT};

    access_log /data/logs/weblogs/gateway.log main;
    error_log /data/logs/weblogs/gateway.error.log;

    default_type text/plain;
    absolute_redirect off;

    # 动态 DNS 解析 (容器重建后 IP 变化, 通过 set $var + proxy_pass 触发每次重新解析)
    # resolver IP 由 entrypoint 从 /etc/resolv.conf 注入, 兼容 Docker bridge / K8s / host 模式
    resolver ${NGINX_RESOLVER} valid=10s ipv6=off;
    resolver_timeout 5s;

    # OPS-Gateway
    location = / {
        return 302 ${OPS_BASE_PATH}/web/;
    }

    # {{SUBPATH_REDIRECT_BARE}}

    location = ${OPS_BASE_PATH}/web {
        return 302 ${OPS_BASE_PATH}/web/;
    }

    location ${OPS_BASE_PATH}/web/ {
        alias /usr/local/ops/dist/web/;
        try_files $uri $uri/ ${OPS_BASE_PATH}/web/index.html;

        # index.html 必须 no-cache，否则用户升级后浏览器仍拿老 entry，引用已删 hashed JS 导致 SPA 无法启动
        location ~ \.html$ {
            add_header Cache-Control "no-cache, no-store, must-revalidate" always;
            expires -1;
        }

        # locale 翻译文件无内容 hash，不能 immutable（否则改了翻译，已访问用户一年内拿不到新译文）。
        # 必须放在下面 .js 规则之前：nginx 正则 location 按出现顺序首个匹配生效。
        location ~ ^${OPS_BASE_PATH}/web/locale/.+\.js$ {
            add_header Cache-Control "no-cache, must-revalidate" always;
        }

        # 带 hash 的 JS/CSS 长 cache（hash 变了浏览器自然拉新文件）
        location ~ \.(js|css)$ {
            add_header Cache-Control "public, max-age=31536000, immutable" always;
        }
    }

    location ${OPS_API_PATH}/ {
        if ($request_method = OPTIONS) {
            return 204;
        }
        # ops-server 在同容器内 (127.0.0.1) 不会变 IP, 不需要动态解析
        proxy_pass http://127.0.0.1:${OPS_PORT}/api/;
        # 必须带引号：子路径为空时裸值会渲染成无参数指令，nginx 启动失败
        proxy_set_header X-Forwarded-Path "${OPS_BASE_PATH}";
    }

    # 验证会话的内部位置：交给 ops-server 校验签名会话 Cookie（atoken 已非裸 token，
    # 是 HMAC 签名会话 nonce.expires.sig，nginx 无法直接比对，统一委托后端）。
    location = /_auth {
        internal;
        proxy_pass http://127.0.0.1:${OPS_PORT}/api/session/check;
        proxy_pass_request_body off;
        proxy_set_header Content-Length "";
    }

    # 跨集群指标写入入口（多集群纳管用）
    # 别的 K8s 集群里的采集组件（k8s-addons）靠这个 URL 把指标 remote_write 回来。
    # 为什么不复用下面那个 location：它走 auth_request /_auth 校验的是浏览器签名会话
    # Cookie，而 Prometheus 的 remote_write 只能带固定 header，永远过不了那道校验；
    # 而 ops-prometheus 自身是 ClusterIP，集群外根本连不上。没有这个 location，
    # 多集群方案就落不了地。
    # 鉴权用 Bearer + ENV_OPS_TOKEN（与平台其它入口同一个 token），采集端配：
    #   remote_write:
    #     - url: http://<平台地址>:<NodePort>${OPS_BASE_PATH}/prometheus/server/api/v1/write
    #       authorization: { type: Bearer, credentials: "<ENV_OPS_TOKEN>" }
    # nginx 按最长前缀匹配，这个比下面的 /prometheus/server/ 更长，优先命中。
    location ${OPS_BASE_PATH}/prometheus/server/api/v1/write {
        if ($http_authorization != "Bearer ${ENV_OPS_TOKEN}") { return 401; }
        # remote_write 一批可达数 MB（snappy 压缩的 protobuf），默认 1m 会截成 413
        client_max_body_size 64m;
        set $upstream_prometheus "${ENV_PROMETHEUS_SERVER}";
        rewrite ^${OPS_BASE_PATH}(/prometheus/server/.*)$ $1 break;
        proxy_pass $upstream_prometheus;
    }

    # OPS-Prometheus
    location ${OPS_BASE_PATH}/prometheus/server/ {
        auth_request /_auth;
        # set 必须在 rewrite...break 之前：break 会中止 rewrite 模块后续指令，set 被跳过则 proxy_pass 为空
        set $upstream_prometheus "${ENV_PROMETHEUS_SERVER}";
        rewrite ^${OPS_BASE_PATH}(/prometheus/server/.*)$ $1 break;
        proxy_pass $upstream_prometheus;
    }

    # Grafana Live WebSocket（auth_request 不兼容 WebSocket）
    location ${OPS_BASE_PATH}/prometheus/grafana/api/live/ {
        proxy_http_version 1.1;
        proxy_set_header Host $proxy_host;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        set $upstream_grafana_live "${ENV_PROMETHEUS_GRAFANA}";
        proxy_pass $upstream_grafana_live;
    }

    location ${OPS_BASE_PATH}/prometheus/grafana/ {
        auth_request /_auth;
        proxy_http_version 1.1;
        # 保留浏览器访问的主机名，Grafana 用它校验原生编辑请求的 Origin。
        proxy_set_header Host $host;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        set $upstream_grafana "${ENV_PROMETHEUS_GRAFANA}";
        proxy_pass $upstream_grafana;
        sub_filter '</head>' '$mdis_grafana_style</head>';
        sub_filter_once on;
    }

    location ${OPS_BASE_PATH}/prometheus/grafana/api/user {
        return 200 "auth.anonymous true";
    }

    location ${OPS_BASE_PATH}/prometheus/grafana/api/plugins/grafana-oncall-app/settings {
        return 200 "disable.plugin true";
    }

    location = ${OPS_BASE_PATH}/prometheus/grafana/custem.css {
        alias /usr/local/nginx/html/grafana/custem.css;
    }

    # 跨集群日志写入入口（与上面 prometheus 那条同理）
    # 别的 K8s 集群里的 alloy-logs 往这里推容器日志。ops-loki 是 ClusterIP、集群外连不上，
    # 而下面那条 /loki/ 走会话 Cookie 鉴权，日志采集器同样带不了。
    # 采集端配 ENV_LOKI_PUSH_URL=http://<平台地址>:<NodePort>${OPS_BASE_PATH}/loki/api/v1/push
    # 与 ENV_LOKI_PUSH_TOKEN=<ENV_OPS_TOKEN>。
    location ${OPS_BASE_PATH}/loki/api/v1/push {
        if ($http_authorization != "Bearer ${ENV_OPS_TOKEN}") { return 401; }
        # 日志批次可观，默认 1m 会截成 413
        client_max_body_size 64m;
        set $upstream_loki "${ENV_LOKI_URL}";
        rewrite ^${OPS_BASE_PATH}/loki/(.*)$ /$1 break;
        proxy_pass $upstream_loki;
    }

    # OPS-Loki（查询 API；push 见上面那条独立入口）
    location ${OPS_BASE_PATH}/loki/ {
        auth_request /_auth;
        set $upstream_loki "${ENV_LOKI_URL}";
        rewrite ^${OPS_BASE_PATH}/loki/(.*)$ /$1 break;
        proxy_pass $upstream_loki;
    }

    # OPS-Tempo（APM 链路追踪）
    location ${OPS_BASE_PATH}/tempo/ {
        auth_request /_auth;
        set $upstream_tempo "${ENV_TEMPO_URL}";
        rewrite ^${OPS_BASE_PATH}/tempo/(.*)$ /$1 break;
        proxy_pass $upstream_tempo;
    }

    # OPS-Alloy UI（OTLP 采集器管理）
    location ${OPS_BASE_PATH}/alloy/ {
        auth_request /_auth;
        proxy_http_version 1.1;
        set $upstream_alloy "${ENV_ALLOY_URL}";
        proxy_pass $upstream_alloy;
    }

    # 自定义页面(反代模式)：把任意 Web UI 挂进平台菜单。目标由 ops-server 按 slug 动态决定
    # (地址存 UI 配置，运行时可改)，nginx 仅把 ${BASE}/custom/<slug>/<path> 重写成
    # /api/custom-proxy/<slug>/<path> 打到同容器 ops-server，鉴权在这里做。
    # 1.5.12 前这里是写死的 ${BASE}/flink/ 一条 —— 每加一个页面就要改一次网关配置。
    # CSS 注入也移到了 ops-server(它知道这个 slug 该注入什么)，故不再有 sub_filter。
    location ${OPS_BASE_PATH}/custom/ {
        auth_request /_auth;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        rewrite ^${OPS_BASE_PATH}/custom/(.*)$ /api/custom-proxy/$1 break;
        proxy_pass http://127.0.0.1:${OPS_PORT};
    }
}
