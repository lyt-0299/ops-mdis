#!/bin/bash
# ops-allinone 唯一构建脚本（1.4.0 终态）
# ====================================
# 1) 用仓库根目录 Dockerfile 编译前后端并构建单一镜像
# 2) 运行时由 ROLE 选择组件
# 3) push（compose 起栈见 ops.yaml.example）
# 4) REGRESSION=1 时：用新镜像重启本地栈并跑整体回归（test/mdis_regression.py），失败则非零退出
#    可覆盖：REG_COMPOSE(默认 /data/mingdao/script/ops-allinone.yaml) / REG_BASE(默认 http://127.0.0.1:9080)
#            REG_SUBPATH(默认 /mdis) / REG_TOKEN(默认从 REG_COMPOSE 里抠 ENV_OPS_TOKEN)
set -euo pipefail

# IMAGE 是版本真相源；下面 sync-version.sh 会把这个 tag 铺到所有 k8s 清单与文档。
if [ -n "${IMAGE:-}" ]; then IMAGE_OVERRIDDEN=1; else IMAGE_OVERRIDDEN=0; fi
IMAGE="${IMAGE:-registry.cn-hangzhou.aliyuncs.com/mdpublic/ops-allinone:1.5.12}"

SELF_DIR="$(cd "$(dirname "$0")" && pwd)"           # plugin/allinone
REPO_DIR="$(cd "$SELF_DIR/../.." && pwd)"           # 仓库根

# 清单里的 tag 必须跟着 IMAGE 走，否则客户照清单装到的是旧版本。
# 2026-08-04 前靠人肉逐个改，已漂成三档(build.sh=1.5.10 / k8s/=1.5.9 / k8s-cluster/=1.5.7)——
# 照 k8s-cluster/ 装的客户拿的是 1.5.7，而 1.5.10 修的正是 K8s 面板整片 No data。
if [ "$IMAGE_OVERRIDDEN" = "1" ]; then
  echo "==> 跳过版本同步（IMAGE 被外部覆盖为 $IMAGE，临时构建不动仓库文件）"
else
  echo "==> [0/3] 同步镜像 tag 到部署清单与文档"
  "$SELF_DIR/sync-version.sh"
fi

echo "==> 用仓库根目录 Dockerfile 构建单一镜像"
docker build --platform linux/amd64 -t "$IMAGE" "$REPO_DIR"

if [ "${PUSH:-1}" = "1" ]; then
  echo "==> push"
  docker push "$IMAGE"
else
  echo "==> 跳过 push (PUSH=0)"
fi
echo "完成：$IMAGE"

# ── 可选整体回归（REGRESSION=1 启用）────────────────────────────────────────
if [ "${REGRESSION:-0}" = "1" ]; then
  REG_COMPOSE="${REG_COMPOSE:-/data/mingdao/script/ops-allinone.yaml}"
  # 访问地址和子路径一律从 compose 里读，别写死。
  # 原先默认写的是 127.0.0.1:9080（当时 local_50 前面还有一层 openresty 反代），入口
  # 改成直连 gateway 的 48881 之后，回归每次都停在「UI 未就绪(最后状态码 000)」——
  # 镜像构建成功、九个容器也都起来了，看着像新版本把页面弄挂了，实际是夹具自己在
  # 敲一个早就没人监听的端口。发版前必跑的这一步，失败信息指向了完全错误的方向。
  REG_PORT="${REG_PORT:-$(awk '/^[[:space:]]*ports:/{f=1;next} f&&/^[[:space:]]*-[[:space:]]*"?[0-9]+:[0-9]+/{gsub(/[^0-9:]/,"");split($0,a,":");print a[1];exit}' "$REG_COMPOSE")}"
  REG_BASE="${REG_BASE:-http://127.0.0.1:${REG_PORT:-48881}}"
  REG_SUBPATH="${REG_SUBPATH-$(grep -m1 'ENV_OPS_SUB_PATH' "$REG_COMPOSE" | sed -E 's/.*ENV_OPS_SUB_PATH:[[:space:]]*"?([^"]*)"?.*/\1/')}"
  REG_TOKEN="${REG_TOKEN:-$(grep -m1 'ENV_OPS_TOKEN' "$REG_COMPOSE" | sed -E 's/.*ENV_OPS_TOKEN:\s*"?([^"]+)"?.*/\1/')}"

  # ⚠️ 这里必须把 compose 里的镜像替换成本次构建的 $IMAGE 再起。
  # 1.4.6 前是直接 `docker compose -f "$REG_COMPOSE" up -d`，而该 compose 里的 tag 是写死的
  # （local_50 上一直是 1.4.0）→ 容器原封不动继续跑旧镜像，compose 只打印 "Running"，
  # 回归全绿但**测的根本不是刚构建的东西**。发版前必跑的这一步一直在给假信心。
  REG_PROJECT="${REG_PROJECT:-$(basename "$(dirname "$REG_COMPOSE")")}"
  REG_RENDERED="$(mktemp /tmp/ops-regression-XXXXXX.yaml)"
  sed -E "s#(image:[[:space:]]*).*ops-allinone:.*#\1${IMAGE}#" "$REG_COMPOSE" > "$REG_RENDERED"
  echo "==> [回归] 用新镜像重启本地栈：$REG_COMPOSE (project=$REG_PROJECT, image=$IMAGE)"
  # --project-directory 必须显式指向原 compose 所在目录：compose 里的相对卷路径
  # (./volume/data/...) 是相对「project 目录」解析的，渲染文件放在 /tmp 而不带这个参数，
  # 会把整套数据卷挂到 /tmp/volume/... 上——容器起得来、回归也能跑，但跑的是一套空数据，
  # 表现为慢查询/诊断类接口 503、页面空白，看起来像代码回归，实则是回归夹具自己挂错了盘。
  docker compose -p "$REG_PROJECT" --project-directory "$(dirname "$REG_COMPOSE")" \
    -f "$REG_RENDERED" up -d

  # 断言：确认容器真的换成了本次构建的镜像，否则后面的“全部通过”没有意义
  running_wrong=$(docker ps --filter "label=com.docker.compose.project=${REG_PROJECT}" \
    --format '{{.Names}} {{.Image}}' | grep -v " ${IMAGE}$" || true)
  if [ -n "$running_wrong" ]; then
    echo "回归终止：以下容器没有跑在 $IMAGE 上——"; echo "$running_wrong"; exit 1
  fi

  echo "==> [回归] 等待 UI 就绪：${REG_BASE}${REG_SUBPATH}/web/"
  for i in $(seq 1 30); do
    code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 "${REG_BASE}${REG_SUBPATH}/web/" || true)
    [ "$code" = "200" ] && break
    sleep 3
  done
  [ "$code" = "200" ] || { echo "UI 未就绪(最后状态码 $code)，回归终止"; exit 1; }

  # ⚠️ 静态页 200 ≠ 能登录：nginx 与 ops-server 在同一个容器里，nginx 一起来静态页就
  # 200，而 ops-server 还要连 ops-mongo、挂告警子系统、起七个调度器，慢的时候要十几秒。
  # 这段空窗里 /api/* 是 502，回归第一步登录必然超时，报出来的却是「token 登录失败」——
  # 又是一次「发版前最后一道闸自己坏了，却报成产物有问题」。所以再等 API 真的能用。
  echo "==> [回归] 等待后端 API 就绪：${REG_BASE}${REG_SUBPATH}/api/auth"
  for i in $(seq 1 40); do
    acode=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 \
      -X POST -H "Authorization: ${REG_TOKEN}" "${REG_BASE}${REG_SUBPATH}/api/auth" || true)
    [ "$acode" = "200" ] && break
    sleep 3
  done
  [ "$acode" = "200" ] || { echo "后端 API 未就绪(最后状态码 $acode)，回归终止"; exit 1; }

  echo "==> [回归] 跑整体回归 test/mdis_regression.py"
  python3 "$REPO_DIR/test/mdis_regression.py" \
    --base "$REG_BASE" --subpath "$REG_SUBPATH" --token "$REG_TOKEN"
  echo "==> [回归] 全部通过"
fi
