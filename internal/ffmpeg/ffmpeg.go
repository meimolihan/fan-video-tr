// Package ffmpeg 封装对 FFmpeg / FFprobe 的调用，提供媒体探测、编码器能力
// 探测、硬件加速检测、视频转码与进度解析等能力。FFmpeg 通过内置路径或配置
// 指定，调用方需保证环境中存在 ffmpeg / ffprobe 可执行文件。
package ffmpeg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DetectError 表示未找到可执行文件或当前环境无法运行 FFmpeg。
type DetectError struct{ bin string }

func (e *DetectError) Error() string {
	return fmt.Sprintf("未找到可执行文件 %q，请安装 ffmpeg 或在配置中指定 ffmpeg.path / ffmpeg.ffprobe_path", e.bin)
}

// Client FFmpeg 调用客户端。
type Client struct {
	ffmpegBin    string
	ffprobeBin   string
	threads      int
	preset       string
	crf          int
	audioBitrate string
	accel        string
	vaapiDevice  string
	hwDecode     bool

	capOnce sync.Once
	caps    *Capabilities
}

// Options FFmpeg 客户端配置项。
type Options struct {
	FFmpegBin    string
	FFprobeBin   string
	Threads      int
	Preset       string
	CRF          int
	AudioBitrate string
	// Accel 默认硬件加速策略：auto / none / nvenc / qsv / vaapi
	Accel string
	// VAAPIDevice VAAPI 渲染设备节点
	VAAPIDevice string
	// HWDecode 是否启用硬件解码
	HWDecode bool
}

// New 创建 FFmpeg 客户端，并校验可执行文件存在。
func New(opts Options) (*Client, error) {
	ffmpegBin := opts.FFmpegBin
	if ffmpegBin == "" {
		ffmpegBin = "ffmpeg"
	}
	ffprobeBin := opts.FFprobeBin
	if ffprobeBin == "" {
		ffprobeBin = "ffprobe"
	}
	if _, err := exec.LookPath(ffmpegBin); err != nil {
		return nil, &DetectError{bin: ffmpegBin}
	}
	if _, err := exec.LookPath(ffprobeBin); err != nil {
		return nil, &DetectError{bin: ffprobeBin}
	}
	preset := opts.Preset
	if preset == "" {
		preset = "veryfast"
	}
	crf := opts.CRF
	if crf <= 0 {
		crf = 23
	}
	bitrate := opts.AudioBitrate
	if bitrate == "" {
		bitrate = "192k"
	}
	accel := strings.ToLower(strings.TrimSpace(opts.Accel))
	if accel == "" {
		accel = AccelAuto
	}
	device := opts.VAAPIDevice
	if device == "" {
		device = "/dev/dri/renderD128"
	}
	return &Client{
		ffmpegBin:    ffmpegBin,
		ffprobeBin:   ffprobeBin,
		threads:      opts.Threads,
		preset:       preset,
		crf:          crf,
		audioBitrate: bitrate,
		accel:        accel,
		vaapiDevice:  device,
		hwDecode:     opts.HWDecode,
	}, nil
}

// FFmpegBin 返回 ffmpeg 可执行文件路径。
func (c *Client) FFmpegBin() string { return c.ffmpegBin }

// FFprobeBin 返回 ffprobe 可执行文件路径。
func (c *Client) FFprobeBin() string { return c.ffprobeBin }

// DefaultThreads 返回客户端默认线程配置（0 表示交由 ffmpeg 自动探测）。
func (c *Client) DefaultThreads() int { return c.threads }

// DefaultAccel 返回客户端默认硬件加速策略。
func (c *Client) DefaultAccel() string { return c.accel }

// ==================== 媒体探测 ====================

// MediaStream 媒体流信息（保留转码参数面板需要的字段）。
type MediaStream struct {
	Index     int    `json:"index"`
	CodecType string `json:"codec_type"`
	CodecName string `json:"codec_name"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	// Duration 单流时长（秒）
	Duration float64 `json:"duration"`
	BitRate  int64   `json:"bit_rate"`
	// FrameRate 平均帧率（fps）
	FrameRate float64 `json:"frame_rate"`
	// SampleAspect 像素宽高比（用于判断方形像素）
	SampleAspect string `json:"sample_aspect_ratio"`
	// PixelAspect 由 SAR/DAR 推算的显示宽高比
	PixelAspect float64 `json:"pixel_aspect_ratio"`
	PixFmt      string  `json:"pix_fmt"`
	Profile     string  `json:"profile"`
	Level       int     `json:"level"`
	// SampleRate 音频采样率（Hz）
	SampleRate int `json:"sample_rate"`
	// Channels 音频声道数
	Channels int `json:"channels"`
	// ChannelLayout 音频声道布局
	ChannelLayout string `json:"channel_layout"`
	// IsAttachedPic 是否为内嵌封面（attached_pic）流。
	IsAttachedPic bool `json:"is_attached_pic"`
}

// MediaInfo 媒体文件探测结果。
type MediaInfo struct {
	Path       string        `json:"path"`
	Name       string        `json:"name"`
	Duration   float64       `json:"duration"`
	Size       int64         `json:"size"`
	BitRate    int64         `json:"bit_rate"`
	FormatName string        `json:"format_name"`
	Streams    []MediaStream `json:"streams"`
	// Video 首个视频流（可能为 nil）
	Video *MediaStream `json:"video"`
	// Audio 首个音频流（可能为 nil）
	Audio *MediaStream `json:"audio"`
}

// HasVideo 判断是否包含真实视频流（排除内嵌封面）。
func (m *MediaInfo) HasVideo() bool {
	return m != nil && m.Video != nil
}

type ffprobeFormat struct {
	Duration   string `json:"duration"`
	Size       string `json:"size"`
	BitRate    string `json:"bit_rate"`
	FormatName string `json:"format_name"`
	NbStreams  int    `json:"nb_streams"`
}

type ffprobeOutput struct {
	Format  ffprobeFormat   `json:"format"`
	Streams []ffprobeStream `json:"streams"`
}

type ffprobeStream struct {
	Index         int               `json:"index"`
	CodecType     string            `json:"codec_type"`
	CodecName     string            `json:"codec_name"`
	Width         int               `json:"width"`
	Height        int               `json:"height"`
	CodedWidth    int               `json:"coded_width"`
	CodedHeight   int               `json:"coded_height"`
	Duration      string            `json:"duration"`
	BitRate       string            `json:"bit_rate"`
	FrameRate     string            `json:"avg_frame_rate"`
	RFrameRate    string            `json:"r_frame_rate"`
	SampleAspect  string            `json:"sample_aspect_ratio"`
	DisplayAspect string            `json:"display_aspect_ratio"`
	PixFmt        string            `json:"pix_fmt"`
	Profile       string            `json:"profile"`
	Level         int               `json:"level"`
	SampleRate    string            `json:"sample_rate"`
	Channels      int               `json:"channels"`
	ChannelLayout string            `json:"channel_layout"`
	Disposition   map[string]int    `json:"disposition"`
	Tags          map[string]string `json:"tags"`
}

// Probe 使用 ffprobe 探测媒体文件信息。
func (c *Client) Probe(ctx context.Context, path string) (*MediaInfo, error) {
	args := []string{
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		path,
	}
	cmd := exec.CommandContext(ctx, c.ffprobeBin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("ffprobe 探测 %s 失败: %s", path, msg)
	}

	var out ffprobeOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return nil, fmt.Errorf("解析 ffprobe 输出失败: %w", err)
	}

	info := &MediaInfo{Path: path}
	if idx := strings.LastIndexByte(path, '/'); idx >= 0 {
		info.Name = path[idx+1:]
	} else {
		info.Name = path
	}
	info.Duration = parseFloat(out.Format.Duration)
	info.Size = parseInt64(out.Format.Size)
	info.BitRate = parseInt64(out.Format.BitRate)
	info.FormatName = out.Format.FormatName
	for _, s := range out.Streams {
		st := MediaStream{
			Index:         s.Index,
			CodecType:     s.CodecType,
			CodecName:     s.CodecName,
			Width:         s.Width,
			Height:        s.Height,
			Duration:      parseFloat(s.Duration),
			BitRate:       parseInt64(s.BitRate),
			FrameRate:     parseRational(s.FrameRate, s.RFrameRate),
			SampleAspect:  s.SampleAspect,
			PixFmt:        s.PixFmt,
			Profile:       s.Profile,
			Level:         s.Level,
			SampleRate:    int(parseFloat(s.SampleRate)),
			Channels:      s.Channels,
			ChannelLayout: s.ChannelLayout,
			IsAttachedPic: s.Disposition["attached_pic"] == 1,
		}
		st.PixelAspect = pixelAspect(st)
		info.Streams = append(info.Streams, st)
		// 首个非封面视频流 / 首个音频流
		if st.CodecType == "video" && !st.IsAttachedPic && info.Video == nil {
			cp := st
			info.Video = &cp
		}
		if st.CodecType == "audio" && info.Audio == nil {
			cp := st
			info.Audio = &cp
		}
	}
	if info.Duration <= 0 {
		if info.Video != nil {
			info.Duration = info.Video.Duration
		} else if info.Audio != nil {
			info.Duration = info.Audio.Duration
		}
	}
	if info.Size <= 0 {
		if st, err := os.Stat(path); err == nil {
			info.Size = st.Size()
		}
	}
	if info.BitRate <= 0 && info.Duration > 0 && info.Size > 0 {
		info.BitRate = int64(float64(info.Size) * 8 / info.Duration)
	}
	return info, nil
}

// pixelAspect 由 SAR / DAR 推算显示宽高比（方形像素时为 1）。
func pixelAspect(st MediaStream) float64 {
	w, h := st.Width, st.Height
	if w <= 0 || h <= 0 {
		return 1
	}
	if sar := parseRational(st.SampleAspect, ""); sar > 0 {
		return sar
	}
	return 1
}

// parseRational 解析 "30000/1001" 形式的帧率；失败时回退到 fallback。
func parseRational(s, fallback string) float64 {
	if v := rationalValue(s); v > 0 {
		return v
	}
	return rationalValue(fallback)
}

// rationalValue 解析 "30000/1001"（分数）或 "25"（整数）形式的数值。
// ffprobe 的 avg_frame_rate / r_frame_rate / sample_aspect_ratio 均为分数形式。
func rationalValue(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "N/A" {
		return 0
	}
	if num, den, ok := strings.Cut(s, "/"); ok {
		n, err1 := strconv.ParseFloat(strings.TrimSpace(num), 64)
		d, err2 := strconv.ParseFloat(strings.TrimSpace(den), 64)
		if err1 != nil || err2 != nil || d == 0 {
			return 0
		}
		if v := n / d; v > 0 {
			return v
		}
		return 0
	}
	return parseFloat(s)
}

func parseFloat(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "N/A" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}

func parseInt64(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "N/A" {
		return 0
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(f)
	}
	return 0
}

// EnsureFFmpegAvailable 供启动时快速自检，返回检测到的版本字符串。
func (c *Client) EnsureFFmpegAvailable(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, c.ffmpegBin, "-version").Output()
	if err != nil {
		return "", err
	}
	first := strings.SplitN(string(out), "\n", 2)[0]
	return strings.TrimSpace(first), nil
}

// EnsureFFprobeAvailable 供启动时快速自检 FFprobe。
func (c *Client) EnsureFFprobeAvailable(ctx context.Context) error {
	if _, err := exec.CommandContext(ctx, c.ffprobeBin, "-version").Output(); err != nil {
		return err
	}
	return nil
}

// ==================== 编码器与硬件加速能力探测 ====================

// Accel 硬件加速标识。
const (
	AccelNone  = "none"
	AccelAuto  = "auto"
	AccelNVENC = "nvenc"
	AccelQSV   = "qsv"
	AccelVAAPI = "vaapi"
)

// AccelNames 返回全部硬件加速标识（展示顺序）。
func AccelNames() []string {
	return []string{AccelNone, AccelNVENC, AccelQSV, AccelVAAPI}
}

// AccelSupport 描述一种硬件加速在本机的可用性。
type AccelSupport struct {
	// ID 加速标识：none / nvenc / qsv / vaapi
	ID string `json:"id"`
	// Name 中文展示名
	Name string `json:"name"`
	// Available 是否可用（编码器存在且设备节点存在）
	Available bool `json:"available"`
	// Reason 不可用原因（可用时为空）
	Reason string `json:"reason,omitempty"`
	// Device 设备节点（nvenc / vaapi 有值）
	Device string `json:"device,omitempty"`
	// Encoders 该加速下检测到的编码器名
	Encoders []string `json:"encoders"`
	// Codecs 该加速可用的编码族 ID（h264 / h265 / vp9 / av1）
	Codecs []string `json:"codecs"`
}

// Capabilities FFmpeg 环境能力描述（前端据此渲染编码器下拉框）。
type Capabilities struct {
	// Ffmpeg ffmpeg 版本首行
	FFmpeg string `json:"ffmpeg"`
	// FFprobe ffprobe 版本首行
	FFprobe string `json:"ffprobe"`
	// FfmpegPath ffmpeg 可执行文件路径
	FfmpegPath string `json:"ffmpeg_path"`
	// Encoders 本机全部可用编码器名
	Encoders []string `json:"encoders"`
	// SoftwareCodecs 本机可用的软件编码族
	SoftwareCodecs []string `json:"software_codecs"`
	// SoftwarePresets x264/x265 可用 preset 名称
	SoftwarePresets []string `json:"software_presets"`
	// HWAccel 硬件加速可用性列表
	HWAccel []AccelSupport `json:"hw_accel"`
	// HWDecode 解码器 ID 列表（qsv / vaapi / cuda 等）
	HWDecode []string `json:"hw_decode"`
	// PixelFormats 当前编码路径可用的像素格式
	PixelFormats []string `json:"pixel_formats"`
	// Detected 探测时间
	Detected string `json:"detected"`
}

// Detect 探测本机 FFmpeg 能力（编码器、preset、硬件加速）。结果在进程内缓存。
func (c *Client) Detect(ctx context.Context) *Capabilities {
	c.capOnce.Do(func() { c.caps = c.detect(ctx) })
	return c.caps
}

// detect 执行实际探测（无缓存）。
func (c *Client) detect(ctx context.Context) *Capabilities {
	caps := &Capabilities{
		FfmpegPath:      c.ffmpegBin,
		Encoders:        []string{},
		SoftwareCodecs:  []string{},
		SoftwarePresets: []string{},
		HWAccel:         []AccelSupport{},
		HWDecode:        []string{},
		PixelFormats:    []string{"yuv420p", "yuv420p10le", "yuv422p10le"},
		Detected:        time.Now().Format(time.RFC3339),
	}
	if out, err := runLimited(ctx, 20*time.Second, c.ffmpegBin, "-version"); err == nil {
		caps.FFmpeg = firstLine(out)
	}
	if out, err := runLimited(ctx, 20*time.Second, c.ffprobeBin, "-version"); err == nil {
		caps.FFprobe = firstLine(out)
	}

	encoders := map[string]bool{}
	if out, err := runLimited(ctx, 30*time.Second, c.ffmpegBin, "-hide_banner", "-encoders"); err == nil {
		for _, name := range parseFFmpegList(out, "V") {
			encoders[name] = true
		}
	}
	for name := range encoders {
		caps.Encoders = append(caps.Encoders, name)
	}
	sort.Strings(caps.Encoders)

	// 软件编码族
	for _, f := range CodecFamilies() {
		for _, enc := range f.SoftwareEncoders {
			if encoders[enc] {
				caps.SoftwareCodecs = append(caps.SoftwareCodecs, f.ID)
				break
			}
		}
	}

	// x264 / x265 的 preset 列表
	if encoders["libx264"] || encoders["libx265"] {
		if out, err := runLimited(ctx, 30*time.Second, c.ffmpegBin, "-hide_banner", "-h", "encoder=libx264"); err == nil {
			caps.SoftwarePresets = append(caps.SoftwarePresets, parseX264Presets(out)...)
		}
	}
	if len(caps.SoftwarePresets) == 0 {
		caps.SoftwarePresets = []string{"ultrafast", "superfast", "veryfast", "faster", "fast", "medium", "slow", "slower", "veryslow"}
	}

	// 硬件解码器
	if out, err := runLimited(ctx, 30*time.Second, c.ffmpegBin, "-hide_banner", "-hwaccels"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "Hardware acceleration") {
				continue
			}
			caps.HWDecode = append(caps.HWDecode, line)
		}
	}

	// 硬件加速可用性
	device := c.findRenderDevice()
	nvidia := findNvidiaDevice()
	for _, accel := range []struct {
		id, name, dev string
	}{
		{AccelNone, "软件编码（CPU）", ""},
		{AccelNVENC, "NVIDIA NVENC", nvidia},
		{AccelQSV, "Intel QSV", device},
		{AccelVAAPI, "VAAPI（Intel/AMD）", device},
	} {
		sup := AccelSupport{ID: accel.id, Name: accel.name, Device: accel.dev, Encoders: []string{}, Codecs: []string{}}
		if accel.id == AccelNone {
			sup.Available = true
		} else {
			// 编译进 ffmpeg 的编码器
			var matched []string
			for _, f := range CodecFamilies() {
				if enc, ok := f.HWEncoders[accel.id]; ok && encoders[enc] {
					matched = append(matched, enc)
					sup.Codecs = append(sup.Codecs, f.ID)
				}
			}
			sup.Encoders = matched
			switch {
			case len(matched) == 0:
				sup.Reason = "当前 FFmpeg 未编译该加速的编码器"
			case accel.id == AccelNVENC && accel.dev == "":
				sup.Reason = "未检测到 NVIDIA 设备（/dev/nvidia*）"
			case (accel.id == AccelQSV || accel.id == AccelVAAPI) && accel.dev == "":
				sup.Reason = "未检测到 /dev/dri 渲染设备"
			default:
				// 设备节点与编译支持都具备后，再做一次真实编码验证：
				// 容器中设备映射不正确、驱动缺失时节点仍存在，
				// 若不验证会让「自动」把任务全部导向不可用的硬件路径。
				if err := c.probeAccel(ctx, accel.id, matched[0], accel.dev); err != nil {
					sup.Reason = "硬件编码自检失败：" + err.Error()
					sup.Codecs = nil
				} else {
					sup.Available = true
				}
			}
		}
		caps.HWAccel = append(caps.HWAccel, sup)
	}

	return caps
}

// probeAccel 用一帧测试视频验证硬件编码器是否真的可用。
// 编译支持 + 设备节点存在并不等于可用：容器设备映射错误、驱动缺失时，
// 真实调用仍会失败。返回压缩后的首行错误信息。
func (c *Client) probeAccel(ctx context.Context, accel, encoder, device string) error {
	args := []string{"-hide_banner", "-loglevel", "error", "-f", "lavfi",
		"-i", "color=c=black:s=128x128:r=10:d=1", "-frames:v", "2",
		"-c:v", encoder}
	switch accel {
	case AccelVAAPI:
		args = append(args, "-vaapi_device", device, "-vf", "format=nv12,hwupload")
	case AccelQSV:
		args = append(args, "-vf", "format=nv12")
	}
	args = append(args, "-f", "null", "-")

	out, err := runLimited(ctx, 15*time.Second, c.ffmpegBin, args...)
	if err == nil {
		return nil
	}
	return errors.New(summarizeErr(out))
}

// findRenderDevice 查找第一个可用的 /dev/dri/renderD* 设备节点。
// 优先返回配置指定的设备，不存在时回退到系统首个渲染节点。
func (c *Client) findRenderDevice() string {
	if c.vaapiDevice != "" {
		if _, err := os.Stat(c.vaapiDevice); err == nil {
			return c.vaapiDevice
		}
	}
	if matches, err := filepath.Glob("/dev/dri/renderD*"); err == nil && len(matches) > 0 {
		return matches[0]
	}
	return ""
}

// findNvidiaDevice 查找 NVIDIA 设备节点。
func findNvidiaDevice() string {
	for _, p := range []string{"/dev/nvidiactl", "/dev/nvidia0", "/dev/nvidia"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// parseFFmpegList 解析 `ffmpeg -encoders` / `-decoders` 输出，返回指定流类型的名称。
// 输出形如 " V....D h264_nvenc           NVIDIA NVENC H.264 encoder"：
// 首列固定为 6 个能力标志位，其首位字符即流类型（V 视频 / A 音频 / S 字幕）。
// streamType 传流类型字符本身，例如 "V"（视频编码器）、"A"（音频编码器）。
func parseFFmpegList(out, streamType string) []string {
	var names []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 8 {
			continue
		}
		fields := strings.Fields(line)
		// 至少要有「能力标志 + 名称 + 描述」三段，缺描述的是表头或异常行
		if len(fields) < 3 {
			continue
		}
		flags := fields[0]
		if len(flags) != 6 || string(flags[0]) != streamType {
			continue
		}
		name := fields[1]
		// 过滤图例行（如 "V..... = Video"）与异常行
		if strings.ContainsAny(name, "=,;") || strings.HasPrefix(name, "-") {
			continue
		}
		names = append(names, name)
	}
	return names
}

// parseX264Presets 从 `ffmpeg -h encoder=libx264` 输出中提取 preset 取值。
func parseX264Presets(out string) []string {
	for _, line := range strings.Split(out, "\n") {
		idx := strings.Index(line, "preset")
		if idx < 0 {
			continue
		}
		rest := line[idx:]
		start := strings.IndexByte(rest, '{')
		end := strings.LastIndexByte(rest, '}')
		if start < 0 || end <= start {
			continue
		}
		var presets []string
		for _, p := range strings.Split(rest[start+1:end], " ") {
			if p = strings.TrimSpace(p); p != "" {
				presets = append(presets, p)
			}
		}
		if len(presets) > 0 {
			return presets
		}
	}
	return nil
}

// runLimited 带超时执行命令并限制输出大小。
func runLimited(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, name, args...)
	buf := &limitedBuffer{max: 1 << 20}
	cmd.Stdout = buf
	cmd.Stderr = buf
	if err := cmd.Run(); err != nil {
		return buf.String(), err
	}
	return buf.String(), nil
}

func firstLine(s string) string {
	return strings.TrimSpace(strings.SplitN(strings.TrimSpace(s), "\n", 2)[0])
}

// limitedBuffer 有限大小的输出缓冲，避免异常输出把内存撑爆。
type limitedBuffer struct {
	buf []byte
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(b.buf) < b.max {
		room := b.max - len(b.buf)
		if len(p) > room {
			p = p[:room]
		}
		b.buf = append(b.buf, p...)
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string { return strings.TrimSpace(string(b.buf)) }
