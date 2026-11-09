#!/bin/sh
set -eu

# Auto-detect container IP for memberlist advertise address
ADVERTISE_ADDR="${ENV_TEMPO_ADVERTISE_ADDR:-$(hostname -I 2>/dev/null | awk '{print $1}')}"

if [ -n "${ENV_S3_ENDPOINT:-}" ]; then
    # ⚠️ Tempo 用 minio-go，endpoint 必须是 host:port，带 scheme 会报
    #    "Endpoint url cannot have fully qualified paths" 直接起不来。
    #    而 Loki 用 AWS SDK、必须带 scheme —— 同一个 ENV_S3_ENDPOINT 两边要求相反，
    #    所以在这里剥掉，让用户只按文档写一种格式（带 http(s):// ）。
    #    是否走 TLS 由 tempo-s3.yaml 的 insecure 控制，不靠 scheme 判断。
    _ep="${ENV_S3_ENDPOINT}"
    _ep="${_ep#http://}"
    _ep="${_ep#https://}"
    export ENV_S3_ENDPOINT="${_ep%%/*}"
    # 桶名用 Tempo 专属变量，回退到通用 ENV_S3_BUCKET，再回退默认值。
    # ⚠️ 别只用 ENV_S3_BUCKET：它常在 ConfigMap 里全局设一次并注入所有角色，
    #    会把 Tempo 和 Loki 指到同一个桶（Loki 先占了目录结构，Tempo 随后起不来）。
    #    1.5.1 起部署清单一律配 ENV_S3_BUCKET_TEMPO / ENV_S3_BUCKET_LOKI。
    _raw_bucket="${ENV_S3_BUCKET:-}"
    export ENV_S3_BUCKET="${ENV_S3_BUCKET_TEMPO:-${ENV_S3_BUCKET:-tempo}}"
    # 共桶自查：两个专属变量都没设、却设了通用 ENV_S3_BUCKET 时，Loki 与 Tempo 会落到
    # 同一个桶。这是个静默状态——不报错、也未必立刻崩，但两边的目录结构混在一起，
    # 出问题时极难定位。明确打一条日志，让人知道自己正处在这个状态里。
    if [ -z "${ENV_S3_BUCKET_LOKI:-}" ] && [ -z "${ENV_S3_BUCKET_TEMPO:-}" ] && [ -n "${_raw_bucket:-}" ]; then
        echo "$(date +%F\ %T) - [s3] ⚠ Loki 与 Tempo 正共用同一个 bucket \"${_raw_bucket}\"（来自 ENV_S3_BUCKET）。"
        echo "$(date +%F\ %T) - [s3]   建议拆成 ENV_S3_BUCKET_LOKI / ENV_S3_BUCKET_TEMPO 两个不同的桶，两个桶都需预先创建。"
    fi
    export ENV_S3_ACCESS_KEY="${ENV_S3_ACCESS_KEY:-}"
    export ENV_S3_SECRET_KEY="${ENV_S3_SECRET_KEY:-}"
    # 复用 loki 那套单一开关 ENV_S3_FORCE_PATH_STYLE 派生 minio-go 的寻址方式：
    #   true(US3/MinIO) → path(2)；false(COS/OSS 等只认 virtual-host) → DNS(1)
    export ENV_S3_FORCE_PATH_STYLE="${ENV_S3_FORCE_PATH_STYLE:-true}"
    if [ "$ENV_S3_FORCE_PATH_STYLE" = "true" ]; then
        export ENV_S3_BUCKET_LOOKUP=2
    else
        export ENV_S3_BUCKET_LOOKUP=1
    fi
    CONFIG=/etc/tempo/tempo-s3.yaml
else
    CONFIG=/etc/tempo/tempo.yaml
fi

sed -i "s/__ADVERTISE_ADDR__/${ADVERTISE_ADDR}/" "$CONFIG"

exec /usr/local/bin/tempo -config.file="$CONFIG" -config.expand-env=true
