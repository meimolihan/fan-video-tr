package ffmpeg

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// ==================== 编码族定义 ====================

// CodecFamily 描述一个视频编码族（H.264 / H.265 / VP9 / AV1）及其在各加速
// 路径下的编码器实现。软件编码器按优先级排列，第一个存在的即被选用。
type CodecFamily struct {
	// ID 编码族标识：h264 / h265 / vp9 / av1
	ID string
	// Name 中文展示名
	Name string
	// SoftwareEncoders 软件编码器候选（按优先级）
	SoftwareEncoders []string
	// HWEncoders 硬件编码器映射：加速标识 → 编码器名
	HWEncoders map[string]string
	// Container 该编码族推荐的输出容器
	Container string
	// AudioCodec 推荐音频编码
	AudioCodec string
	// MaxQuality 质量参数（CRF）上限，越大越糙
	MaxQuality int
	// Note 展示用说明
	Note string
}

// CodecFamilies 返回全部编码族定义（顺序即前端展示顺序）。
func CodecFamilies() []CodecFamily {
	return []CodecFamily{
		{
			ID:               "h264",
			Name:             "H.264 / AVC",
			SoftwareEncoders: []string{"libx264"},
			HWEncoders: map[string]string{
				AccelNVENC: "h264_nvenc",
				AccelQSV:   "h264_qsv",
				AccelVAAPI: "h264_vaapi",
			},
			Container:  "mp4",
			AudioCodec: "aac",
			MaxQuality: 51,
			Note:       "兼容性最好，通用播放器通吃",
		},
		{
			ID:               "h265",
			Name:             "H.265 / HEVC",
			SoftwareEncoders: []string{"libx265"},
			HWEncoders: map[string]string{
				AccelNVENC: "hevc_nvenc",
				AccelQSV:   "hevc_qsv",
				AccelVAAPI: "hevc_vaapi",
			},
			Container:  "mp4",
			AudioCodec: "aac",
			MaxQuality: 51,
			Note:       "同画质体积约为 H.264 的 50%~70%",
		},
		{
			ID:               "vp9",
			Name:             "VP9",
			SoftwareEncoders: []string{"libvpx-vp9"},
			HWEncoders: map[string]string{
				AccelVAAPI: "vp9_vaapi",
			},
			Container:  "webm",
			AudioCodec: "opus",
			MaxQuality: 63,
			Note:       "WebM 容器，网页播放友好",
		},
		{
			ID:               "av1",
			Name:             "AV1",
			SoftwareEncoders: []string{"libsvtav1", "libaom-av1"},
			HWEncoders: map[string]string{
				AccelNVENC: "av1_nvenc",
				AccelQSV:   "av1_qsv",
				AccelVAAPI: "av1_vaapi",
			},
			Container:  "mp4",
			AudioCodec: "aac",
			MaxQuality: 63,
			Note:       "压缩率最高，软件编码较慢",
		},
	}
}

// CodecFamilyByID 按标识查找编码族。
func CodecFamilyByID(id string) (CodecFamily, bool) {
	for _, f := range CodecFamilies() {
		if f.ID == id {
			return f, true
		}
	}
	return CodecFamily{}, false
}

// ==================== 输出容器 ====================

// Container 输出容器定义。
type Container struct {
	ID   string
	Name string
	Ext  string
	// MimeType 浏览器 / 下载用 MIME
	MimeType string
	// VideoCodecs 兼容的视频编码族
	VideoCodecs []string
	// AudioCodecs 兼容的音频编码
	AudioCodecs []string
	// FastStart 是否支持 faststart（仅 MP4/MOV）
	FastStart bool
	// Note 展示用说明
	Note string
}

// Containers 返回全部支持的输出容器定义。
func Containers() []Container {
	return []Container{
		{
			ID: "mp4", Name: "MP4", Ext: ".mp4", MimeType: "video/mp4",
			VideoCodecs: []string{"h264", "h265", "av1"},
			AudioCodecs: []string{"aac", "opus", "copy", "none"},
			FastStart:   true,
			Note:        "通用性最好，支持边下边播",
		},
		{
			ID: "mkv", Name: "MKV", Ext: ".mkv", MimeType: "video/x-matroska",
			VideoCodecs: []string{"h264", "h265", "vp9", "av1"},
			AudioCodecs: []string{"aac", "opus", "copy", "none"},
			Note:        "可容纳任意编码与多音轨",
		},
		{
			ID: "webm", Name: "WebM", Ext: ".webm", MimeType: "video/webm",
			// WebM 是 Matroska 的受限子集：只接受 VP8/VP9/AV1 视频与 Opus/Vorbis 音频
			VideoCodecs: []string{"vp9", "av1"},
			AudioCodecs: []string{"opus", "none"},
			Note:        "Web / 流媒体首选",
		},
		{
			ID: "mov", Name: "MOV", Ext: ".mov", MimeType: "video/quicktime",
			VideoCodecs: []string{"h264", "h265"},
			AudioCodecs: []string{"aac", "copy", "none"},
			FastStart:   true,
			Note:        "Apple 生态友好",
		},
	}
}

// ContainerByID 按标识查找容器。
func ContainerByID(id string) (Container, bool) {
	for _, ct := range Containers() {
		if ct.ID == id {
			return ct, true
		}
	}
	return Container{}, false
}

// ContainerByExt 按扩展名查找容器。
func ContainerByExt(ext string) (Container, bool) {
	ext = strings.ToLower(ext)
	if ext == "" {
		return Container{}, false
	}
	for _, ct := range Containers() {
		if ct.Ext == ext {
			return ct, true
		}
	}
	return Container{}, false
}

// MimeTypeForExt 由扩展名推导 MIME（播放与下载用）。
func MimeTypeForExt(ext string) string {
	if ct, ok := ContainerByExt(ext); ok {
		return ct.MimeType
	}
	return ""
}

// ==================== 分辨率 ====================

// Resolution 常用分辨率档位。
type Resolution struct {
	// ID 档位标识：keep / 2160p / ... / 自定义 WxH
	ID string
	// Name 展示名
	Name string
	// Width / Height 像素尺寸（0 表示保持原始）
	Width  int
	Height int
	// Label 附加说明（如 4K / 竖屏提示）
	Label string
}

// Resolutions 返回可选的分辨率档位（含"保持原始"与"自定义"）。
func Resolutions() []Resolution {
	return []Resolution{
		{ID: "keep", Name: "保持原始", Label: "不缩放"},
		{ID: "2160p", Name: "4K · 2160p", Width: 3840, Height: 2160, Label: "超高清"},
		{ID: "1440p", Name: "2K · 1440p", Width: 2560, Height: 1440, Label: "QHD"},
		{ID: "1080p", Name: "全高清 · 1080p", Width: 1920, Height: 1080, Label: "FHD"},
		{ID: "720p", Name: "高清 · 720p", Width: 1280, Height: 720, Label: "HD"},
		{ID: "480p", Name: "标清 · 480p", Width: 854, Height: 480, Label: "SD"},
		{ID: "360p", Name: "流畅 · 360p", Width: 640, Height: 360, Label: "低清"},
		{ID: "custom", Name: "自定义…", Label: "手动填写宽高"},
	}
}

// ResolutionByID 按标识查找分辨率档位。
func ResolutionByID(id string) (Resolution, bool) {
	for _, r := range Resolutions() {
		if r.ID == id {
			return r, true
		}
	}
	return Resolution{}, false
}

// ParseCustomResolution 解析 "1920x1080" 形式的自定义分辨率。
func ParseCustomResolution(s string) (int, int, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return 0, 0, fmt.Errorf("自定义分辨率不能为空")
	}
	sep := strings.IndexAny(s, "x*×")
	if sep <= 0 {
		return 0, 0, fmt.Errorf("自定义分辨率格式应为 宽x高，如 1920x1080")
	}
	w, err := strconv.Atoi(strings.TrimSpace(s[:sep]))
	if err != nil {
		return 0, 0, fmt.Errorf("宽度不是有效数字")
	}
	h, err := strconv.Atoi(strings.TrimSpace(s[sep+1:]))
	if err != nil {
		return 0, 0, fmt.Errorf("高度不是有效数字")
	}
	if w < 16 || h < 16 {
		return 0, 0, fmt.Errorf("宽高过小（至少 16）")
	}
	if w > 16384 || h > 16384 {
		return 0, 0, fmt.Errorf("宽高超出支持范围（最大 16384）")
	}
	return w, h, nil
}

// ==================== 帧率档位 ====================

// FPSOption 帧率档位。
type FPSOption struct {
	ID    string
	Name  string
	Value float64
	Label string
}

// FPSOptions 返回可选帧率（含"保持原始"）。
func FPSOptions() []FPSOption {
	return []FPSOption{
		{ID: "keep", Name: "保持原始", Label: "不调整"},
		{ID: "60", Name: "60 fps", Value: 60, Label: "高帧率"},
		{ID: "30", Name: "30 fps", Value: 30, Label: "通用"},
		{ID: "25", Name: "25 fps", Value: 25, Label: "PAL 制"},
		{ID: "24", Name: "24 fps", Value: 24, Label: "电影感"},
		{ID: "23.976", Name: "23.976 fps", Value: 24000.0 / 1001.0, Label: "NTSC 电影"},
		{ID: "15", Name: "15 fps", Value: 15, Label: "省空间"},
	}
}

// FPSValue 解析帧率档位；支持直接传入数字（如 "29.97"）。
func FPSValue(id string) (float64, bool) {
	s := strings.TrimSpace(id)
	if s == "" || s == "keep" {
		return 0, true
	}
	for _, o := range FPSOptions() {
		if o.ID == s {
			return o.Value, true
		}
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && f > 0 && f <= 240 {
		return f, true
	}
	// 兼容 30000/1001 这类 N/D 分数写法（ffmpeg 原生接受）
	if strings.Contains(s, "/") {
		if f := rationalValue(s); f > 0 && f <= 240 {
			return f, true
		}
	}
	return 0, false
}

// ==================== 像素格式 ====================

// PixelFormats 返回可选像素格式。
func PixelFormats() []string {
	return []string{"yuv420p", "yuv420p10le", "yuv422p10le", "yuv444p"}
}

// NormalizePixelFormat 校验像素格式，keep / 空值回退到 yuv420p。
func NormalizePixelFormat(pf string) (string, bool) {
	pf = strings.ToLower(strings.TrimSpace(pf))
	if pf == "" || pf == "keep" {
		return "yuv420p", true
	}
	for _, v := range PixelFormats() {
		if v == pf {
			return v, true
		}
	}
	return "", false
}

// ==================== 音频 ====================

// AudioCodecOption 音频编码选项。
type AudioCodecOption struct {
	ID   string
	Name string
	// Ext 输出文件扩展名（copy 时用于推导）
	Label string
}

// AudioCodecs 返回可选音频编码。
func AudioCodecs() []AudioCodecOption {
	return []AudioCodecOption{
		{ID: "aac", Name: "AAC", Label: "MP4 通用"},
		{ID: "opus", Name: "Opus", Label: "WebM 首选"},
		{ID: "mp3", Name: "MP3", Label: "兼容性最高"},
		{ID: "copy", Name: "直接复制", Label: "不重新编码"},
		{ID: "none", Name: "移除音轨", Label: "输出无声视频"},
	}
}

// AudioEncoderName 返回音频编码的 ffmpeg 编码器名。
func AudioEncoderName(id string) string {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "aac":
		return "aac"
	case "opus":
		return "libopus"
	case "mp3":
		return "libmp3lame"
	case "ac3":
		return "ac3"
	case "flac":
		return "flac"
	default:
		return ""
	}
}

// AudioChannels 返回可选声道设置。
func AudioChannels() []struct {
	ID    string
	Name  string
	Value int
	Label string
} {
	return []struct {
		ID    string
		Name  string
		Value int
		Label string
	}{
		{ID: "keep", Name: "保持原始", Value: 0, Label: "不调整"},
		{ID: "stereo", Name: "立体声", Value: 2, Label: "双声道"},
		{ID: "mono", Name: "单声道", Value: 1, Label: "省空间"},
	}
}

// ChannelValue 解析声道设置。
func ChannelValue(id string) int {
	switch strings.ToLower(strings.TrimSpace(id)) {
	case "stereo", "2":
		return 2
	case "mono", "1":
		return 1
	default:
		return 0
	}
}

// AudioBitrates 返回可选音频码率。
func AudioBitrates() []string {
	return []string{"64k", "96k", "128k", "160k", "192k", "256k", "320k"}
}

// ==================== 输出扩展名 ====================

// OutputExtension 依据容器返回输出文件扩展名。
func OutputExtension(container string) string {
	if ct, ok := ContainerByID(container); ok {
		return ct.Ext
	}
	return ".mp4"
}

// OutputExtensionForInput 依据源文件扩展名推测输出容器（用作默认容器）。
func OutputExtensionForInput(inputPath string) string {
	ext := strings.ToLower(filepath.Ext(inputPath))
	switch ext {
	case ".mp4", ".m4v", ".mov":
		return "mp4"
	case ".mkv":
		return "mkv"
	case ".webm":
		return "webm"
	default:
		return "mp4"
	}
}

// ==================== 码率解析 ====================

// ParseBitrate 解析 "4M" / "8000k" / "1500000" 形式的码率为 bps。
// 返回 0 表示"自动/不限制"。
func ParseBitrate(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" || s == "0" || s == "auto" {
		return 0, nil
	}
	mult := int64(1000)
	switch {
	case strings.HasSuffix(s, "k"):
		mult = 1000
		s = strings.TrimSuffix(s, "k")
	case strings.HasSuffix(s, "m"):
		mult = 1000 * 1000
		s = strings.TrimSuffix(s, "m")
	case strings.HasSuffix(s, "g"):
		mult = 1000 * 1000 * 1000
		s = strings.TrimSuffix(s, "g")
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, fmt.Errorf("码率格式无效: %q（示例 4M / 6000k）", s)
	}
	if f <= 0 {
		return 0, fmt.Errorf("码率必须大于 0")
	}
	return int64(f * float64(mult)), nil
}

// SuggestBitrate 依据分辨率与帧率推荐一个视频码率（bps）。
// 经验系数：以 1080p30 / 6Mbps 为基准按像素与帧率线性缩放。
func SuggestBitrate(width, height int, fps float64) int64 {
	if width <= 0 || height <= 0 {
		return 4_000_000
	}
	if fps <= 0 {
		fps = 30
	}
	pixels := float64(width * height)
	fpsFactor := fps / 30.0
	if fpsFactor < 0.5 {
		fpsFactor = 0.5
	}
	if fpsFactor > 4 {
		fpsFactor = 4
	}
	// 1080p 基准 6 Mbps / 每像素每秒
	base := pixels * 0.55 * fpsFactor
	if base < 250_000 {
		base = 250_000
	}
	if base > 120_000_000 {
		base = 120_000_000
	}
	return int64(base)
}
