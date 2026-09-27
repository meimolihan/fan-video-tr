package handler

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/meimolihan/fan-video-tr/internal/service"
)

type mediaHandler struct {
	svc *service.Service
	log *zap.SugaredLogger
}

// listDir 浏览目录 GET /api/media/dir?path=/foo
func (h *mediaHandler) listDir(c *gin.Context) {
	dir := c.Query("path")
	list, err := h.svc.Media.ListDir(dir)
	if err != nil {
		h.log.Warnf("浏览目录失败 %s: %v", dir, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "无法读取目录: " + err.Error()})
		return
	}
	// 统计视频数量，便于前端提示
	videos := 0
	for _, it := range list {
		if it.IsVideo {
			videos++
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"path":    h.svc.Media.HomeDir(),
		"current": normalizePath(dir),
		"up":      upPath(dir),
		"total":   len(list),
		"videos":  videos,
		"items":   list,
	})
}

// normalizePath 返回空串对应的实际根路径展示值。
func normalizePath(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Clean(dir)
}

// upPath 返回上一级目录路径；已在根目录时返回空串。
func upPath(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return ""
	}
	cleaned := filepath.Clean(dir)
	parent := filepath.Dir(cleaned)
	if parent == cleaned {
		return ""
	}
	return parent
}

// info 探测媒体信息 GET /api/media/info?path=
func (h *mediaHandler) info(c *gin.Context) {
	p := c.Query("path")
	if strings.TrimSpace(p) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 path 参数"})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	info, err := h.svc.Media.Info(ctx, p)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"info":   info,
		"stream": service.ResolveStreamURL(p),
		"thumb":  service.ThumbURL(p, thumbTime(info.Duration)),
	})
}

// thumbTime 取缩略图的推荐时间点：避开开头黑帧，取 10% 处。
func thumbTime(duration float64) float64 {
	if duration <= 0 {
		return 0
	}
	if t := duration * 0.1; t < duration-1 {
		return t
	}
	return duration / 2
}

// probeBatch 批量探测（供前端预检多个文件） POST /api/media/probe
// body: {paths: [...]}
func (h *mediaHandler) probeBatch(c *gin.Context) {
	var req struct {
		Paths []string `json:"paths"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数无效: " + err.Error()})
		return
	}
	paths := req.Paths
	if len(paths) > 200 {
		paths = paths[:200]
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()

	items := make([]gin.H, 0, len(paths))
	for _, p := range paths {
		item := gin.H{"path": p, "name": filepath.Base(p)}
		info, err := h.svc.Media.Info(ctx, p)
		if err != nil {
			item["error"] = err.Error()
			items = append(items, item)
			continue
		}
		item["info"] = info
		items = append(items, item)
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// stream 播放视频流 GET /api/media/stream?path=（支持 HTTP Range）
func (h *mediaHandler) stream(c *gin.Context) {
	p := c.Query("path")
	if strings.TrimSpace(p) == "" {
		c.Status(http.StatusBadRequest)
		return
	}
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			c.Status(http.StatusNotFound)
		} else {
			c.Status(http.StatusBadRequest)
		}
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		c.Status(http.StatusNotFound)
		return
	}
	ct := mimeTypeForExt(filepath.Ext(p))
	if ct != "" {
		c.Header("Content-Type", ct)
	}
	c.Header("Accept-Ranges", "bytes")
	http.ServeContent(c.Writer, c.Request, filepath.Base(p), st.ModTime(), f)
}

// thumb 截取指定时间点的一帧作为缩略图 GET /api/media/thumb?path=&t=
func (h *mediaHandler) thumb(c *gin.Context) {
	p := c.Query("path")
	if strings.TrimSpace(p) == "" {
		c.Status(http.StatusBadRequest)
		return
	}
	at := 0.0
	if v := c.Query("t"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			at = f
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	data, _, err := h.svc.FFmpeg.CaptureFrameBytes(ctx, p, at)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.Header("Cache-Control", "private, max-age=600")
	c.Data(http.StatusOK, "image/jpeg", data)
}
