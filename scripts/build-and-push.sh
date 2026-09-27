#!/bin/bash
#
# fan-video-tr - 发布脚本（触发 GitHub Actions 自动构建）
# 不在本地编译任何产物：仅更新版本号、推送代码并打 v 开头 tag。
# 推送代码、打 tag 后显式调用 gh workflow run 触发发布流水线（日常 push 不会触发）：
#   release.yml -> amd64/arm64 二进制 + sha256 创建 GitHub Release（版本跟随 internal/version/version.go）
#                  + multi-arch Docker 镜像（latest + 版本标签）
#
# Usage:
#   TAG(必填) 形如 v1.0.0; --yes 免交互; -m "备注" 可选发版说明
#     bash scripts/build-and-push.sh v1.0.0 --yes -m "本次新增 xxx"
set -euo pipefail

info() { echo -e "${gl_lv}>>> $*${reset}"; }
warn() { echo -e "${gl_huang}!!! $*${reset}"; }
error() { echo -e "${gl_hong}ERROR: $*${reset}"; exit 1; }

list_color_init() {
    export gl_hui=$'\033[38;5;59m'
    export gl_hong=$'\033[38;5;9m'
    export gl_lv=$'\033[38;5;10m'
    export gl_huang=$'\033[38;5;11m'
    export gl_lan=$'\033[38;5;32m'
    export gl_bai=$'\033[38;5;15m'
    export gl_zi=$'\033[38;5;13m'
    export gl_bufan=$'\033[38;5;14m'
    export reset=$'\033[0m'
}
list_color_init

# 拉取本次 tag 触发的 workflow run：tag push 后 Actions 尚未注册新 run，
# 这里按 tag 过滤并轮询等待，且用 headSha 校验确实是本次 push 的 run。
get_gh_run_info() {
    local tag="$1"
    local expect_sha
    expect_sha=$(git rev-parse HEAD 2>/dev/null) || expect_sha=""

    local tries=0
    local max_tries=12
    local run_json=""
    local sha=""
    while (( tries < max_tries )); do
        run_json=$(gh run list --workflow=release.yml --limit 1 --branch "${tag}" \
            --json status,displayTitle,headBranch,event,databaseId,startedAt,headSha 2>/dev/null) || run_json=""
        if [[ -n "$run_json" && "$run_json" != "[]" ]]; then
            sha=$(echo "$run_json" | jq -r '.[0].headSha')
            if [[ -z "$expect_sha" || "$sha" == "$expect_sha" ]]; then
                echo "$run_json"
                return 0
            fi
        fi
        tries=$((tries + 1))
        if (( tries < max_tries )); then
            sleep 5
        fi
    done
    return 1
}

beautify_gh_run() {
    local tag="${1:-}"
    echo -e ""
    echo -e "${gl_zi}>>> GitHub Actions Release 流水线信息${gl_bai}"

    json_data=$(get_gh_run_info "${tag}")
    if [[ -z "$json_data" || "$json_data" == "[]" ]]; then
        echo -e "${gl_hong}[提示] ${reset}未获取到本次(${tag})流水线运行记录，可稍后用以下命令查看："
        echo -e "${gl_lv}gh run list --workflow=release.yml --branch ${tag}${reset}"
        return 1
    fi

    status=$(echo "$json_data" | jq -r '.[0].status')
    run_id=$(echo "$json_data" | jq -r '.[0].databaseId')
    case "$status" in
        in_progress) status_text="${gl_huang}运行中${reset}" ;;
        completed)
            conclusion=$(gh run view "$run_id" --json conclusion | jq -r '.conclusion')
            case "$conclusion" in
                success) status_text="${gl_lv}成功${reset}" ;;
                failure) status_text="${gl_hong}失败${reset}" ;;
                cancelled) status_text="${gl_hui}已取消${reset}" ;;
                *) status_text="${gl_huang}已完成(${conclusion})${reset}" ;;
            esac ;;
        *) status_text="${gl_hui}${status}${reset}" ;;
    esac
    printf "%-14s%s\n" "${gl_hui}[运行状态]：${reset}" "$status_text"
    printf "%-14s%s\n" "${gl_hui}[Run ID]：${reset}" "${gl_bufan}$run_id${reset}"
    echo -e "${gl_lv}实时跟踪流水线：${reset}gh run watch $run_id"
    echo -e "${gl_lv}查看完整日志：${reset}gh run view $run_id --log"
    return 0
}

YES_MODE=0
TAG=""
MSG=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        --yes) YES_MODE=1; shift ;;
        -m|--message)
            shift
            [ -n "${1:-}" ] || error "缺少 -m/--message 的备注内容"
            MSG="$1"
            shift
            ;;
        *) TAG="$1"; shift ;;
    esac
done

[ -z "${TAG}" ] && error "缺少 TAG 参数，用法: bash scripts/build-and-push.sh v1.0.0 --yes -m \"备注\""
TARGET_VER="${TAG#v}"
VER_RE='^v[0-9]+\.[0-9]+\.[0-9]+$'
[[ "${TAG}" =~ ${VER_RE} ]] || error "TAG 必须形如 vX.Y.Z（当前: ${TAG}）"

cd "$(dirname "$0")/.."

# ===================== CNB 同名仓库检测与自动创建 =====================
ensure_cnb_repo() {
    local token="${CNB_ACCESS_TOKEN:-}"
    if [ -z "${token}" ] && [ -f "${HOME}/.cnb_token" ]; then
        token=$(<"${HOME}/.cnb_token")
    fi
    if [ -z "${token}" ]; then
        warn "未配置 CNB_ACCESS_TOKEN 或 ~/.cnb_token，跳过 CNB 仓库检测"
        return 0
    fi
    local origin repo cname code
    origin=$(git config --get remote.origin.url 2>/dev/null || true)
    repo=$(basename "${origin}" .git 2>/dev/null || true)
    [ -z "${repo}" ] && repo=$(basename "$(pwd)" 2>/dev/null || true)
    cname="cnb.cool/meimolihan/${repo}"
    if GIT_TERMINAL_PROMPT=0 git ls-remote "https://cnb:${token}@${cname}.git" >/dev/null 2>&1; then
        info "CNB 同名仓库已存在：${cname}，跳过创建"
        return 0
    fi
    info "CNB 仓库不存在：${cname}，正在自动创建 ..."
    code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "https://api.cnb.cool/meimolihan/-/repos" \
        -H "Authorization: Bearer ${token}" \
        -H "Accept: application/vnd.cnb.api+json" \
        -H "Content-Type: application/json" \
        -d "{\"name\":\"${repo}\",\"visibility\":\"public\"}")
    if [ "${code}" = "201" ] || [ "${code}" = "200" ]; then
        info "CNB 仓库创建成功：${cname}（HTTP ${code}）"
    else
        warn "CNB 仓库创建返回 HTTP ${code}（可能已存在或权限不足），继续发布"
    fi
}
ensure_cnb_repo

if [ "${YES_MODE}" = "0" ]; then
    read -r -p "即将发布 ${TAG}，将 bump internal/version/version.go 并推送 main + tag，确认? [y/N] " ans
    [[ "${ans}" =~ ^[Yy]$ ]] || { echo "已取消"; exit 1; }
fi

[[ "$(git status --porcelain)" =~ .[MADR] ]] && {
    warn "工作区存在未提交改动:"
    git status --short
    error "请先提交或 stash 后再发布"
}

# ===================== 同步远端 =====================
info "fetch 远端状态"
git fetch origin
LOCAL_HASH=$(git rev-parse @)
REMOTE_HASH=$(git rev-parse @{u} 2>/dev/null || echo "")
if [ -n "${REMOTE_HASH}" ] && [ "${LOCAL_HASH}" != "${REMOTE_HASH}" ]; then
    error "本地 main 与远端不一致，请先 git pull"
fi

# ===================== bump 版本号 =====================
info "更新 internal/version/version.go 版本号 -> ${TAG}"
python3 - "${TARGET_VER}" <<'PY'
import re, sys
want = sys.argv[1]
p = 'internal/version/version.go'
s = open(p, encoding='utf-8').read()
new, n = re.subn(r'^var Version\s*=\s*"[0-9.]+"',
                 'var Version = "' + want + '"', s, count=1, flags=re.M)
if n == 0:
    print('ERROR: internal/version/version.go 中未找到 Version 定义'); sys.exit(1)
open(p, 'w', encoding='utf-8').write(new)
print('internal/version/version.go Version -> ' + want)
PY

git diff --stat

# ===================== 写发版备注 =====================
info "写入发版备注 RELEASE_NOTES.md"
{
  if [ -n "${MSG}" ]; then
    printf '%s\n' "${MSG}"
    printf '\n'
  fi
  printf '## Docker 安装\n'
  printf '```bash\n'
  printf 'docker pull mobufan/fan-video-tr:latest\n'
  printf 'docker run -d --name fan-video-tr -p 8790:8790 -v /var/lib/fan-video-tr:/data mobufan/fan-video-tr:%s\n' "${TAG}"
  printf '```\n'
  printf '\n'
  printf '```bash\n'
  printf 'docker pull ghcr.io/meimolihan/fan-video-tr:%s\n' "${TAG}"
  printf '```\n'
  printf '\n'
  printf '## 二进制安装\n'
  printf '### Linux amd64 / arm64\n'
  printf '```bash\n'
  printf 'bash -c "$(curl -sSL https://raw.githubusercontent.com/meimolihan/fan-video-tr/main/scripts/install.sh)" -p 8790\n'
  printf '```\n'
  printf '\n'
  printf '## 二进制卸载\n'
  printf '```bash\n'
  printf 'bash -c "$(curl -sSL https://raw.githubusercontent.com/meimolihan/fan-video-tr/main/scripts/uninstall.sh)" -y\n'
  printf '```\n'
} > RELEASE_NOTES.md

# ===================== Git 提交 & Tag =====================
info "提交版本变更"
git add .
git commit -q -m "chore: bump version to ${TAG}" || warn "无变更可提交？"
info "推送 main"
git push origin main
if git rev-parse -q --verify "refs/tags/${TAG}" >/dev/null; then
    if [ "${YES_MODE}" = "0" ]; then
        read -r -p "远端已存在 tag ${TAG}，删除并重建? [y/N] " ans
        [[ "${ans}" =~ ^[Yy]$ ]] || error "已取消（tag ${TAG} 已存在）"
    fi
    git tag -f -a "${TAG}" -m "${TAG}"
    git push origin ":refs/tags/${TAG}" --force
    git push origin "refs/tags/${TAG}" --force
else
    git tag -a "${TAG}" -m "${TAG}"
    git push origin "refs/tags/${TAG}"
fi

# ===================== 触发 GitHub Actions 发布流水线 =====================
info "触发 GitHub Actions 发布流水线 (release.yml, tag=${TAG})"
if ! command -v gh >/dev/null 2>&1; then
    warn "未安装 gh CLI，无法自动触发流水线。"
    warn "请手动运行: gh workflow run release.yml -f tag=${TAG} --ref ${TAG}"
    exit 0
fi
if ! gh workflow run release.yml -f tag="${TAG}" --ref "${TAG}" >/dev/null 2>&1; then
    warn "gh workflow run 触发失败，请检查："
    warn "  1. 已执行过 gh auth login 且 token 含 workflow 权限"
    warn "  2. 远端已推送 tag ${TAG}"
    warn "  可手动触发: gh workflow run release.yml -f tag=${TAG} --ref ${TAG}"
    exit 1
fi
info "已触发发布流水线，tag=${TAG}"

info "查看发布结果: gh release view ${TAG}"
info "  docker pull mobufan/fan-video-tr:${TAG}"


echo -e "${gl_bai}远程安装命令： ${gl_lv}bash -c \"\$(curl -sSL https://raw.githubusercontent.com/meimolihan/fan-video-tr/main/scripts/install.sh)\" -p 8790 -d /var/lib/fan-video-tr -s /vol2/1000/downloads/fan-video-tr${gl_bai}"


beautify_gh_run "${TAG}"