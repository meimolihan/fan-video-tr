package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"go.uber.org/zap"

	"github.com/meimolihan/fan-video-tr/internal/config"
	"github.com/meimolihan/fan-video-tr/internal/ffmpeg"
)

// videoExts 可浏览 / 可转码的视频扩展名。
var videoExts = map[string]struct{}{
	".mp4": {}, ".mkv": {}, ".mov": {}, ".avi": {}, ".wmv": {},
	".flv": {}, ".webm": {}, ".m4v": {}, ".ts": {}, ".3gp": {},
	".rmvb": {}, ".mpg": {}, ".mpeg": {}, ".m2ts": {}, ".rm": {},
	".m2v": {}, ".vob": {}, ".ogv": {}, ".mts": {}, ".f4v": {},
}

// IsVideoFile 判断文件名是否为支持的视频格式。
func IsVideoFile(name string) bool {
	_, ok := videoExts[strings.ToLower(filepath.Ext(name))]
	return ok
}

// FileEntry 文件浏览条目。
type FileEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	IsVideo bool   `json:"is_video"`
	// Modified 修改时间（秒，Unix），0 表示未知
	Modified int64 `json:"modified"`
	// Deletable 标记该条目是否允许通过删除接口移除（位于允许删除的根目录内）
	Deletable bool `json:"deletable"`
}

// MediaService 媒体文件浏览与信息探测。
type MediaService struct {
	cfg *config.Config
	ffc *ffmpeg.Client
	log *zap.SugaredLogger

	infoMu    sync.Mutex
	infoCache map[string]*ffmpeg.MediaInfo
}

// NewMediaService 创建媒体服务。
func NewMediaService(cfg *config.Config, ffc *ffmpeg.Client, log *zap.SugaredLogger) *MediaService {
	return &MediaService{
		cfg:       cfg,
		ffc:       ffc,
		log:       log,
		infoCache: make(map[string]*ffmpeg.MediaInfo),
	}
}

// HomeDir 返回文件浏览器的起始目录。
func (m *MediaService) HomeDir() string {
	if dir := strings.TrimSpace(m.cfg.App.MediaDir); dir != "" {
		return filepath.Clean(dir)
	}
	if dir, err := os.Getwd(); err == nil {
		return dir
	}
	return "/"
}

// ListDir 列出目录条目：目录在前，按名称排序，仅保留目录与视频文件。
func (m *MediaService) ListDir(dir string) ([]FileEntry, error) {
	if dir == "" {
		dir = m.HomeDir()
	}
	dir = filepath.Clean(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	list := make([]FileEntry, 0, len(entries))
	roots := m.deletableRoots()
	for _, e := range entries {
		name := e.Name()
		fe := FileEntry{
			Name:      name,
			Path:      filepath.Join(dir, name),
			IsDir:     e.IsDir(),
			Deletable: pathDeletable(filepath.Join(dir, name), roots),
		}
		if info, err := e.Info(); err == nil {
			fe.Size = info.Size()
			fe.Modified = info.ModTime().Unix()
		}
		if !e.IsDir() {
			// 仅展示可读取的视频文件，便于在浏览器中直接预览
			if IsVideoFile(name) {
				fe.IsVideo = true
				list = append(list, fe)
			}
			continue
		}
		list = append(list, fe)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].IsDir != list[j].IsDir {
			return list[i].IsDir
		}
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
	return list, nil
}

// Info 探测媒体信息（带进程内缓存，避免重复探测同一文件）。
func (m *MediaService) Info(ctx context.Context, path string) (*ffmpeg.MediaInfo, error) {
	path = filepath.Clean(path)
	m.infoMu.Lock()
	if v, ok := m.infoCache[path]; ok {
		m.infoMu.Unlock()
		return v, nil
	}
	m.infoMu.Unlock()

	info, err := m.ffc.Probe(ctx, path)
	if err != nil {
		return nil, err
	}
	m.infoMu.Lock()
	// 简单上限，避免长时间运行后无限增长
	if len(m.infoCache) > 256 {
		m.infoCache = make(map[string]*ffmpeg.MediaInfo)
	}
	m.infoCache[path] = info
	m.infoMu.Unlock()
	return info, nil
}

// Forget 清除指定路径（及旧路径）的探测缓存。
func (m *MediaService) Forget(path string) {
	m.infoMu.Lock()
	delete(m.infoCache, filepath.Clean(path))
	m.infoMu.Unlock()
}

// ResolveStreamURL 将本地磁盘路径编码为可播放的流地址（供前端拼接使用）。
func ResolveStreamURL(path string) string {
	return "/api/media/stream?path=" + url.QueryEscape(path)
}

// ThumbURL 返回封面预览地址（取指定时间点的一帧）。
func ThumbURL(path string, at float64) string {
	return "/api/media/thumb?path=" + url.QueryEscape(path) + "&t=" + strings.TrimSuffix(strings.TrimRight(formatFloat(at), "0"), ".")
}

// formatFloat 以最少小数位输出浮点数（用于缩略图时间点）。
func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}

// ==================== 删除 ====================
//
// 文件浏览器允许向上导航至任意目录，但删除是不可逆的破坏性操作，
// 因此只放开应用自有目录：媒体目录、上传目录、输出目录。
// 数据目录根本身不放开，以免误删 config.yaml / 模板 / 日志等应用自身文件。

// deletableRoots 返回允许删除的根目录（绝对路径、已清理、去重）。
// 浏览根为文件系统根 / 时忽略该项，避免把「从 / 浏览」误当成「允许删除全盘」。
func (m *MediaService) deletableRoots() []string {
	candidates := []string{
		m.HomeDir(),
		m.cfg.UploadsDir(),
		m.cfg.OutputDir(),
	}
	roots := make([]string, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, c := range candidates {
		if strings.TrimSpace(c) == "" {
			continue
		}
		abs, err := filepath.Abs(c)
		if err != nil {
			continue
		}
		abs = filepath.Clean(abs)
		if abs == string(filepath.Separator) {
			continue
		}
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		roots = append(roots, abs)
	}
	return roots
}

// underRoot 判断 path 是否严格位于 root 之内。
// 用 filepath.Rel 而非字符串前缀比较，避免 /data-evil 命中 /data 这类误判。
func underRoot(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// isUnderRoots 判断 path 是否位于任一 root 之内。
func isUnderRoots(path string, roots []string) bool {
	for _, root := range roots {
		if underRoot(path, root) {
			return true
		}
	}
	return false
}

// resolveTarget 清理路径并解析符号链接，防止经由软链跳出允许目录。
// 路径不存在时按清理后的绝对路径返回，交由后续 os 调用报错。
func resolveTarget(path string) (string, bool) {
	if strings.TrimSpace(path) == "" {
		return "", false
	}
	target := filepath.Clean(path)
	if !filepath.IsAbs(target) {
		abs, err := filepath.Abs(target)
		if err != nil {
			return "", false
		}
		target = abs
	}
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}
	return target, true
}

// pathDeletable 用预计算的根目录集合判断单个路径是否可删除。
func pathDeletable(path string, roots []string) bool {
	target, ok := resolveTarget(path)
	if !ok {
		return false
	}
	return isUnderRoots(target, roots)
}

// IsDeletable 判断路径是否允许删除（供 ListDir 标记条目与外部校验使用）。
func (m *MediaService) IsDeletable(path string) bool {
	return pathDeletable(path, m.deletableRoots())
}

// Remove 删除文件或目录（目录递归删除）。返回实际删除的路径。
// 仅允许删除位于 deletableRoots 内的内容，且不允许删除根目录本身。
func (m *MediaService) Remove(path string) (string, error) {
	target, ok := resolveTarget(path)
	if !ok {
		return "", errors.New("缺少 path 参数")
	}
	if !isUnderRoots(target, m.deletableRoots()) {
		return "", fmt.Errorf("出于安全考虑，仅允许删除媒体、上传与输出目录内的内容: %s", target)
	}

	// 删除时使用清理后的原始路径而非解析结果：目标为符号链接时只移除链接本身，
	// 不跟随其指向的目标，避免删掉链接外的文件。
	orig := filepath.Clean(path)
	if abs, err := filepath.Abs(orig); err == nil {
		orig = abs
	}
	if _, err := os.Lstat(orig); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("目标不存在: %s", target)
		}
		return "", fmt.Errorf("无法访问目标: %w", err)
	}
	if err := os.RemoveAll(orig); err != nil {
		// 权限/只读属于部署层面的常见问题，给出可操作提示而非裸露的 errno。
		// EROFS 不被 errors.Is(err, fs.ErrPermission) 覆盖，需单独判断。
		if errors.Is(err, syscall.EROFS) {
			return "", fmt.Errorf("删除失败：该目录以只读方式挂载，无法删除（请检查容器卷挂载是否带 :ro）: %w", err)
		}
		if errors.Is(err, fs.ErrPermission) {
			return "", fmt.Errorf("删除失败：容器内应用用户无权删除该文件（请检查目录挂载的读写权限与应用用户权限）: %w", err)
		}
		return "", fmt.Errorf("删除失败: %w", err)
	}
	// 清除探测缓存，避免删除后残留过期信息
	m.Forget(target)
	return target, nil
}
