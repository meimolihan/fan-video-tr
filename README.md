# fan-video-tr

内置 FFmpeg 的**视频转码 Web 工具**：单文件二进制 + 原生前端（`go:embed` 内嵌），不依赖 Node、不依赖外部 CDN，开箱即用。

- 媒体库浏览 → 视频预览（原生 `<video>`，支持 Range 断点续播）
- 批量转码：H.264 / H.265 / VP9 / AV1，可选 NVENC / QSV / VAAPI 硬件加速
- 质量模式：CRF 或固定码率；支持**双遍编码**（按分辨率自动折算目标码率）
- 输出容器：MP4 / MKV / WebM / MOV，自动校验容器与编码的兼容性
- 转码模板（内置 8 套 + 自定义）、时间区间裁剪、输出体积预估
- 任务队列：进度 / 速度 / 剩余时间、暂停取消、失败重试、下载、删除产物
- 硬件加速**真实自检**：启动时跑一次微型编码，不可用的加速方式会被标记为不可用（`auto` 自动回退）

> 定位是**纯视频转码**：不做封面管理、不做片头片尾合成。

---

## 快速开始

### 二进制直接运行

```bash
# 编译（需要 Go 1.25+）
make build            # 产物 bin/fan-video-tr

# 启动
./bin/fan-video-tr -port 8790 -media /path/to/videos
```

浏览器打开 <http://127.0.0.1:8790>。

### Docker Compose（推荐）

```bash
cp docker-compose.yml docker-compose.override.yml   # 按需修改素材路径与数据目录
docker compose up -d
docker compose logs -f
```

镜像：`mobufan/fan-video-tr`（Docker Hub / GHCR，multi-arch：`linux/amd64`、`linux/arm64`）。
硬件转码需放开设备映射（compose 中已给出 `/dev/dri` 示例，NVIDIA 另需 container toolkit）。

### systemd 安装

```bash
bash scripts/install.sh                 # 默认 8790 端口 + systemd 服务
bash scripts/install.sh -p 8790 -s /vol2/1000/videos -d /var/lib/fan-video-tr
bash scripts/uninstall.sh               # 卸载（--purge 连数据目录一起删）
```

---

## 命令行

```
fan-video-tr [命令] [选项]

（无命令）              启动 Web 服务
  -port     <端口>     监听端口（默认 8790）
  -data     <目录>     数据目录（默认 ./data）
  -media    <目录>     视频浏览根目录（默认当前工作目录）
  -output   <目录>     转码输出目录（默认 <data>/output）
  -worker   <数量>     并发转码数（默认 1）

status                 查看运行方式 / PID / 端口 / 资源占用 / 健康检查
start | stop | restart 启动 / 停止 / 重启服务（systemd 优先）
uninstall [-y]         停止服务并移除安装（--purge 删除数据目录）
backup | restore       备份 / 恢复数据目录说明
version | -v           版本号
help | -h              帮助
```

## 配置

优先级：**命令行参数 > 环境变量 > `config.yaml` > 内置默认值**。

- 配置文件查找：`./config.yaml` → `./data/config.yaml` → `/etc/fan-video-tr/config.yaml`
- 环境变量：前缀 `FVT_`，层级用下划线，例如 `app.data_dir` → `FVT_APP_DATA_DIR`
- 完整示例见 [`config.example.yaml`](config.example.yaml)

常用环境变量：

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `FVT_APP_PORT` | `8790` | 监听端口 |
| `FVT_APP_DATA_DIR` | `./data` | 数据目录（输出、模板） |
| `FVT_APP_MEDIA_DIR` | 空 | 视频浏览根目录 |
| `FVT_APP_OUTPUT_DIR` | `output` | 转码输出目录（相对 `data_dir`） |
| `FVT_APP_WORKER` | `1` | 并发转码数 |
| `FVT_FFMPEG_ACCEL` | `auto` | `auto` / `none` / `nvenc` / `qsv` / `vaapi` |
| `FVT_FFMPEG_DETECT_ACCEL` | `true` | 启动时自检硬件编码 |
| `FVT_LOGGING_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |

---

## 硬件加速

启动时会**真实跑一次微型编码**（非仅看设备节点），结果可在界面「硬件加速」区看到：

- **NVENC**：需要 NVIDIA 驱动 + 容器映射 GPU
- **QSV**：Intel 核显 / 独显，容器映射 `/dev/dri`
- **VAAPI**：AMD / Intel，容器映射 `/dev/dri` 并把用户加入 `video` / `render` 组

选 `auto` 时按 NVENC → QSV → VAAPI → 软件 的顺序回退；硬件路径默认关闭硬件解码（`hw_decode: false`），兼容性最好。

## 转码参数要点

- **CRF 与双遍编码互斥**：x264/x265/VP9/AV1 都拒绝 `CRF + 2pass`。勾选双遍时会自动切换为码率模式，并按目标分辨率折算目标码率。
- **双遍编码要求两遍设置完全一致**（preset / 像素格式），否则编码器会拒绝第二遍；程序已统一处理。
- **不支持双遍的编码器**：本机 `libsvtav1` 走单遍，选 AV1 + 双遍时会明确报错，而不是排队后失败。
- **容器兼容性**：WebM 仅接受 VP9/AV1 视频与 Opus 音频；界面切换容器时会自动纠正不兼容的编码。
- **不放大**：目标分辨率小于等于源分辨率时保持原尺寸，不会强行放大。

## HTTP API

| 方法与路径 | 说明 |
| --- | --- |
| `GET /api/health` | 健康检查 |
| `GET /api/version` | 版本信息 |
| `GET /api/capabilities` | 编码器 / 硬件加速 / 能力字典 |
| `GET /api/options` | 前端字典与默认参数 |
| `GET /api/media/dir` | 目录浏览 |
| `GET /api/media/info` | 媒体信息（编码、分辨率、帧率、时长…） |
| `GET /api/media/stream` | 视频流（支持 Range） |
| `GET /api/media/thumb` | 缩略帧 |
| `POST /api/media/probe` | 探测并返回可转码参数 |
| `GET/POST/DELETE /api/tasks` | 任务列表 / 批量创建 / 清空 |
| `GET /api/tasks/stats` | 队列统计 |
| `GET /api/tasks/output-targets` | 可选输出目录 |
| `GET/DELETE /api/tasks/:id` | 任务详情 / 取消 |
| `GET /api/tasks/:id/download` | 下载成品 |
| `DELETE /api/tasks/:id/output` | 删除成品（同时移除记录） |
| `GET/POST/DELETE /api/profiles` | 模板列表 / 保存 / 删除 |
| `DELETE /api/profiles/:name` | 删除模板 |

## 开发

```bash
make test        # go vet + go test
make build-all   # 交叉编译 linux amd64 + arm64
make docker      # 本地构建镜像
```

前端位于 `internal/embedded/web/`（`index.html` + `css/style.css` + `js/app.js` + `favicon.svg`），
通过 `go:embed` 打进二进制，**改前端后必须重新编译**。

## 发布

```bash
bash scripts/build-and-push.sh 1.0.1
```

脚本会更新 `internal/version/version.go` 的版本号、创建标签与 Release、推送 Docker 镜像（Docker Hub + GHCR），并同步到 CNB 镜像仓库；随后手动触发 `.github/workflows/release.yml`。

## 许可证

见 [LICENSE](LICENSE)。
