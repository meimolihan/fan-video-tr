package service

import (
	"fmt"
	"strings"

	"github.com/meimolihan/fan-video-tr/internal/ffmpeg"
)

// TranscodeParams 一次转码的完整参数（前端表单 / 预设模板的载荷结构）。
// 字段与 ffmpeg.TranscodeOptions 一一对应，仅多一层 JSON 标签。
type TranscodeParams struct {
	// Codec 视频编码族：h264 / h265 / vp9 / av1
	Codec string `json:"codec"`
	// Accel 硬件加速：auto / none / nvenc / qsv / vaapi
	Accel string `json:"accel"`
	// RateControl 质量控制：crf / bitrate
	RateControl string `json:"rate_control"`
	// CRF 恒定质量（rate_control=crf 时生效）
	CRF int `json:"crf"`
	// VideoBitrate 目标视频码率，如 "4M"（rate_control=bitrate 时生效）
	VideoBitrate string `json:"video_bitrate"`
	// Maxrate 峰值码率上限（留空自动取目标码率 2 倍）
	Maxrate string `json:"maxrate,omitempty"`
	// BufSize 码率缓冲（留空自动取目标码率 2 倍）
	BufSize string `json:"bufsize,omitempty"`
	// Preset 编码预设（软件为 x264/x265 档位，硬件由服务端映射）
	Preset string `json:"preset"`
	// TwoPass 两遍编码（仅软件编码生效，画质/体积更优但耗时翻倍）
	TwoPass bool `json:"two_pass"`
	// Resolution 分辨率档位：keep / 2160p / 1440p / 1080p / 720p / 480p / 360p / custom
	Resolution string `json:"resolution"`
	// CustomSize 自定义分辨率，如 "1920x1080"
	CustomSize string `json:"custom_size,omitempty"`
	// FPS 帧率：keep / 60 / 30 / 25 / 24 / 23.976 / 15
	FPS string `json:"fps"`
	// PixelFormat 像素格式：keep / yuv420p / yuv420p10le / yuv422p10le / yuv444p
	PixelFormat string `json:"pixel_format"`
	// AudioCodec 音频编码：aac / opus / mp3 / copy / none
	AudioCodec string `json:"audio_codec"`
	// AudioBitrate 音频码率，如 "192k"
	AudioBitrate string `json:"audio_bitrate"`
	// AudioChannels 声道：keep / stereo / mono
	AudioChannels string `json:"audio_channels"`
	// Container 输出容器：mp4 / mkv / webm / mov
	Container string `json:"container"`
	// FastStart 前置 moov（MP4/MOV 边下边播）
	FastStart bool `json:"fast_start"`
	// Start / End 可选时间区间（秒）；End<=0 表示从 0 到结尾
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// DefaultParams 返回前端表单的初始参数（通用均衡档）。
func DefaultParams() TranscodeParams {
	return TranscodeParams{
		Codec:         "h264",
		Accel:         "auto",
		RateControl:   ffmpeg.RateCRF,
		CRF:           23,
		Preset:        "veryfast",
		TwoPass:       false,
		Resolution:    "keep",
		FPS:           "keep",
		PixelFormat:   "yuv420p",
		AudioCodec:    "aac",
		AudioBitrate:  "192k",
		AudioChannels: "keep",
		Container:     "mp4",
		FastStart:     true,
	}
}

// ToOptions 转换为 ffmpeg 层的参数结构。
func (p TranscodeParams) ToOptions(defThreads int) ffmpeg.TranscodeOptions {
	return ffmpeg.TranscodeOptions{
		Codec:         p.Codec,
		Accel:         p.Accel,
		RateControl:   p.RateControl,
		CRF:           p.CRF,
		VideoBitrate:  p.VideoBitrate,
		Maxrate:       p.Maxrate,
		BufSize:       p.BufSize,
		Preset:        p.Preset,
		TwoPass:       p.TwoPass,
		Resolution:    p.Resolution,
		CustomSize:    p.CustomSize,
		FPS:           p.FPS,
		PixelFormat:   p.PixelFormat,
		AudioCodec:    p.AudioCodec,
		AudioBitrate:  p.AudioBitrate,
		AudioChannels: p.AudioChannels,
		Container:     p.Container,
		FastStart:     p.FastStart,
		Start:         p.Start,
		End:           p.End,
		Threads:       defThreads,
	}
}

// Summary 返回参数的人类可读摘要（任务列表 / 输出文件名使用）。
func (p TranscodeParams) Summary() string {
	var parts []string

	codec := strings.ToUpper(p.Codec)
	if p.Accel != "" && p.Accel != "none" {
		codec += "/" + strings.ToUpper(p.Accel)
	}
	parts = append(parts, codec)

	if p.RateControl == ffmpeg.RateBitrate {
		parts = append(parts, strings.TrimSpace(p.VideoBitrate)+"bps")
	} else {
		parts = append(parts, fmt.Sprintf("CRF %d", p.CRF))
	}

	switch {
	case p.Resolution == "keep":
		parts = append(parts, "原始分辨率")
	case p.Resolution == "custom":
		parts = append(parts, p.CustomSize)
	default:
		if r, ok := ffmpeg.ResolutionByID(p.Resolution); ok {
			parts = append(parts, fmt.Sprintf("%d×%d", r.Width, r.Height))
		}
	}
	if p.FPS != "keep" && p.FPS != "" {
		parts = append(parts, p.FPS+"fps")
	}
	if p.AudioCodec == "none" {
		parts = append(parts, "无音轨")
	} else if p.AudioCodec != "copy" && p.AudioCodec != "" {
		parts = append(parts, strings.ToUpper(p.AudioCodec)+" "+p.AudioBitrate)
	}
	return strings.Join(parts, " · ")
}

// Suffix 依据参数推导输出文件名后缀（如 "_1080pH265"），为空表示不加后缀。
func (p TranscodeParams) Suffix() string {
	var tags []string
	switch {
	case p.Resolution == "keep":
	case p.Resolution == "custom":
		tags = append(tags, sanitizeTag(p.CustomSize))
	default:
		if r, ok := ffmpeg.ResolutionByID(p.Resolution); ok {
			tags = append(tags, r.ID)
		}
	}
	switch p.Codec {
	case "h265":
		tags = append(tags, "H265")
	case "vp9":
		tags = append(tags, "VP9")
	case "av1":
		tags = append(tags, "AV1")
	}
	if len(tags) == 0 {
		return ""
	}
	return "_" + strings.Join(tags, "")
}

// sanitizeTag 把自定义尺寸转成可用于文件名的短标签。
func sanitizeTag(s string) string {
	repl := strings.NewReplacer("x", "", "X", "", "*", "", "×", "", " ", "", "　", "")
	return repl.Replace(strings.TrimSpace(s))
}

// ==================== 转码预设模板 ====================

// Profile 转码预设（参数模板）。
type Profile struct {
	// Name 模板名（同名校验唯一）
	Name string `json:"name"`
	// Description 用途说明
	Description string `json:"description,omitempty"`
	// Params 转码参数
	Params TranscodeParams `json:"params"`
	// Builtin 是否为内置模板（不可删除 / 不可覆盖）
	Builtin bool `json:"builtin"`
}

// builtinProfiles 内置转码模板。
func builtinProfiles() []Profile {
	return []Profile{
		{
			Name:        "通用均衡",
			Description: "H.264 + 原始分辨率 + CRF23，兼容性最好，日常转码首选",
			Params: TranscodeParams{
				Codec: "h264", Accel: "auto", RateControl: ffmpeg.RateCRF, CRF: 23,
				Preset: "veryfast", Resolution: "keep", FPS: "keep", PixelFormat: "yuv420p",
				AudioCodec: "aac", AudioBitrate: "192k", AudioChannels: "keep",
				Container: "mp4", FastStart: true,
			},
			Builtin: true,
		},
		{
			Name:        "高画质归档",
			Description: "H.265 + CRF18 + 原始分辨率，同画质体积更小，适合长期保存",
			Params: TranscodeParams{
				Codec: "h265", Accel: "auto", RateControl: ffmpeg.RateCRF, CRF: 18,
				Preset: "slow", Resolution: "keep", FPS: "keep", PixelFormat: "yuv420p",
				AudioCodec: "aac", AudioBitrate: "192k", AudioChannels: "keep",
				Container: "mp4", FastStart: true,
			},
			Builtin: true,
		},
		{
			Name:        "4K 压制",
			Description: "H.265 + 2160p + CRF20，把 4K 素材压到 1080p 设备也能看",
			Params: TranscodeParams{
				Codec: "h265", Accel: "auto", RateControl: ffmpeg.RateCRF, CRF: 20,
				Preset: "medium", Resolution: "2160p", FPS: "keep", PixelFormat: "yuv420p",
				AudioCodec: "aac", AudioBitrate: "192k", AudioChannels: "stereo",
				Container: "mp4", FastStart: true,
			},
			Builtin: true,
		},
		{
			Name:        "极速转码",
			Description: "硬件加速优先，自动使用 NVENC / QSV / VAAPI，无显卡时回退软件 veryfast",
			Params: TranscodeParams{
				Codec: "h264", Accel: "auto", RateControl: ffmpeg.RateBitrate, VideoBitrate: "6M",
				Preset: "veryfast", Resolution: "keep", FPS: "keep", PixelFormat: "yuv420p",
				AudioCodec: "aac", AudioBitrate: "160k", AudioChannels: "keep",
				Container: "mp4", FastStart: true,
			},
			Builtin: true,
		},
		{
			Name:        "体积优先",
			Description: "H.265 + 1080p + CRF28 + 两遍编码，体积压到最小",
			Params: TranscodeParams{
				Codec: "h265", Accel: "auto", RateControl: ffmpeg.RateCRF, CRF: 28,
				Preset: "slow", TwoPass: true, Resolution: "1080p", FPS: "keep", PixelFormat: "yuv420p",
				AudioCodec: "aac", AudioBitrate: "128k", AudioChannels: "stereo",
				Container: "mp4", FastStart: true,
			},
			Builtin: true,
		},
		{
			Name:        "网页 WebM",
			Description: "VP9 + Opus + 1080p，网页 / 流媒体友好",
			Params: TranscodeParams{
				Codec: "vp9", Accel: "auto", RateControl: ffmpeg.RateCRF, CRF: 32,
				Preset: "medium", Resolution: "1080p", FPS: "keep", PixelFormat: "yuv420p",
				AudioCodec: "opus", AudioBitrate: "128k", AudioChannels: "stereo",
				Container: "webm", FastStart: false,
			},
			Builtin: true,
		},
		{
			Name:        "竖屏短视频",
			Description: "H.264 + 1080×1920 + 30fps，适配手机竖屏分发",
			Params: TranscodeParams{
				Codec: "h264", Accel: "auto", RateControl: ffmpeg.RateCRF, CRF: 22,
				Preset: "veryfast", Resolution: "custom", CustomSize: "1080x1920",
				FPS: "30", PixelFormat: "yuv420p",
				AudioCodec: "aac", AudioBitrate: "128k", AudioChannels: "stereo",
				Container: "mp4", FastStart: true,
			},
			Builtin: true,
		},
		{
			Name:        "极速瘦身",
			Description: "H.264 + 720p + 固定 3Mbps，批量上传 / 移动端播放最省流量",
			Params: TranscodeParams{
				Codec: "h264", Accel: "auto", RateControl: ffmpeg.RateBitrate, VideoBitrate: "3M",
				Preset: "veryfast", Resolution: "720p", FPS: "keep", PixelFormat: "yuv420p",
				AudioCodec: "aac", AudioBitrate: "128k", AudioChannels: "stereo",
				Container: "mp4", FastStart: true,
			},
			Builtin: true,
		},
	}
}
