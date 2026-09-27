package service

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

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
	for _, e := range entries {
		name := e.Name()
		fe := FileEntry{
			Name:  name,
			Path:  filepath.Join(dir, name),
			IsDir: e.IsDir(),
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
