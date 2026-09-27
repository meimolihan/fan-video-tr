package ffmpeg

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseRational(t *testing.T) {
	cases := []struct {
		raw      string
		fallback string
		want     float64
	}{
		{"25", "30", 25},
		{"25/1", "30", 25},
		{"30000/1001", "30", 29.97002997},
		{"24000/1001", "30", 23.97602398},
		{"0/0", "30", 30},
		{"", "30", 30},
		{"N/A", "24", 24},
		{"abc", "25", 25},
	}
	for _, c := range cases {
		got := parseRational(c.raw, c.fallback)
		if diff := got - c.want; diff > 0.001 || diff < -0.001 {
			t.Errorf("parseRational(%q, %q) = %v, want %v", c.raw, c.fallback, got, c.want)
		}
	}
}

func TestRationalValue(t *testing.T) {
	if v := rationalValue("30/1"); v != 30 {
		t.Errorf("rationalValue(30/1) = %v, want 30", v)
	}
	if v := rationalValue("bad"); v != 0 {
		t.Errorf("rationalValue(bad) = %v, want 0", v)
	}
}

func TestParseFFmpegList(t *testing.T) {
	out := `Encoders:
 V..... = Video
 V....D h264_nvenc           NVIDIA NVENC H.264 encoder (codec h264)
 V....D libx264              libx264 H.264 / AVC / MPEG-4 AVC / MPEG-4 part 10 (codec h264)
 V..... hevc_qsv             Intel Quick Sync H.265 encoder (codec hevc)
 A....D aac                  AAC (Advanced Audio Coding)
`
	encs := parseFFmpegList(out, "V")
	want := []string{"h264_nvenc", "libx264", "hevc_qsv"}
	if !reflect.DeepEqual(encs, want) {
		t.Errorf("parseFFmpegList = %v, want %v", encs, want)
	}
	if got := parseFFmpegList(out, "A"); !reflect.DeepEqual(got, []string{"aac"}) {
		t.Errorf("parseFFmpegList(音频) = %v, want [aac]", got)
	}
	if got := parseFFmpegList("Encoders:\n", "V"); len(got) != 0 {
		t.Errorf("空输出应返回空列表，实际 %v", got)
	}
}

func TestParseBitrate(t *testing.T) {
	cases := map[string]int64{
		"4M":    4_000_000,
		"6000k": 6_000_000,
		"1.5M":  1_500_000,
		"800k":  800_000,
		"2000":  2_000_000, // 无单位按 k 处理
		"":      0,
		"auto":  0,
		"0":     0,
	}
	for in, want := range cases {
		got, err := ParseBitrate(in)
		if err != nil {
			t.Errorf("ParseBitrate(%q) 报错: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseBitrate(%q) = %d, want %d", in, got, want)
		}
	}
	if _, err := ParseBitrate("快一点"); err == nil {
		t.Error("非法码率应返回错误")
	}
}

// TestClampContainerCompat 锁定容器与编码的兼容性约束：
// 曾经的 bug 是 WebM 允许 H.264/AAC，导致 ffmpeg 直接失败。
func TestClampContainerCompat(t *testing.T) {
	webmH264 := &TranscodeOptions{
		Codec: "h264", Accel: AccelNone, RateControl: RateCRF, CRF: 23,
		Container: "webm", AudioCodec: "opus", PixelFormat: "yuv420p", Resolution: "keep", FPS: "keep",
	}
	if err := webmH264.Clamp(nil, 0); err == nil {
		t.Error("WebM + H.264 应被拒绝")
	}

	webmCopy := &TranscodeOptions{
		Codec: "vp9", Accel: AccelNone, RateControl: RateCRF, CRF: 30,
		Container: "webm", AudioCodec: "copy", PixelFormat: "yuv420p", Resolution: "keep", FPS: "keep",
	}
	if err := webmCopy.Clamp(nil, 0); err == nil {
		t.Error("WebM + copy 音轨应被拒绝（AAC 无法直接放进 WebM）")
	}

	ok := &TranscodeOptions{
		Codec: "vp9", Accel: AccelNone, RateControl: RateCRF, CRF: 30,
		Container: "webm", AudioCodec: "opus", PixelFormat: "yuv420p", Resolution: "720p", FPS: "keep",
	}
	if err := ok.Clamp(nil, 0); err != nil {
		t.Errorf("WebM + VP9 + Opus 应通过，实际: %v", err)
	}
}

// TestClampTwoPass 锁定双遍编码的两条规则：
//  1. CRF 与双遍互斥，必须自动切换成码率模式；
//  2. 硬件加速路径不能开双遍。
func TestClampTwoPass(t *testing.T) {
	opts := &TranscodeOptions{
		Codec: "h264", Accel: AccelNone, RateControl: RateCRF, CRF: 23, Preset: "medium",
		TwoPass: true, Container: "mp4", AudioCodec: "aac", PixelFormat: "yuv420p",
		Resolution: "1080p", FPS: "keep",
	}
	if err := opts.Clamp(nil, 0); err != nil {
		t.Fatalf("Clamp: %v", err)
	}
	if !opts.TwoPass {
		t.Fatal("软件路径应保留双遍编码")
	}
	if opts.RateControl != RateBitrate {
		t.Errorf("双遍编码下 RateControl 应为 bitrate，实际 %q", opts.RateControl)
	}
	if _, err := ParseBitrate(opts.VideoBitrate); err != nil || opts.VideoBitrate == "" {
		t.Errorf("双遍编码应自动补齐目标码率，实际 %q (%v)", opts.VideoBitrate, err)
	}

	hw := &TranscodeOptions{
		Codec: "h264", Accel: AccelNone, RateControl: RateBitrate, VideoBitrate: "2M",
		TwoPass: true, Container: "mp4", AudioCodec: "aac", PixelFormat: "yuv420p",
		Resolution: "720p", FPS: "keep",
	}
	if err := hw.Clamp(nil, 0); err != nil {
		t.Fatalf("Clamp: %v", err)
	}
	if !hw.TwoPass {
		t.Fatal("软件路径双遍应保留")
	}
	enc, err := hw.EncoderName(nil)
	if err != nil {
		t.Fatalf("EncoderName: %v", err)
	}
	if !supportsTwoPass(enc) {
		t.Errorf("挑选到的编码器 %q 必须支持双遍", enc)
	}
}

// TestBuildTranscodeArgsPassConsistency 保证两遍编码的编码器设置完全一致，
// 否则 x264 第二遍会报 "different weightp setting than first pass"。
func TestBuildTranscodeArgsPassConsistency(t *testing.T) {
	c, err := New(Options{FFmpegBin: "ffmpeg", FFprobeBin: "ffprobe"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	opts := TranscodeOptions{
		Codec: "h264", Accel: AccelNone, RateControl: RateBitrate, VideoBitrate: "2M",
		Preset: "medium", TwoPass: true, Container: "mp4", AudioCodec: "aac",
		PixelFormat: "yuv420p", Resolution: "720p", FPS: "keep",
		passLogFile: "/tmp/test.pass",
	}
	in, out := "/tmp/in.mp4", "/tmp/out.mp4"
	pass1 := strings.Join(c.buildTranscodeArgs(&opts, nil, in, out, 1), " ")
	pass2 := strings.Join(c.buildTranscodeArgs(&opts, nil, in, out, 2), " ")

	for _, want := range []string{"-preset medium", "-pix_fmt yuv420p", "-passlogfile /tmp/test.pass"} {
		if !strings.Contains(pass1, want) {
			t.Errorf("第一遍缺少 %q：%s", want, pass1)
		}
		if !strings.Contains(pass2, want) {
			t.Errorf("第二遍缺少 %q：%s", want, pass2)
		}
	}
	if !strings.Contains(pass1, "-pass 1") || !strings.Contains(pass2, "-pass 2") {
		t.Errorf("遍数参数错误：%s / %s", pass1, pass2)
	}
	if strings.Contains(pass1, "-pass 2") {
		t.Errorf("单遍任务绝不能出现 -pass 2：%s", pass1)
	}
	// 第一遍不该带码率/音频/容器参数
	for _, bad := range []string{"-b:v", "-maxrate", "-c:a", "-movflags"} {
		if strings.Contains(pass1, bad) {
			t.Errorf("第一遍不应包含 %q：%s", bad, pass1)
		}
	}
}

func TestBuildTranscodeArgsSinglePass(t *testing.T) {
	c, _ := New(Options{FFmpegBin: "ffmpeg", FFprobeBin: "ffprobe"})
	opts := TranscodeOptions{
		Codec: "h264", Accel: AccelNone, RateControl: RateCRF, CRF: 22, Preset: "veryfast",
		Container: "mp4", AudioCodec: "aac", AudioBitrate: "192k", PixelFormat: "yuv420p",
		Resolution: "1080p", FPS: "keep", FastStart: true,
	}
	args := strings.Join(c.buildTranscodeArgs(&opts, nil, "/tmp/in.mp4", "/tmp/out.mp4", 2), " ")
	if strings.Contains(args, "-pass") {
		t.Errorf("单遍编码不应出现 -pass：%s", args)
	}
	for _, want := range []string{"-c:v libx264", "-crf 22", "-c:a aac", "-movflags +faststart"} {
		if !strings.Contains(args, want) {
			t.Errorf("缺少 %q：%s", want, args)
		}
	}
}

func TestFPSValueAndResolution(t *testing.T) {
	if v, ok := FPSValue("30000/1001"); !ok || v < 29.9 || v > 30 {
		t.Errorf("FPSValue(30000/1001) = %v, %v", v, ok)
	}
	if _, ok := FPSValue("不存在"); ok {
		t.Error("非法帧率应返回 false")
	}
	if r, ok := ResolutionByID("1080p"); !ok || r.Width != 1920 || r.Height != 1080 {
		t.Errorf("ResolutionByID(1080p) = %+v, %v", r, ok)
	}
}

func TestSuggestBitrate(t *testing.T) {
	lo := SuggestBitrate(1280, 720, 30)
	hi := SuggestBitrate(3840, 2160, 30)
	if lo <= 0 || hi <= lo {
		t.Errorf("码率建议应随分辨率递增：720p=%d, 4K=%d", lo, hi)
	}
	if b := SuggestBitrate(0, 0, 0); b <= 0 {
		t.Errorf("未知分辨率应给出兜底码率，实际 %d", b)
	}
}
