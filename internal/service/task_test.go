package service

import (
	"strings"
	"testing"

	"github.com/meimolihan/fan-video-tr/internal/ffmpeg"
)

func TestTargetResolution(t *testing.T) {
	cases := []struct {
		name         string
		resolution   string
		custom       string
		srcW, srcH   int
		wantW, wantH int
	}{
		{name: "keep 保持原样", resolution: "keep", srcW: 1920, srcH: 1080},
		{name: "标准 1080p", resolution: "1080p", srcW: 3840, srcH: 2160, wantW: 1920, wantH: 1080},
		{name: "720p", resolution: "720p", srcW: 1920, srcH: 1080, wantW: 1280, wantH: 720},
		{name: "不放大小于源的档位", resolution: "1080p", srcW: 1280, srcH: 720},
		{name: "未知源尺寸时用档位值", resolution: "1080p", wantW: 1920, wantH: 1080},
		{name: "竖屏源按比例收缩", resolution: "1080p", srcW: 1080, srcH: 1920, wantW: 606, wantH: 1080},
		{name: "自定义尺寸", resolution: "custom", custom: "640x360", srcW: 1920, srcH: 1080, wantW: 640, wantH: 360},
		{name: "非法自定义尺寸回退为原始尺寸", resolution: "custom", custom: "乱写", srcW: 1920, srcH: 1080},
	}
	for _, c := range cases {
		opts := ffmpeg.TranscodeOptions{Resolution: c.resolution, CustomSize: c.custom}
		w, h := targetResolution(opts, c.srcW, c.srcH)
		if w != c.wantW || h != c.wantH {
			t.Errorf("%s: targetResolution = %dx%d, want %dx%d", c.name, w, h, c.wantW, c.wantH)
		}
		if w%2 != 0 || h%2 != 0 {
			t.Errorf("%s: 输出尺寸必须为偶数，实际 %dx%d", c.name, w, h)
		}
	}
}

func TestBuildOutputName(t *testing.T) {
	cases := []struct {
		src, suffix, container, want string
	}{
		// 后缀由 resolveSuffix 先行解析，这里只验证拼接与净化
		{"/a/b/电影.mkv", "_转码", "mp4", "电影_转码.mp4"},
		{"/a/b/movie.mp4", "_1080pH265", "mkv", "movie_1080pH265.mkv"},
		{"/a/b/clip.webm", "_", "webm", "clip_.webm"},
		{"/a/b/no_ext", "_转码", "mov", "no_ext_转码.mov"},
		{"/a/b/a/bad:name.mp4", "", "mp4", "bad_name.mp4"},
		{"/a/b/.hidden.mp4", "_转码", "mp4", ".hidden_转码.mp4"},
		{"/a/b/x.MKV", "_转码", "mkv", "x_转码.mkv"},
	}
	for _, c := range cases {
		if got := buildOutputName(c.src, c.suffix, c.container); got != c.want {
			t.Errorf("buildOutputName(%q,%q,%q) = %q, want %q", c.src, c.suffix, c.container, got, c.want)
		}
	}
}

func TestResolveSuffix(t *testing.T) {
	none := TranscodeParams{Codec: "h264", Resolution: "keep"}
	if got := resolveSuffix("none", none); got != "" {
		t.Errorf("suffix=none 应为空，实际 %q", got)
	}
	if got := resolveSuffix("  ", none); got != DefaultNameSuffix {
		t.Errorf("全默认参数应回退到默认后缀 %q，实际 %q", DefaultNameSuffix, got)
	}
	h265 := TranscodeParams{Codec: "h265", Resolution: "1080p"}
	if got := resolveSuffix("", h265); got == "" || got == DefaultNameSuffix {
		t.Errorf("H.265+1080p 应推导出专属后缀，实际 %q", got)
	}
	if got := resolveSuffix("a/b:c", none); got != "a_b_c" {
		t.Errorf("非法字符应被替换，实际 %q", got)
	}
}

func TestNormalizeSaveTarget(t *testing.T) {
	if got := normalizeSaveTarget(SaveTargetSource); got != SaveTargetSource {
		t.Errorf("source 应保持不变，实际 %q", got)
	}
	for _, in := range []string{"", "乱填", "default", "/tmp"} {
		if got := normalizeSaveTarget(in); got != SaveTargetDefault {
			t.Errorf("normalizeSaveTarget(%q) = %q, want %q", in, got, SaveTargetDefault)
		}
	}
}

func TestSourceDirOf(t *testing.T) {
	dir, err := sourceDirOf("/vol1/1000/视频/a.mp4")
	if err != nil || dir != "/vol1/1000/视频" {
		t.Errorf("sourceDirOf = %q, %v", dir, err)
	}
	if _, err := sourceDirOf("相对路径/a.mp4"); err == nil {
		t.Error("相对路径应报错")
	}
	if _, err := sourceDirOf("  "); err == nil {
		t.Error("空路径应报错")
	}
}

func TestParamsSummary(t *testing.T) {
	p := TranscodeParams{
		Codec: "h264", Accel: "none", RateControl: "crf", CRF: 23,
		Resolution: "1080p", FPS: "30", AudioCodec: "aac", AudioBitrate: "128k",
	}
	s := p.Summary()
	for _, want := range []string{"H264", "CRF 23", "1920×1080", "30fps", "AAC 128k"} {
		if !strings.Contains(s, want) {
			t.Errorf("Summary() = %q, 缺少 %q", s, want)
		}
	}
	noAudio := p
	noAudio.AudioCodec = "none"
	if !strings.Contains(noAudio.Summary(), "无音轨") {
		t.Errorf("移除音轨时 Summary() = %q, 应提示无音轨", noAudio.Summary())
	}
}
