# 更新日志

本项目遵循语义化版本。发布记录见 GitHub Releases。

## v1.0.0（首个正式版）

### 功能
- 媒体库浏览、目录切换、视频在线预览（Range 断点续播）、缩略帧提取
- 批量转码：H.264 / H.265 / VP9 / AV1，MP4 / MKV / WebM / MOV 输出
- 质量控制：CRF、固定码率（VBV 约束）、双遍编码
- 硬件加速：NVENC / QSV / VAAPI，启动时真实微型编码自检，`auto` 自动回退
- 时间区间裁剪、帧率 / 分辨率 / 像素格式 / 音频编码与码率 / 声道设置
- 转码模板：内置 8 套常用模板，支持自定义增删（中文名可用）
- 任务队列：进度、速度、剩余时间、取消、失败信息、下载、删除产物、批量清理
- 输出体积预估、输出目录选择（源目录 / 默认目录 / 自定义）
- 深色 / 浅色主题、键盘快捷键、响应式布局

### 工程
- 单二进制部署：前端通过 `go:embed` 内嵌，无 Node、无外部 CDN
- CLI：`status` / `start|stop|restart` / `uninstall` / `backup|restore` / `version`
- 配置：命令行 > 环境变量（`FVT_` 前缀）> `config.yaml` > 默认值
- 部署：`Dockerfile`（multi-arch）、`docker-compose.yml`、`Makefile`、systemd 安装脚本
- CI：编译 + vet + 单测；发版工作流产出 amd64/arm64 二进制与多架构镜像

### 已知限制
- 本机 FFmpeg 未编译 `libaom-av1` 时，AV1 只能走 `libsvtav1`（单遍），AV1 + 双遍会明确报错
- `libsvtav1` 不支持双遍编码；`x264/x265/vpx/aom` 支持
- 硬件路径默认关闭硬件解码（`ffmpeg.hw_decode` 可开），以优先保证兼容性
