#!/bin/bash
#
# ops-allinone 统一镜像 —— ROLE 分发入口（原型 / proof-of-concept）
# =================================================================
# 设计：一个镜像内打包所有「单个 Go 二进制」类组件，容器启动时由 $ROLE
#       决定扮演哪个角色。容器数量不变（仍各跑各的），但**只维护、只打包
#       这一个镜像**。Grafana / MongoDB 不并入（带插件运行时 / 数据库，
#       继续用各自官方镜像，见 README）。
#
# 用法（docker-compose）：
#   services:
#     ops-prometheus:
#       image: registry.cn-hangzhou.aliyuncs.com/mdpublic/ops-allinone:1.5.12
#       environment: { ROLE: prometheus, ... }   # 其余 env 与原 ops-prometheus 完全一致
#     ops-agent:
#       image: registry.cn-hangzhou.aliyuncs.com/mdpublic/ops-allinone:1.5.12
#       environment: { ROLE: agent, ... }
#     ...
#
# 关键点：各角色的真实启动逻辑**原样复用**现有各组件的 entrypoint
#        （构建时拷到 /roles/<role>.sh），本文件只做「按 ROLE 选脚本」这一件事，
#        所以行为与拆分镜像时逐字节一致，迁移风险最低。

set -euo pipefail

ROLE="${ROLE:-}"

# 支持的角色 → 对应的角色脚本（= 原组件 entrypoint）
# 方案 C：全部并入一个镜像，含 grafana / mongo。
declare -A ROLE_SCRIPT=(
  [gateway]="/roles/gateway.sh"
  [prometheus]="/roles/prometheus.sh"
  [agent]="/roles/agent.sh"
  [nodeagent]="/roles/nodeagent.sh"
  [alloy]="/roles/alloy.sh"
  [loki]="/roles/loki.sh"
  [tempo]="/roles/tempo.sh"
  [ksm]="/roles/ksm.sh"
  [grafana]="/roles/grafana.sh"
  [mongo]="/roles/mongo.sh"
)

usage() {
  echo "[ops-allinone] 必须通过环境变量 ROLE 指定角色。"
  echo "  支持的 ROLE: ${!ROLE_SCRIPT[*]}"
  echo "  例: docker run -e ROLE=prometheus ops-allinone"
  exit 64
}

[ -z "$ROLE" ] && { echo "[ops-allinone] 错误：未设置 ROLE"; usage; }

script="${ROLE_SCRIPT[$ROLE]:-}"
[ -z "$script" ] && { echo "[ops-allinone] 错误：未知 ROLE=$ROLE"; usage; }
[ ! -f "$script" ] && { echo "[ops-allinone] 错误：角色脚本缺失 $script（镜像未打包该角色？）"; exit 70; }

echo "$(date +%F\ %T) - [ops-allinone] 启动角色 ROLE=$ROLE → $script"
exec bash "$script"
