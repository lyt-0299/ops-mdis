#!/bin/sh
set -eu

export ENV_LOKI_RETENTION="${ENV_LOKI_RETENTION:-720h}"

if [ -n "${ENV_S3_ENDPOINT:-}" ]; then
    export ENV_S3_ENDPOINT
    # 桶名用 Loki 专属变量，回退到通用 ENV_S3_BUCKET，再回退默认值。
    # 见 roles/tempo.sh 同处注释：只用 ENV_S3_BUCKET 会导致两个组件共桶。
    _raw_bucket="${ENV_S3_BUCKET:-}"
    export ENV_S3_BUCKET="${ENV_S3_BUCKET_LOKI:-${ENV_S3_BUCKET:-loki}}"
    # 共桶自查：两个专属变量都没设、却设了通用 ENV_S3_BUCKET 时，Loki 与 Tempo 会落到
    # 同一个桶。这是个静默状态——不报错、也未必立刻崩，但两边的目录结构混在一起，
    # 出问题时极难定位。明确打一条日志，让人知道自己正处在这个状态里。
    if [ -z "${ENV_S3_BUCKET_LOKI:-}" ] && [ -z "${ENV_S3_BUCKET_TEMPO:-}" ] && [ -n "${_raw_bucket:-}" ]; then
        echo "$(date +%F\ %T) - [s3] ⚠ Loki 与 Tempo 正共用同一个 bucket \"${_raw_bucket}\"（来自 ENV_S3_BUCKET）。"
        echo "$(date +%F\ %T) - [s3]   建议拆成 ENV_S3_BUCKET_LOKI / ENV_S3_BUCKET_TEMPO 两个不同的桶，两个桶都需预先创建。"
    fi
    export ENV_S3_ACCESS_KEY="${ENV_S3_ACCESS_KEY:-}"
    export ENV_S3_SECRET_KEY="${ENV_S3_SECRET_KEY:-}"
    # path-style: US3/MinIO 需 true(默认);腾讯云 COS 只支持 virtual-hosted，须传 false
    export ENV_S3_FORCE_PATH_STYLE="${ENV_S3_FORCE_PATH_STYLE:-true}"
    CONFIG=/etc/loki/loki-config-s3.yaml
else
    CONFIG=/etc/loki/loki-config-fs.yaml
fi

exec /usr/local/bin/loki -config.file="$CONFIG" -config.expand-env=true
