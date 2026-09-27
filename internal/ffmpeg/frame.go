package ffmpeg

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CaptureFrame 在指定时间点截取一帧并输出为图片文件（默认 jpg）。
func (c *Client) CaptureFrame(ctx context.Context, input string, at float64, output string) error {
	args := []string{
		"-hide_banner",
		"-y",
		"-ss", formatSeconds(at),
		"-i", input,
		"-frames:v", "1",
		"-q:v", "2",
		output,
	}
	cmd := exec.CommandContext(ctx, c.ffmpegBin, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("截取帧失败（时间 %.3fs）: %v（%s）", at, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// CaptureFrameBytes 在指定时间点截取一帧并返回图片字节与 Content-Type，
// 供文件列表缩略图使用。
func (c *Client) CaptureFrameBytes(ctx context.Context, input string, at float64) ([]byte, string, error) {
	dir, err := os.MkdirTemp("", "fan-video-tr-frame-*")
	if err != nil {
		return nil, "", err
	}
	defer os.RemoveAll(dir)

	out := filepath.Join(dir, "frame.jpg")
	if err := c.CaptureFrame(ctx, input, at, out); err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(out)
	if err != nil {
		return nil, "", err
	}
	return data, "image/jpeg", nil
}

// SuggestedCaptureTime 返回无封面时默认截取的推荐时间点（取 10% 处）。
func SuggestedCaptureTime(duration float64) float64 {
	if duration <= 0 {
		return 0
	}
	if t := duration * 0.1; t < duration-1 {
		return t
	}
	return duration / 2
}
