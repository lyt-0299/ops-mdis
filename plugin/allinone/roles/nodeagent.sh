#!/bin/bash

# 定义日志输出函数
log() {
    local message="$1"
    echo "$(date +"%Y-%m-%d %H:%M:%S") - ${message}"
}

# 启动 Node Exporter
start_node_exporter() {
    log "Starting Node Exporter..."
    /usr/local/agent/node_exporter/node_exporter --web.listen-address=":59100" --path.rootfs=/host > /usr/local/agent/node_exporter/logs/node_exporter.log 2>&1 &
}

# 启动所有 Exporter
start_all_exporters() {
    start_node_exporter

    # 等待所有进程
    wait
}

# 执行启动函数
start_all_exporters
