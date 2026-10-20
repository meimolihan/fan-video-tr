package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/meimolihan/fan-video-tr/internal/config"
	"go.uber.org/zap"
)

// errTooLarge 上传内容超过配置的大小上限。
var errTooLarge = errors.New("upload exceeds size limit")

// UploadService 视频上传：流式落盘 + 原子重命名 + 重名自动改名。
// 目的目录固定为配置的上传目录（app.upload_dir），不接受客户端指定，防止越权写入。
type UploadService struct {
	cfg *config.Config
	log *zap.SugaredLogger
}

// NewUploadService 创建上传服务。
func NewUploadService(cfg *config.Config, log *zap.SugaredLogger) *UploadService {
	if cfg.App.UploadMaxSize <= 0 {
		cfg.App.UploadMaxSize = 10 * 1024 * 1024 * 1024
	}
	return &UploadService{cfg: cfg, log: log}
}

// UploadResult 单个文件的上传结果。
type UploadResult struct {
	Path    string `json:"path"`    // 落盘绝对路径
	Name    string `json:"name"`    // 最终文件名（重名时可能被自动改名）
	Size    int64  `json:"size"`    // 实际字节数
	Renamed bool   `json:"renamed"` // 是否因重名被自动改名
}

// Save 将上传流写入上传目录。
//   - filename：客户端提供的原始文件名，会做净化与扩展名白名单校验
//   - r：上传内容流（multipart part 或任意 Reader）
//   - ctx：取消时中断写入并清理临时文件
//
// 落盘策略：先写 <name>.part 临时文件，完成后原子重命名为正式文件名，
// 避免上传中途的文件被浏览器/转码任务看到。
func (u *UploadService) Save(ctx context.Context, filename string, r io.Reader) (*UploadResult, error) {
	name := sanitizeUploadName(filename)
	if !IsVideoFile(name) {
		return nil, fmt.Errorf("不支持的视频格式: %s（仅支持 mp4/mkv/mov/avi 等视频格式）", strings.ToLower(filepath.Ext(name)))
	}

	dir := u.cfg.UploadsDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("上传目录不可用: %w", err)
	}

	dst, renamed := uniqueFilePath(dir, name)
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return nil, fmt.Errorf("创建临时文件失败: %w", err)
	}

	n, copyErr := io.Copy(f, &limitReader{r: r, max: u.cfg.App.UploadMaxSize, ctx: ctx})
	if closeErr := f.Close(); copyErr == nil && closeErr != nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		_ = os.Remove(tmp) // 任何失败路径都不留 .part 残留
		if errors.Is(copyErr, errTooLarge) {
			return nil, fmt.Errorf("文件超过大小上限（%s）", fmtBytes(u.cfg.App.UploadMaxSize))
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("写入失败: %w", copyErr)
	}

	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("保存失败: %w", err)
	}

	if u.log != nil {
		u.log.Infof("视频上传完成: %s (%d bytes)", dst, n)
	}
	return &UploadResult{Path: dst, Name: filepath.Base(dst), Size: n, Renamed: renamed}, nil
}

// limitReader 带大小上限与 context 取消检查的 Reader。
// 超限时返回 errTooLarge（由调用方清理临时文件）。
type limitReader struct {
	r   io.Reader
	max int64
	n   int64
	ctx context.Context
}

func (l *limitReader) Read(p []byte) (int, error) {
	if l.ctx != nil {
		if err := l.ctx.Err(); err != nil {
			return 0, err
		}
	}
	n, err := l.r.Read(p)
	l.n += int64(n)
	if l.n > l.max {
		return n, errTooLarge
	}
	return n, err
}

// sanitizeUploadName 净化客户端文件名：
//   - 去目录成分（防 ../ 与绝对路径穿越）
//   - 去控制字符，Windows 非法字符替换为下划线
//   - 限长（保留扩展名），空结果回退 upload.mp4
func sanitizeUploadName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "upload.mp4"
	}
	// 反斜杠一律视为路径分隔符后取最后一段（防 ../ 与绝对路径穿越，跨平台行为一致）
	name = path.Base(strings.ReplaceAll(name, "\\", "/"))

	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f: // 控制字符直接剔除
			continue
		case strings.ContainsRune(`\/:*?"<>|`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	name = strings.Trim(strings.TrimSpace(b.String()), ".")
	if name == "" {
		return "upload.mp4"
	}

	// 限长：总长 100 字符，优先保留扩展名
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	if utf8.RuneCountInString(base) > 100-len([]rune(ext)) {
		runes := []rune(base)
		base = string(runes[:100-len([]rune(ext))])
	}
	if base == "" {
		base = "upload"
	}
	return base + ext
}

// uniqueFilePath 目标已存在时自动追加 (n) 序号，绝不覆盖已有文件。
// 返回最终路径与是否发生了改名。
func uniqueFilePath(dir, name string) (string, bool) {
	dst := filepath.Join(dir, name)
	if _, err := os.Stat(dst); err != nil {
		return dst, false
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; i < 10000; i++ {
		cand := filepath.Join(dir, fmt.Sprintf("%s(%d)%s", base, i, ext))
		if _, err := os.Stat(cand); err != nil {
			return cand, true
		}
	}
	// 理论上不会走到：极端并发下追加时间戳兜底
	return filepath.Join(dir, fmt.Sprintf("%s(%d)%s", base, time.Now().UnixNano()%1e8, ext)), true
}

// fmtBytes 人类可读的字节数（错误提示用）。
func fmtBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
