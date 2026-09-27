package handler

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/meimolihan/fan-video-tr/internal/ffmpeg"
	"github.com/meimolihan/fan-video-tr/internal/service"
)

type metaHandler struct {
	svc *service.Service
	log *zap.SugaredLogger
}

// version GET /api/version
func (h *metaHandler) version(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"app":     "fan-video-tr",
		"name":    "视频转码工具",
		"version": versionString(),
	})
}

// capabilities GET /api/capabilities
// 返回本机 FFmpeg 编码器与硬件加速可用性（前端编码器下拉框的唯一数据源）。
func (h *metaHandler) capabilities(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 40*time.Second)
	defer cancel()
	caps := h.svc.FFmpeg.Detect(ctx)

	// 常用软件 preset 精简列表（前端 select 用）
	presets := caps.SoftwarePresets
	if len(presets) == 0 {
		presets = []string{"ultrafast", "veryfast", "faster", "fast", "medium", "slow", "slower", "veryslow"}
	}

	c.JSON(http.StatusOK, gin.H{
		"ffmpeg":          caps.FFmpeg,
		"ffprobe":         caps.FFprobe,
		"ffmpeg_path":     caps.FfmpegPath,
		"encoders":        caps.Encoders,
		"software_codecs": caps.SoftwareCodecs,
		"presets":         presets,
		"hw_accel":        caps.HWAccel,
		"hw_decode":       caps.HWDecode,
		"pixel_formats":   caps.PixelFormats,
		"detected":        caps.Detected,
	})
}

// codecOption 前端编码器选项。
type codecOption struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Note       string `json:"note"`
	Container  string `json:"container"`
	MaxQuality int    `json:"max_quality"`
	Available  bool   `json:"available"`
	// Encoders 可用编码器（含硬件）
	Encoders []encoderOption `json:"encoders"`
}

// encoderOption 单个编码器实现。
type encoderOption struct {
	Name  string `json:"name"`
	Accel string `json:"accel"`
	Label string `json:"label"`
}

// options GET /api/options
// 返回前端参数表单所需的全部选项字典（分辨率、容器、音频、预设默认值等）。
func (h *metaHandler) options(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 40*time.Second)
	defer cancel()
	caps := h.svc.FFmpeg.Detect(ctx)

	// 编码族 → 各加速路径的编码器
	codecs := make([]codecOption, 0, len(ffmpeg.CodecFamilies()))
	for _, f := range ffmpeg.CodecFamilies() {
		opt := codecOption{
			ID:         f.ID,
			Name:       f.Name,
			Note:       f.Note,
			Container:  f.Container,
			MaxQuality: f.MaxQuality,
			Encoders:   []encoderOption{},
		}
		for _, enc := range f.SoftwareEncoders {
			opt.Encoders = append(opt.Encoders, encoderOption{
				Name:  enc,
				Accel: ffmpeg.AccelNone,
				Label: "软件 " + enc,
			})
		}
		for _, accel := range ffmpeg.AccelNames() {
			enc, ok := f.HWEncoders[accel]
			if !ok {
				continue
			}
			opt.Encoders = append(opt.Encoders, encoderOption{
				Name:  enc,
				Accel: accel,
				Label: strings.ToUpper(accel) + " " + enc,
			})
		}
		// 软件编码器至少要有一个可用
		for _, enc := range f.SoftwareEncoders {
			if containsStr(caps.Encoders, enc) {
				opt.Available = true
				break
			}
		}
		codecs = append(codecs, opt)
	}

	containers := make([]gin.H, 0, len(ffmpeg.Containers()))
	for _, ct := range ffmpeg.Containers() {
		containers = append(containers, gin.H{
			"id":           ct.ID,
			"name":         ct.Name,
			"ext":          ct.Ext,
			"mime_type":    ct.MimeType,
			"video_codecs": ct.VideoCodecs,
			"audio_codecs": ct.AudioCodecs,
			"fast_start":   ct.FastStart,
			"note":         ct.Note,
		})
	}

	resolutions := make([]gin.H, 0, len(ffmpeg.Resolutions()))
	for _, r := range ffmpeg.Resolutions() {
		resolutions = append(resolutions, gin.H{
			"id": r.ID, "name": r.Name, "width": r.Width, "height": r.Height, "label": r.Label,
		})
	}

	fps := make([]gin.H, 0, len(ffmpeg.FPSOptions()))
	for _, o := range ffmpeg.FPSOptions() {
		fps = append(fps, gin.H{"id": o.ID, "name": o.Name, "value": o.Value, "label": o.Label})
	}

	audioCodecs := make([]gin.H, 0)
	for _, a := range ffmpeg.AudioCodecs() {
		audioCodecs = append(audioCodecs, gin.H{"id": a.ID, "name": a.Name, "label": a.Label})
	}
	audioChannels := make([]gin.H, 0)
	for _, a := range ffmpeg.AudioChannels() {
		audioChannels = append(audioChannels, gin.H{"id": a.ID, "name": a.Name, "value": a.Value, "label": a.Label})
	}

	presets := caps.SoftwarePresets
	if len(presets) == 0 {
		presets = []string{"ultrafast", "veryfast", "faster", "fast", "medium", "slow", "slower", "veryslow"}
	}

	// 常用目标码率（按分辨率档位分组）
	bitrates := map[string][]string{
		"720p":  {"1.5M", "2M", "3M", "4M", "6M"},
		"1080p": {"2M", "3M", "4M", "6M", "8M", "12M"},
		"2160p": {"8M", "12M", "16M", "25M", "40M"},
	}

	c.JSON(http.StatusOK, gin.H{
		"codecs":         codecs,
		"containers":     containers,
		"resolutions":    resolutions,
		"fps":            fps,
		"pixel_formats":  ffmpeg.PixelFormats(),
		"audio_codecs":   audioCodecs,
		"audio_bitrates": ffmpeg.AudioBitrates(),
		"audio_channels": audioChannels,
		"presets":        presets,
		"bitrates":       bitrates,
		"accels":         accelOptions(caps),
		"defaults":       service.DefaultParams(),
		"name_suffix":    service.DefaultNameSuffix,
		"max_batch":      500,
		"output_dir":     h.svc.Cfg.OutputDir(),
		"data_dir":       h.svc.Cfg.App.DataDir,
		"worker":         h.svc.Tasks.Workers(),
	})
}

// accelOptions 返回硬件加速选项（含不可用项与原因，便于界面提示）。
func accelOptions(caps *ffmpeg.Capabilities) []gin.H {
	out := []gin.H{{"id": "auto", "name": "自动（优先硬件）", "available": true}}
	for _, a := range caps.HWAccel {
		out = append(out, gin.H{
			"id":        a.ID,
			"name":      a.Name,
			"available": a.Available,
			"reason":    a.Reason,
			"device":    a.Device,
			"encoders":  a.Encoders,
			"codecs":    a.Codecs,
		})
	}
	return out
}

// containsStr 判断字符串切片是否包含目标值。
func containsStr(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// nowFunc / since 便于测试替换的时间辅助。
var nowFunc = time.Now

func since(start time.Time) string { return time.Since(start).Round(time.Millisecond).String() }

// ==================== 码率推荐 ====================

// suggestBitrate 为指定分辨率/帧率推荐视频码率（bps）。
func suggestBitrate(width, height int, fps float64) int64 {
	return ffmpeg.SuggestBitrate(width, height, fps)
}

// parsePositiveInt 解析正整数参数。
func parsePositiveInt(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return def
}
