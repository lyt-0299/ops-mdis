#!/bin/sh
set -eu

export ENV_TEMPO_GRPC_URL="${ENV_TEMPO_GRPC_URL:-http://ops-tempo:4317}"
export ENV_PROMETHEUS_REMOTE_WRITE_URL="${ENV_PROMETHEUS_REMOTE_WRITE_URL:-http://ops-prometheus:9090/api/v1/write}"
export ENV_LOKI_PUSH_URL="${ENV_LOKI_PUSH_URL:-http://ops-loki:3100/loki/api/v1/push}"

# Docker 容器日志采集只在有 docker.sock 时装载。
# K8s(containerd) 下没有这个 socket，无条件加载会让 discovery.docker 每 10 秒刷一次
# 连接失败，属于纯噪音——曾有人据此误判成故障、手工把宿主机 docker.sock 挂进容器，
# 结果把宿主机容器日志灌进了 Loki。这里按环境自动决定，不需要用户配任何东西。
ALLOY_RUN_DIR=/etc/alloy/run
rm -rf "$ALLOY_RUN_DIR"
mkdir -p "$ALLOY_RUN_DIR"
cp /etc/alloy/base.alloy "$ALLOY_RUN_DIR/"

if [ -S /var/run/docker.sock ]; then
    cp /etc/alloy/docker-logs.alloy "$ALLOY_RUN_DIR/"
    echo "[alloy] 已启用 Docker 容器日志采集（/var/run/docker.sock）"
else
    # 这条必须打：否则单机部署漏挂 docker.sock 时会静默不采日志，比刷报错更难查。
    echo "[alloy] 未检测到 /var/run/docker.sock，跳过 Docker 容器日志采集。"
    echo "[alloy] K8s 部署下这是正常的：容器日志由 alloy-logs DaemonSet 经 kube API 采集，"
    echo "[alloy] 本进程只负责接收 OTLP 链路数据。若这是单机 Compose 部署，请检查 ops.yaml"
    echo "[alloy] 里 ops-alloy 是否挂载了 /var/run/docker.sock:/var/run/docker.sock。"
fi

exec /usr/local/bin/alloy run "$ALLOY_RUN_DIR" \
    --server.http.listen-addr=0.0.0.0:12345 \
    --stability.level=generally-available \
    --storage.path=/data/mdis/alloy
