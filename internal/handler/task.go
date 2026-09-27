package handler

import (
	"context"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/meimolihan/fan-video-tr/internal/service"
)

type taskHandler struct {
	svc *service.Service
	log *zap.SugaredLogger
}

// create POST /api/tasks
// body: {paths: [...], params: {...}, suffix: "...", save_target: "default|source"}
func (h *taskHandler) create(c *gin.Context) {
	var req struct {
		Paths      []string                `json:"paths"`
		Path       string                  `json:"path"`
		Params     service.TranscodeParams `json:"params"`
		Suffix     string                  `json:"suffix"`
		SaveTarget string                  `json:"save_target"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数无效: " + err.Error()})
		return
	}
	// 兼容单文件提交：path 字段
	if len(req.Paths) == 0 {
		if p := strings.TrimSpace(req.Path); p != "" {
			req.Paths = []string{p}
		}
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 180*time.Second)
	defer cancel()
	tasks, err := h.svc.Tasks.Create(ctx, service.TaskMeta{
		Paths:      req.Paths,
		Params:     req.Params,
		Suffix:     req.Suffix,
		SaveTarget: req.SaveTarget,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"tasks": tasks,
		"total": len(tasks),
		"stats": h.svc.Tasks.Stats(),
	})
}

// outputTargets GET /api/tasks/output-targets?path=&suffix=&container=
// 返回可选保存位置的绝对路径与可写性。
func (h *taskHandler) outputTargets(c *gin.Context) {
	p := c.Query("path")
	if strings.TrimSpace(p) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 path 参数"})
		return
	}
	container := c.DefaultQuery("container", "mp4")
	c.JSON(http.StatusOK, gin.H{
		"targets": h.svc.Tasks.OutputTargets(p, c.Query("suffix"), container),
	})
}

// list GET /api/tasks
func (h *taskHandler) list(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"tasks": h.svc.Tasks.List(),
		"stats": h.svc.Tasks.Stats(),
	})
}

// stats GET /api/tasks/stats
func (h *taskHandler) stats(c *gin.Context) {
	c.JSON(http.StatusOK, h.svc.Tasks.Stats())
}

// get GET /api/tasks/:id
func (h *taskHandler) get(c *gin.Context) {
	t, ok := h.svc.Tasks.Get(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "任务不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"task": t})
}

// cancel DELETE /api/tasks/:id
func (h *taskHandler) cancel(c *gin.Context) {
	if err := h.svc.Tasks.Cancel(c.Param("id")); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// cleanFinished DELETE /api/tasks  body 可选 {"cancel_running": true}
func (h *taskHandler) cleanFinished(c *gin.Context) {
	var req struct {
		CancelRunning bool `json:"cancel_running"`
	}
	// 允许空 body
	_ = c.ShouldBindJSON(&req)
	cancelled := 0
	if req.CancelRunning {
		cancelled = h.svc.Tasks.CancelAll()
	}
	n := h.svc.Tasks.CleanFinished()
	c.JSON(http.StatusOK, gin.H{"ok": true, "removed": n, "cancelled": cancelled})
}

// removeOutput DELETE /api/tasks/:id/output
func (h *taskHandler) removeOutput(c *gin.Context) {
	if err := h.svc.Tasks.RemoveOutput(c.Param("id")); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "stats": h.svc.Tasks.Stats()})
}

// download GET /api/tasks/:id/download
func (h *taskHandler) download(c *gin.Context) {
	t, ok := h.svc.Tasks.Get(c.Param("id"))
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "任务不存在"})
		return
	}
	if t.Status != service.TaskDone {
		c.JSON(http.StatusBadRequest, gin.H{"error": "任务尚未完成，无法下载"})
		return
	}
	if t.Output == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "输出文件不存在"})
		return
	}
	name := t.OutputName
	if name == "" {
		name = filepath.Base(t.Output)
	}
	// 正确的视频 MIME：以 application/octet-stream 提供会让 Chrome 视为
	// “可疑二进制”并在 HTTP（非 HTTPS）页面上拦截下载。视频类型则放行。
	if ct := mimeTypeForExt(filepath.Ext(t.Output)); ct != "" {
		c.Header("Content-Type", ct)
	} else {
		c.Header("Content-Type", "application/octet-stream")
	}
	// RFC 5987 编码中文文件名；纯 ASCII 名同时附带 filename= 兜底
	encoded := "filename*=UTF-8''" + url.PathEscape(name)
	cd := "attachment; " + encoded
	if isASCII(name) {
		cd = `attachment; filename="` + name + `"; ` + encoded
	}
	c.Header("Content-Disposition", cd)
	http.ServeFile(c.Writer, c.Request, t.Output)
}
