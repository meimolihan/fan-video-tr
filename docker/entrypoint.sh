#!/bin/sh
#
# fan-video-tr 容器入口：权限自愈 + 降权运行
#
# 镜像以 root 启动本脚本，针对通过卷挂载进来的宿主目录（无论其属主/权限如何）
# 统一重建数据目录与转码输出目录，并全部交权给 fvt 用户，
# 随后以 fvt 身份执行应用（su-exec 降权，避免以 root 跑 web 服务）。
# 这样用户部署时只需挂载卷，无需关心宿主目录的 uid/gid。
set -eu

APP=/usr/local/bin/fan-video-tr
RUN_USER=fvt

DATA_DIR="${FVT_APP_DATA_DIR:-/data}"
OUTPUT_DIR="${FVT_APP_OUTPUT_DIR:-output}"
# OUTPUT_DIR 相对路径基于数据目录解析（与应用内逻辑保持一致）
case "${OUTPUT_DIR}" in
    /*) ;;
    *)  OUTPUT_DIR="${DATA_DIR}/${OUTPUT_DIR}" ;;
esac

# 1) 重建/校正目录，并把写权限全部交给 fvt
mkdir -p "${DATA_DIR}" "${OUTPUT_DIR}" || true
chown -R "${RUN_USER}:${RUN_USER}" "${DATA_DIR}" "${OUTPUT_DIR}" || true
chmod 755 "${DATA_DIR}" || true

# 2) 打印启动信息（经 stdout 传给 docker logs）
echo "[entrypoint] data=${DATA_DIR} output=${OUTPUT_DIR}"

# 3) 降权运行应用；保留原有命令行参数（如 --port 等）
exec su-exec "${RUN_USER}" "${APP}" "$@"