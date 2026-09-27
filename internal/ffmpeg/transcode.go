package ffmpeg

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// ==================== 转码参数 ====================

// 质量控制模式。
const (
	// RateCRF 恒定质量（数值越小画质越高）
	RateCRF = "crf"
	// RateBitrate 目标码率
	RateBitrate = "bitrate"
)

// TranscodeOptions 单个转码任务的完整参数集合。
// 所有字段均由前端表单传入，服务端负责归一化与校验。
type TranscodeOptions struct {
	// Codec 编码族：h264 / h265 / vp9 / av1
	Codec string
	// Accel 硬件加速：none / nvenc / qsv / vaapi（auto 在归一化阶段解析）
	Accel string
	// RateControl 质量控制：crf / bitrate
	RateControl string
	// CRF 恒定质量值
	CRF int
	// VideoBitrate 目标视频码率，如 "4M"
	VideoBitrate string
	// Maxrate 峰值码率上限（留空按目标的 2 倍）
	Maxrate string
	// BufSize 码率缓冲（留空按目标的 2 倍）
	BufSize string
	// Preset 编码预设（软件为 x264/x265 档位，硬件由服务端映射）
	Preset string
	// TwoPass 是否两遍编码（仅软件编码生效）
	TwoPass bool
	// Resolution 分辨率档位：keep / 1080p / custom …
	Resolution string
	// CustomSize 自定义分辨率，如 "1920x1080"
	CustomSize string
	// FPS 帧率：keep / 30 / 23.976 / 任意数字
	FPS string
	// PixelFormat 像素格式：keep / yuv420p / yuv420p10le …
	PixelFormat string
	// requireTwoPass 内部标记：需要挑选支持两遍编码的软件编码器
	requireTwoPass bool
	// AudioCodec 音频编码：aac / opus / mp3 / copy / none
	AudioCodec string
	// AudioBitrate 音频码率，如 "192k"
	AudioBitrate string
	// AudioChannels 声道：keep / stereo / mono
	AudioChannels string
	// Container 输出容器：mp4 / mkv / webm / mov
	Container string
	// FastStart MP4/MOV 是否前置 moov（边下边播）
	FastStart bool
	// Start / End 可选时间区间（秒）；End<=0 或不小于 Start 表示整段
	Start float64
	End   float64
	// Threads 编码线程数，<=0 使用客户端默认
	Threads int
	// VAAPIDevice VAAPI 设备节点
	VAAPIDevice string
	// HWDecode 是否启用硬件解码
	HWDecode bool
	// KnownDuration 已知源时长（秒）。调用方探测过源文件时传入，
	// 可让整段转码也能按时间给出百分比进度；<=0 时由 RunTranscode 自行探测。
	KnownDuration float64

	// passLogFile 两遍编码统计文件前缀（由 RunTranscode 注入）
	passLogFile string
}

// Clamp 归一化所有参数并返回可用于组装命令行的副本。
// auto 加速策略会在此阶段按本机能力解析为具体编码器。
func (o *TranscodeOptions) Clamp(caps *Capabilities, defThreads int) error {
	o.Codec = strings.ToLower(strings.TrimSpace(o.Codec))
	if _, ok := CodecFamilyByID(o.Codec); !ok {
		return fmt.Errorf("不支持的视频编码: %s", o.Codec)
	}

	o.Accel = strings.ToLower(strings.TrimSpace(o.Accel))
	if o.Accel == "" {
		o.Accel = AccelAuto
	}
	switch o.Accel {
	case AccelNone, AccelAuto, AccelNVENC, AccelQSV, AccelVAAPI:
	default:
		return fmt.Errorf("不支持的硬件加速方式: %s", o.Accel)
	}

	o.RateControl = strings.ToLower(strings.TrimSpace(o.RateControl))
	o.requireTwoPass = false
	if o.RateControl == "" {
		o.RateControl = RateCRF
	}
	if o.RateControl != RateCRF && o.RateControl != RateBitrate {
		return fmt.Errorf("不支持的质量控制模式: %s", o.RateControl)
	}

	if o.Preset = strings.ToLower(strings.TrimSpace(o.Preset)); o.Preset == "" {
		o.Preset = "veryfast"
	}

	// 容器
	o.Container = strings.ToLower(strings.TrimSpace(o.Container))
	ct, ok := ContainerByID(o.Container)
	if !ok {
		o.Container = "mp4"
		ct, _ = ContainerByID("mp4")
	}
	if !containsStr(ct.VideoCodecs, o.Codec) {
		return fmt.Errorf("%s 容器不支持 %s 编码，请更换输出容器", ct.Name, strings.ToUpper(o.Codec))
	}
	if o.FastStart && !ct.FastStart {
		o.FastStart = false
	}

	// 音频
	o.AudioCodec = strings.ToLower(strings.TrimSpace(o.AudioCodec))
	if o.AudioCodec == "" {
		o.AudioCodec = "aac"
	}
	if o.AudioCodec != "copy" && o.AudioCodec != "none" && AudioEncoderName(o.AudioCodec) == "" {
		return fmt.Errorf("不支持的音频编码: %s", o.AudioCodec)
	}
	// copy 也要校验：源音轨编码未知，容器不支持的音频格式直接放进去必然失败
	if o.AudioCodec != "none" && !containsStr(ct.AudioCodecs, o.AudioCodec) {
		return fmt.Errorf("%s 容器不支持 %s 音频，请更换输出容器或音频编码", ct.Name, strings.ToUpper(o.AudioCodec))
	}
	if o.AudioBitrate = strings.ToLower(strings.TrimSpace(o.AudioBitrate)); o.AudioBitrate == "" {
		o.AudioBitrate = "192k"
	}
	if _, err := ParseBitrate(o.AudioBitrate); err != nil {
		return fmt.Errorf("音频码率无效: %s", err)
	}
	o.AudioChannels = strings.ToLower(strings.TrimSpace(o.AudioChannels))
	if o.AudioChannels == "" {
		o.AudioChannels = "keep"
	}

	// 像素格式
	pf, ok := NormalizePixelFormat(o.PixelFormat)
	if !ok {
		return fmt.Errorf("不支持的像素格式: %s", o.PixelFormat)
	}
	o.PixelFormat = pf

	// 分辨率
	o.Resolution = strings.ToLower(strings.TrimSpace(o.Resolution))
	if o.Resolution == "" {
		o.Resolution = "keep"
	}
	if o.Resolution == "custom" {
		if _, _, err := ParseCustomResolution(o.CustomSize); err != nil {
			return err
		}
	} else if _, ok := ResolutionByID(o.Resolution); !ok {
		o.Resolution = "keep"
	}

	// 帧率
	o.FPS = strings.ToLower(strings.TrimSpace(o.FPS))
	if o.FPS == "" {
		o.FPS = "keep"
	}
	if _, ok := FPSValue(o.FPS); !ok {
		return fmt.Errorf("不支持的帧率: %s", o.FPS)
	}

	// 时间区间
	if o.Start < 0 {
		o.Start = 0
	}
	if o.End > 0 && o.End <= o.Start {
		return fmt.Errorf("结束时间必须大于起始时间")
	}

	// 线程
	if o.Threads <= 0 {
		o.Threads = defThreads
	}

	if o.VAAPIDevice == "" {
		o.VAAPIDevice = "/dev/dri/renderD128"
	}

	// 硬件路径的能力收敛
	if o.Accel != AccelNone && o.Accel != AccelAuto {
		if caps != nil && !accelAvailable(caps, o.Accel) {
			return fmt.Errorf("硬件加速 %s 在当前环境不可用，请改用其他加速方式", strings.ToUpper(o.Accel))
		}
	}
	if o.Accel == AccelNone {
		o.HWDecode = false
	}

	// 两遍编码：仅软件路径、且编码器本身支持时可用
	if o.TwoPass {
		o.TwoPass = o.Accel == AccelNone || o.Accel == AccelAuto
	}
	o.requireTwoPass = o.TwoPass
	if o.TwoPass && o.RateControl == RateCRF {
		// x264/x265/VP9/AV1 都拒绝 CRF + 2pass
		// （"CRF/CQP is incompatible with 2pass"），自动改为码率模式
		o.RateControl = RateBitrate
		if strings.TrimSpace(o.VideoBitrate) == "" {
			w, h := 0, 0
			if r, ok := ResolutionByID(o.Resolution); ok {
				w, h = r.Width, r.Height
			}
			fps, _ := FPSValue(o.FPS)
			o.VideoBitrate = strconv.FormatInt(SuggestBitrate(w, h, fps)/1000, 10) + "k"
		}
	}
	// 码率模式才需要码率
	if o.RateControl == RateCRF {
		o.VideoBitrate = ""
	} else {
		if _, err := ParseBitrate(o.VideoBitrate); err != nil {
			return fmt.Errorf("视频码率无效: %s", err)
		}
		if strings.TrimSpace(o.VideoBitrate) == "" {
			return fmt.Errorf("码率模式下必须指定视频码率")
		}
	}
	return nil
}

// Duration 返回时间区间时长（秒）；未设置区间时返回 0。
func (o *TranscodeOptions) Duration() float64 {
	if o.End > 0 && o.End > o.Start {
		return o.End - o.Start
	}
	return 0
}

// EncoderName 解析实际使用的 ffmpeg 编码器名。
func (o *TranscodeOptions) EncoderName(caps *Capabilities) (string, error) {
	family, ok := CodecFamilyByID(o.Codec)
	if !ok {
		return "", fmt.Errorf("不支持的视频编码: %s", o.Codec)
	}
	// 硬件路径
	if o.Accel != AccelNone && o.Accel != AccelAuto {
		if enc, ok := family.HWEncoders[o.Accel]; ok {
			if caps == nil || containsStr(caps.Encoders, enc) {
				return enc, nil
			}
		}
	}
	// 软件路径：取第一个存在的
	for _, enc := range family.SoftwareEncoders {
		if o.requireTwoPass && !supportsTwoPass(enc) {
			continue
		}
		if caps == nil || containsStr(caps.Encoders, enc) {
			return enc, nil
		}
	}
	if o.requireTwoPass && !supportsTwoPass(family.SoftwareEncoders[0]) {
		return "", fmt.Errorf("%s 没有可用于双遍编码的软件编码器", strings.ToUpper(o.Codec))
	}
	return family.SoftwareEncoders[0], nil
}

// supportsTwoPass 报告编码器是否支持 ffmpeg 的 -pass 两遍编码。
// libsvtav1 等编码器没有两遍模式，强行使用会直接报错。
func supportsTwoPass(encoder string) bool {
	switch {
	case strings.HasPrefix(encoder, "libx264"),
		strings.HasPrefix(encoder, "libx265"),
		strings.HasPrefix(encoder, "libvpx"),
		encoder == "libaom-av1",
		encoder == "mpeg4",
		encoder == "mpeg2video",
		encoder == "libtheora",
		encoder == "mjpeg",
		encoder == "ffv1",
		encoder == "huffyuv":
		return true
	}
	return false
}

// ActualAccel 返回归一化后的实际加速方式（auto 已解析为具体值）。
func (o *TranscodeOptions) ActualAccel(caps *Capabilities) string {
	if o.Accel != AccelAuto {
		return o.Accel
	}
	family, ok := CodecFamilyByID(o.Codec)
	if !ok {
		return AccelNone
	}
	// auto：优先 NVENC → QSV → VAAPI → 软件
	for _, accel := range []string{AccelNVENC, AccelQSV, AccelVAAPI} {
		enc, ok := family.HWEncoders[accel]
		if !ok {
			continue
		}
		if caps == nil || (accelAvailable(caps, accel) && containsStr(caps.Encoders, enc)) {
			return accel
		}
	}
	return AccelNone
}

// Estimate 输出体积预估。
type Estimate struct {
	// OutputSize 预估输出体积（字节）
	OutputSize int64 `json:"output_size"`
	// SourceSize 源文件体积（字节）
	SourceSize int64 `json:"source_size"`
	// Ratio 体积占比（输出 / 源）
	Ratio float64 `json:"ratio"`
	// Duration 参与预估的时长（秒）
	Duration float64 `json:"duration"`
	// Note 预估说明
	Note string `json:"note"`
}

// EstimateSize 依据参数粗估输出体积。CRF 模式按分辨率档位的经验系数折算。
func EstimateSize(opts *TranscodeOptions, info *MediaInfo) Estimate {
	est := Estimate{}
	if info == nil {
		return est
	}
	est.SourceSize = info.Size
	est.Duration = info.Duration
	if opts.Duration() > 0 {
		est.Duration = opts.Duration()
	}
	if est.Duration <= 0 || info.Size <= 0 {
		return est
	}

	srcW, srcH := 0, 0
	if info.Video != nil {
		srcW, srcH = info.Video.Width, info.Video.Height
	}
	outW, outH := srcW, srcH
	switch {
	case opts.Resolution == "keep":
	case opts.Resolution == "custom":
		if w, h, err := ParseCustomResolution(opts.CustomSize); err == nil {
			outW, outH = w, h
		}
	default:
		if r, ok := ResolutionByID(opts.Resolution); ok {
			outW, outH = r.Width, r.Height
		}
	}
	// 帧率折算
	fpsFactor := 1.0
	if fv, ok := FPSValue(opts.FPS); ok && fv > 0 {
		srcFPS := 0.0
		if info.Video != nil {
			srcFPS = info.Video.FrameRate
		}
		if srcFPS > 0 {
			fpsFactor = fv / srcFPS
			if fpsFactor < 0.2 {
				fpsFactor = 0.2
			}
			if fpsFactor > 4 {
				fpsFactor = 4
			}
		}
	}
	// 像素折算
	pixelFactor := 1.0
	if srcW > 0 && srcH > 0 && outW > 0 && outH > 0 {
		pixelFactor = float64(outW*outH) / float64(srcW*srcH)
	}

	var bps int64
	switch opts.RateControl {
	case RateBitrate:
		bps, _ = ParseBitrate(opts.VideoBitrate)
		est.Note = "按目标码率估算"
	default:
		// CRF 模式：以源码率为基准，按像素/帧率缩放 + 编码效率系数
		srcBps := info.BitRate
		if info.Audio != nil && info.Audio.BitRate > 0 {
			srcBps -= info.Audio.BitRate
		}
		if srcBps <= 0 {
			srcBps = SuggestBitrate(outW, outH, 30)
		}
		family, _ := CodecFamilyByID(opts.Codec)
		// 编码效率：H.265/AV1 同码率画质更高 → 输出可更小
		gain := 1.0
		switch opts.Codec {
		case "h265":
			gain = 0.65
		case "vp9":
			gain = 0.70
		case "av1":
			gain = 0.55
		}
		// CRF 值越高质量越好、体积越大
		maxQ := float64(family.MaxQuality)
		if maxQ <= 0 {
			maxQ = 51
		}
		crf := float64(opts.CRF)
		if crf <= 0 {
			crf = 23
		}
		crfFactor := 0.45 + 0.75*(crf/maxQ)
		bps = int64(float64(srcBps) * pixelFactor * fpsFactor * gain * crfFactor)
		est.Note = "按恒定质量粗估"
	}
	// 音频
	aBps, _ := ParseBitrate(opts.AudioBitrate)
	if opts.AudioCodec == "none" {
		aBps = 0
	}
	total := bps + aBps
	est.OutputSize = int64(float64(total) * est.Duration / 8)
	if info.Size > 0 {
		est.Ratio = float64(est.OutputSize) / float64(info.Size)
	}
	return est
}

// ==================== 命令行组装 ====================

// PreviewCommand 返回本次转码实际使用的 ffmpeg 参数（两遍编码时取第二遍），
// 归一化失败时返回 nil。仅用于界面排障展示，不参与执行。
func (c *Client) PreviewCommand(ctx context.Context, input, output string, o *TranscodeOptions) []string {
	caps := c.Detect(ctx)
	opts := *o
	if err := opts.Clamp(caps, c.threads); err != nil {
		return nil
	}
	opts.passLogFile = filepath.Join(filepath.Dir(output), "."+filepath.Base(output)+".pass")
	return c.buildTranscodeArgs(&opts, caps, input, output, 2)
}

// buildTranscodeArgs 组装单趟转码命令。
// -ss 放在 -i 前（输入快速 seek），-t 控制时长。
func (c *Client) buildTranscodeArgs(o *TranscodeOptions, caps *Capabilities, input, output string, pass int) []string {
	encoder, _ := o.EncoderName(caps)
	accel := o.ActualAccel(caps)
	isSoftware := strings.HasPrefix(encoder, "lib")
	software := isSoftware || accel == AccelNone

	var args []string
	args = append(args, "-hide_banner", "-y")

	// 硬件设备
	if accel == AccelVAAPI {
		args = append(args, "-vaapi_device", o.VAAPIDevice)
	}
	// 硬件解码（可选）
	if o.HWDecode && accel != AccelNone {
		switch accel {
		case AccelNVENC:
			args = append(args, "-hwaccel", "cuda", "-hwaccel_output_format", "cuda")
		case AccelQSV:
			args = append(args, "-hwaccel", "qsv")
		case AccelVAAPI:
			args = append(args, "-hwaccel", "vaapi", "-hwaccel_device", o.VAAPIDevice, "-hwaccel_output_format", "vaapi")
		}
	}
	// 输入端裁剪
	if o.Start > 0 {
		args = append(args, "-ss", formatSeconds(o.Start))
	}
	args = append(args, "-i", input)
	if d := o.Duration(); d > 0 {
		args = append(args, "-t", formatSeconds(d))
	}

	// 流映射：只取首个视频流与全部音频流（排除内嵌封面）
	if pass == 2 {
		args = append(args, "-map", "0:v:0")
		if o.AudioCodec == "none" {
			args = append(args, "-an")
		} else {
			args = append(args, "-map", "0:a?")
		}
		args = append(args, "-sn", "-dn")
	}

	// 视频滤镜
	filters := o.buildFilters()
	if len(filters) > 0 {
		args = append(args, "-vf", strings.Join(filters, ","))
	}

	// 视频编码器
	args = append(args, "-c:v", encoder)

	// 像素格式：两遍编码必须完全一致，否则第二遍会被编码器拒绝
	// （x264 报 "different weightp setting than first pass"）。
	// VAAPI 的帧常驻显存、由 hwupload 转换，不接受软件 -pix_fmt。
	if o.PixelFormat != "" && accel != AccelVAAPI {
		args = append(args, "-pix_fmt", o.PixelFormat)
	}

	encoderArgs, audioArgs := o.buildRateArgs(encoder, accel, software, pass)
	args = append(args, encoderArgs...)

	// 线程
	if software && o.Threads > 0 && !strings.HasSuffix(encoder, "_vaapi") {
		args = append(args, "-threads", strconv.Itoa(o.Threads))
	}
	// VAAPI 需要把帧上传到渲染设备
	if accel == AccelVAAPI && !o.HWDecode {
		args = append(args, "-filter_complex_threads", "1")
	}

	// 音频
	args = append(args, audioArgs...)

	// 两遍编码参数：只有启用两遍编码时才输出，否则单遍编码会去找不存在的统计文件
	if o.TwoPass {
		args = append(args, "-pass", strconv.Itoa(pass), "-passlogfile", o.passLogFile)
	}

	// 容器附加（第一遍输出为 null，无需附加容器参数）
	if pass != 1 {
		args = append(args, containerArgs(o)...)
	}

	if pass == 1 {
		// 第一遍：丢弃输出，只收集统计信息
		args = append(args, "-f", "null", "-")
	} else {
		args = append(args, output)
	}
	return args
}

// buildFilters 组装视频滤镜链（缩放 / 帧率 / 像素格式 / VAAPI 上传）。
func (o *TranscodeOptions) buildFilters() []string {
	var filters []string
	if o.Resolution != "keep" {
		w, h := 0, 0
		switch {
		case o.Resolution == "custom":
			w, h, _ = ParseCustomResolution(o.CustomSize)
		default:
			if r, ok := ResolutionByID(o.Resolution); ok {
				w, h = r.Width, r.Height
			}
		}
		if w > 0 && h > 0 {
			// 保持原比例缩放并对齐到偶数，避免播放器解码异常
			filters = append(filters, fmt.Sprintf(
				"scale=w=%d:h=%d:force_original_aspect_ratio=decrease:force_divisible_by=2",
				w, h))
		}
	}
	if fv, ok := FPSValue(o.FPS); ok && fv > 0 {
		filters = append(filters, fmt.Sprintf("fps=%s", trimFloat(fv)))
	}
	if o.Accel != AccelNone {
		switch o.Accel {
		case AccelVAAPI:
			if !o.HWDecode {
				// 软件帧 → nv12 → 上传到 VAAPI 设备
				filters = append(filters, "format=nv12", "hwupload")
			}
		case AccelQSV:
			filters = append(filters, "format=nv12")
		}
	}
	return filters
}

// encoderSettingArgs 返回编码器档位类参数（preset / cpu-used 等）。
// 两遍编码时两遍必须完全一致：x264 会把设置写入统计文件，
// 第二遍一旦设置不同就会报 "different weightp setting than first pass"。
func (o *TranscodeOptions) encoderSettingArgs(encoder, accel string) []string {
	var args []string
	switch {
	case accel == AccelNVENC:
		args = append(args, "-preset", nvencPreset(o.Preset))
	case encoder == "libsvtav1":
		args = append(args, "-preset", svtPreset(o.Preset), "-svtav1-params", "fast=1")
	case encoder == "libaom-av1":
		args = append(args, "-cpu-used", aomCPUUsed(o.Preset), "-row-mt", "1")
	case strings.HasPrefix(encoder, "libvpx"):
		args = append(args, "-deadline", "good", "-cpu-used", vpxCPUUsed(o.Preset), "-row-mt", "1", "-tile-columns", "2")
	case accel == AccelQSV, accel == AccelVAAPI:
		// QSV / VAAPI 不接受 x264 档位，质量完全由 global_quality / -b:v 决定
	default:
		// libx264 / libx265 等软件编码器
		args = append(args, "-preset", o.Preset)
	}
	return args
}

// buildRateArgs 返回视频编码质量参数与音频编码参数。
func (o *TranscodeOptions) buildRateArgs(encoder, accel string, software bool, pass int) (video, audio []string) {
	video = append(video, o.encoderSettingArgs(encoder, accel)...)

	if pass == 1 {
		// 第一遍只做分析：无音频、无字幕，输出丢弃
		return append(video, "-an", "-sn"), nil
	}

	switch {
	case accel == AccelNVENC:
		if o.RateControl == RateCRF {
			video = append(video, "-rc", "vbr", "-cq", strconv.Itoa(clampInt(o.CRF, 0, 51)), "-b:v", "0")
		} else {
			b, _ := ParseBitrate(o.VideoBitrate)
			video = append(video, "-rc", "vbr", "-b:v", strconv.FormatInt(b, 10))
		}
	case accel == AccelQSV:
		if o.RateControl == RateCRF {
			video = append(video, "-global_quality", strconv.Itoa(clampInt(o.CRF, 0, 51)), "-b:v", "0")
		} else {
			b, _ := ParseBitrate(o.VideoBitrate)
			video = append(video, "-b:v", strconv.FormatInt(b, 10))
		}
		video = append(video, "-look_ahead", "0")
	case accel == AccelVAAPI:
		// VAAPI 编码器不支持 CRF，统一走码率模式
		b, _ := ParseBitrate(o.VideoBitrate)
		if b <= 0 {
			b = SuggestBitrate(0, 0, 0)
		}
		video = append(video, "-b:v", strconv.FormatInt(b, 10))
	case encoder == "libsvtav1":
		if o.RateControl == RateCRF {
			video = append(video, "-crf", strconv.Itoa(clampInt(o.CRF, 0, 63)))
		} else {
			b, _ := ParseBitrate(o.VideoBitrate)
			video = append(video, "-b:v", strconv.FormatInt(b, 10))
		}
	case encoder == "libaom-av1":
		if o.RateControl == RateCRF {
			video = append(video, "-crf", strconv.Itoa(clampInt(o.CRF, 0, 63)), "-b:v", "0")
		} else {
			b, _ := ParseBitrate(o.VideoBitrate)
			video = append(video, "-b:v", strconv.FormatInt(b, 10))
		}
	case strings.HasPrefix(encoder, "libvpx"):
		if o.RateControl == RateCRF {
			video = append(video, "-crf", strconv.Itoa(clampInt(o.CRF, 0, 63)), "-b:v", "0")
		} else {
			b, _ := ParseBitrate(o.VideoBitrate)
			video = append(video, "-b:v", strconv.FormatInt(b, 10))
		}
	default:
		// libx264 / libx265 等
		if o.RateControl == RateCRF {
			video = append(video, "-crf", strconv.Itoa(clampInt(o.CRF, 0, 51)))
		} else {
			b, _ := ParseBitrate(o.VideoBitrate)
			video = append(video, "-b:v", strconv.FormatInt(b, 10))
			// VBV 约束，提升码率可控性
			max := o.Maxrate
			if max == "" {
				max = strconv.FormatInt(b*2, 10)
			}
			buf := o.BufSize
			if buf == "" {
				buf = strconv.FormatInt(b*2, 10)
			}
			video = append(video, "-maxrate", max, "-bufsize", buf)
		}
	}

	// 音频
	switch o.AudioCodec {
	case "none":
		audio = append(audio, "-an")
	case "copy":
		audio = append(audio, "-c:a", "copy")
	default:
		audio = append(audio, "-c:a", AudioEncoderName(o.AudioCodec), "-b:a", o.AudioBitrate)
		if ch := ChannelValue(o.AudioChannels); ch > 0 {
			audio = append(audio, "-ac", strconv.Itoa(ch))
		}
	}
	return video, audio
}

// containerArgs 返回与容器相关的附加参数。
func containerArgs(o *TranscodeOptions) []string {
	ct, ok := ContainerByID(o.Container)
	if !ok {
		return nil
	}
	if ct.FastStart && o.FastStart {
		return []string{"-movflags", "+faststart"}
	}
	return nil
}

// ==================== 进度 ====================

// Progress 转码进度快照。
type Progress struct {
	// Percent 完成比例 0~1
	Percent float64
	// Seconds 已编码秒数
	Seconds float64
	// Frame 已编码帧数
	Frame int64
	// FPS 实时编码帧率
	FPS float64
	// Speed 实时速度（如 "12.34x"）
	Speed string
	// Bitrate 当前输出码率
	Bitrate string
	// Size 当前已写入字节数
	Size int64
	// Quality 编码器质量指标（部分编码器输出）
	Quality string
	// Pass 当前是第几遍（0 = 单遍）
	Pass int
}

// ProgressFunc 进度回调（由 ffmpeg 输出线程调用）。
type ProgressFunc func(Progress)

// ==================== 执行 ====================

// RunTranscode 执行一次转码，并实时回调进度。
// ctx 取消会终止 ffmpeg 进程并清理未完成的输出文件。
func (c *Client) RunTranscode(ctx context.Context, input, output string, o *TranscodeOptions, progress ProgressFunc) error {
	caps := c.Detect(ctx)

	// 归一化：自动探测硬件加速、校验参数
	opts := *o
	if err := opts.Clamp(caps, c.threads); err != nil {
		return err
	}
	// 注入两遍编码统计文件前缀（与输出同目录，避免跨文件系统）
	if opts.TwoPass {
		opts.passLogFile = filepath.Join(filepath.Dir(output), "."+filepath.Base(output)+".pass")
	} else {
		opts.passLogFile = filepath.Join(filepath.Dir(output), "pass")
	}
	// 无论成功、失败还是被取消，两遍编码的统计文件都不能留在用户目录里
	defer os.Remove(opts.passLogFile)
	defer os.Remove(opts.passLogFile + "-0.log")
	defer os.Remove(opts.passLogFile + "-0.log.mbtree")

	cleanup := func() {
		// 未完成的输出无价值，直接删除，避免占用磁盘
		_ = os.Remove(output)
	}

	// 进度总量：区间优先，其次调用方已知时长，最后现探测一次源文件。
	// 拿不到总量时 ffmpeg 只回调秒数，百分比会停在 0。
	total := opts.Duration()
	if total <= 0 {
		total = opts.KnownDuration
	}
	if total <= 0 {
		if info, err := c.Probe(ctx, input); err == nil {
			total = info.Duration
		}
	}

	var last Progress
	// track 把某一趟的进度映射到 [from, to] 区间，并记录最后一帧快照。
	track := func(from, to float64) func(Progress) {
		return func(p Progress) {
			last = p
			emit(progress, p, from, to)
		}
	}

	if opts.TwoPass {
		// 第一遍：分析（占用前 35% 进度）
		args := c.buildTranscodeArgs(&opts, caps, input, output, 1)
		first := track(0, 0.35)
		if err := c.runPass(ctx, args, total, func(p Progress) {
			p.Pass = 1
			first(p)
		}); err != nil {
			cleanup()
			return err
		}
	}

	args := c.buildTranscodeArgs(&opts, caps, input, output, 2)
	from := 0.0
	if opts.TwoPass {
		from = 0.35
	}
	second := track(from, 1)
	if err := c.runPass(ctx, args, total, func(p Progress) {
		p.Pass = 0
		second(p)
	}); err != nil {
		cleanup()
		return err
	}
	// 收尾：补一次 100%，同时保留最后一帧的速度 / 码率等指标
	emit(progress, last, 1, 1)
	return nil
}

// emit 按 [from, to] 区间缩放百分比后回调（其余字段原样透传）。
func emit(f ProgressFunc, p Progress, from, to float64) {
	if f == nil {
		return
	}
	ratio := p.Percent
	if ratio < 0 {
		ratio = 0
	}
	if ratio > 1 {
		ratio = 1
	}
	p.Percent = from + ratio*(to-from)
	f(p)
}

// runPass 执行一趟 ffmpeg 命令并解析 -progress 输出。
func (c *Client) runPass(ctx context.Context, args []string, duration float64, onProgress func(Progress)) error {
	full := append([]string{"-progress", "pipe:1", "-nostats"}, args...)
	cmd := exec.CommandContext(ctx, c.ffmpegBin, full...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 ffmpeg 失败: %w", err)
	}

	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), 256*1024)
		var cur Progress
		reset := func() {
			cur = Progress{}
		}
		for sc.Scan() {
			key, val, ok := strings.Cut(sc.Text(), "=")
			if !ok {
				continue
			}
			switch key {
			case "out_time_us", "out_time_ms":
				// ffmpeg 的 out_time_us / out_time_ms 单位均为微秒
				us, err := strconv.ParseInt(strings.TrimSpace(val), 10, 64)
				if err != nil {
					continue
				}
				cur.Seconds = float64(us) / 1_000_000
				if duration > 0 {
					cur.Percent = clampFloat(cur.Seconds/duration, 0, 1)
				}
			case "frame":
				cur.Frame, _ = strconv.ParseInt(strings.TrimSpace(val), 10, 64)
			case "fps":
				cur.FPS = parseFloat(val)
			case "speed":
				cur.Speed = strings.TrimSpace(val)
			case "total_size":
				cur.Size, _ = strconv.ParseInt(strings.TrimSpace(val), 10, 64)
			case "bitrate":
				cur.Bitrate = strings.TrimSpace(val)
			case "progress":
				if strings.TrimSpace(val) == "end" {
					cur.Percent = 1
					onProgress(cur)
					reset()
					continue
				}
			}
			if cur.Percent > 0 || cur.Size > 0 {
				onProgress(cur)
			}
		}
	}()

	errBuf := &limitedBuffer{max: 64 * 1024}
	go func() { _, _ = io.Copy(errBuf, stderr) }()

	runErr := cmd.Wait()
	if ctx.Err() != nil {
		return context.Canceled
	}
	if runErr != nil {
		return fmt.Errorf("ffmpeg 转码失败: %v（%s）", runErr, summarizeErr(errBuf.String()))
	}
	return nil
}

// summarizeErr 压缩 ffmpeg 错误输出，只保留末尾关键行。
func summarizeErr(msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return "无错误输出"
	}
	lines := strings.Split(msg, "\n")
	// 优先保留含 Error / Invalid / No such 的行
	var key []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		lower := strings.ToLower(l)
		if strings.Contains(lower, "error") || strings.Contains(lower, "invalid") ||
			strings.Contains(lower, "no such") || strings.Contains(lower, "failed") ||
			strings.Contains(lower, "unknown") || strings.Contains(lower, "unsupported") {
			key = append(key, l)
		}
	}
	if len(key) == 0 {
		key = lines
	}
	if len(key) > 4 {
		key = key[len(key)-4:]
	}
	out := strings.Join(key, " | ")
	if len(out) > 480 {
		out = out[len(out)-480:]
	}
	return out
}

// ==================== 工具函数 ====================

func containsStr(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func formatSeconds(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }

func trimFloat(v float64) string {
	s := strconv.FormatFloat(v, 'f', 3, 64)
	s = strings.TrimRight(s, "0")
	return strings.TrimRight(s, ".")
}

// nvencPreset 将 x264/x265 档位映射到 NVENC 的 p1~p7。
func nvencPreset(p string) string {
	switch p {
	case "ultrafast", "superfast":
		return "p1"
	case "veryfast":
		return "p2"
	case "faster":
		return "p3"
	case "fast":
		return "p4"
	case "slow":
		return "p6"
	case "slower", "veryslow":
		return "p7"
	default:
		return "p5"
	}
}

// svtPreset 将 x264 档位映射到 libsvtav1 的 0~13 档位。
func svtPreset(p string) string {
	switch p {
	case "ultrafast":
		return "13"
	case "superfast":
		return "12"
	case "veryfast":
		return "10"
	case "faster":
		return "8"
	case "fast":
		return "6"
	case "slow":
		return "3"
	case "slower":
		return "2"
	case "veryslow":
		return "0"
	default:
		return "4"
	}
}

// aomCPUUsed 将 x264 档位映射到 libaom 的 cpu-used 0~8。
func aomCPUUsed(p string) string {
	switch p {
	case "ultrafast":
		return "8"
	case "superfast":
		return "7"
	case "veryfast":
		return "6"
	case "faster":
		return "5"
	case "fast":
		return "4"
	case "slow":
		return "2"
	case "slower":
		return "1"
	case "veryslow":
		return "0"
	default:
		return "3"
	}
}

// vpxCPUUsed 将 x264 档位映射到 libvpx 的 cpu-used 0~8。
func vpxCPUUsed(p string) string { return aomCPUUsed(p) }

// accelAvailable 判断某种硬件加速在本机是否可用。
func accelAvailable(caps *Capabilities, id string) bool {
	if caps == nil {
		return false
	}
	for _, a := range caps.HWAccel {
		if a.ID == id {
			return a.Available
		}
	}
	return false
}
