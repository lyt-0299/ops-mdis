#!/bin/bash
# ksm 角色：kube-state-metrics（仅 k8s 部署用；docker-compose 一般不起此角色）。
# 原 ops-ksm 为官方 distroless 镜像，默认入口即 /kube-state-metrics --port=8080 --telemetry-port=8081。
set -e
echo "$(date +%F\ %T) - [ksm] 启动 kube-state-metrics :8080/:8081"
exec /usr/local/bin/kube-state-metrics --port=8080 --telemetry-port=8081
