#!/bin/bash
# 把 ops-allinone 的镜像 tag 从 build.sh 同步到所有部署清单与文档
# =============================================================
# 为什么需要这个：build.sh 的 IMAGE 是版本真相源，但 k8s 清单(k8s/ 与 k8s-cluster/)、
# plugin/README.md、DEPLOY.md 等十几处都各自写死了 tag，发版时靠人肉逐个改 —— 必漂。
# 2026-08-04 实测已漂成三档：build.sh=1.5.10、k8s/=1.5.9、k8s-cluster/=1.5.7，
# 也就是照 k8s-cluster/ 装的客户拿到的是 1.5.7，而 1.5.10 修的正是 K8s 面板整片
# No data —— 最需要这个修复的人反而拿不到。
#
# 用法：
#   ./sync-version.sh            把所有文件同步到 build.sh 里的 tag
#   ./sync-version.sh --check    只检查不改；有漂移则非零退出（发版前/CI 可用）
#   ./sync-version.sh 1.6.0      连 build.sh 一起改到指定版本（发版时用这个）
set -euo pipefail

SELF_DIR="$(cd "$(dirname "$0")" && pwd)"           # plugin/allinone
REPO_DIR="$(cd "$SELF_DIR/../.." && pwd)"           # 仓库根
BUILD_SH="$SELF_DIR/build.sh"

CHECK_ONLY=0
NEW_TAG=""
case "${1:-}" in
  --check) CHECK_ONLY=1 ;;
  "")      ;;
  *)       NEW_TAG="$1" ;;
esac

read_build_tag() {
  sed -nE 's|.*ops-allinone:([0-9]+\.[0-9]+\.[0-9]+).*|\1|p' "$BUILD_SH" | head -1
}

# 指定了新版本 → 先改 build.sh 自己，再以它为准同步其余文件
if [ -n "$NEW_TAG" ]; then
  if ! printf '%s' "$NEW_TAG" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$'; then
    echo "版本号格式应为 X.Y.Z，收到：$NEW_TAG" >&2; exit 2
  fi
  sed -i.bak -E "s|(ops-allinone:)[0-9]+\.[0-9]+\.[0-9]+|\1${NEW_TAG}|g" "$BUILD_SH"
  rm -f "$BUILD_SH.bak"
  echo "build.sh → $NEW_TAG"
fi

TAG="$(read_build_tag)"
[ -n "$TAG" ] || { echo "从 $BUILD_SH 读不出镜像 tag" >&2; exit 2; }

# 收集所有写了 ops-allinone:<版本> 的文件。
# 排除：build.sh 自己(真相源)、node_modules、.playwright-mcp/(浏览器抓的临时快照)、
# docs/superpowers/(历史设计快照，那里的 1.4.0 是当时的事实记录，改了反而失真)。
FILES=""
while IFS= read -r f; do
  case "$f" in
    */node_modules/*|*/.playwright-mcp/*|*/docs/superpowers/*|"$BUILD_SH") continue ;;
  esac
  FILES="$FILES$f"$'\n'
done < <(grep -rlE "ops-allinone:[0-9]+\.[0-9]+\.[0-9]+" "$REPO_DIR" \
           --include='*.yaml' --include='*.yml' --include='*.md' --include='*.sh' 2>/dev/null | sort)

drift=0
changed=0
while IFS= read -r f; do
  [ -n "$f" ] || continue
  # 该文件里出现过的、与目标不一致的版本
  stale="$(grep -oE "ops-allinone:[0-9]+\.[0-9]+\.[0-9]+" "$f" | sed 's/.*://' | grep -vFx "$TAG" | sort -u | tr '\n' ' ' || true)"
  [ -n "$stale" ] || continue
  drift=$((drift + 1))
  rel="${f#"$REPO_DIR"/}"
  if [ "$CHECK_ONLY" = "1" ]; then
    echo "  漂移 $rel  (${stale% } → ${TAG})"
  else
    sed -i.bak -E "s|(ops-allinone:)[0-9]+\.[0-9]+\.[0-9]+|\1${TAG}|g" "$f"
    rm -f "$f.bak"
    echo "  已同步 $rel  (${stale% } → ${TAG})"
    changed=$((changed + 1))
  fi
done <<< "$FILES"

if [ "$CHECK_ONLY" = "1" ]; then
  if [ "$drift" -gt 0 ]; then
    echo "目标 tag = ${TAG}，上述 $drift 个文件与之不一致。跑 ./sync-version.sh 修正。" >&2
    exit 1
  fi
  echo "版本一致：全部为 ${TAG}"
else
  echo "完成：目标 tag = ${TAG}，同步了 $changed 个文件。"
fi
