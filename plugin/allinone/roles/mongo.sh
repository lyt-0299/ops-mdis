#!/bin/bash
# mongo 角色：告警子系统自有 MongoDB（与原 ops-mongo 容器行为一致）。
# 原 ops-mongo: image mongo:4.4.28, command ["--bind_ip_all"], 数据卷挂 /data/mdis/mongo。
set -e
mkdir -p /data/mdis/mongo
echo "$(date +%F\ %T) - [mongo] 启动 mongod --bind_ip_all --dbpath /data/mdis/mongo"
exec /usr/local/bin/mongod --bind_ip_all --dbpath /data/mdis/mongo
