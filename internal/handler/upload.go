package handler

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/meimolihan/fan-video-tr/internal/service"
)

type uploadHandler struct {
	svc *service.Service
	log *zap.SugaredLogger
}

// upload POST /api/media/upload 上传视频（multipart 字段 file）。
// 目的目录固定为配置的上传目录（app.upload_dir），不接受客户端指定，防止越权写入。
// 直接消费 MultipartReader 流式落盘，避免 gin FormFile 先把大文件暂存临时目录的双倍磁盘开销。
func (h *uploadHandler) upload(c *gin.Context) {
	// Content-Length 快速拒绝（multipart 边界开销按 1MB 估），省得读完整个 body 再报错
	if c.Request.ContentLength > h.svc.Cfg.App.UploadMaxSize+(1<<20) {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "文件超过大小上限"})
		return
	}

	mr, err := c.Request.MultipartReader()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求格式错误，需为 multipart/form-data"})
		return
	}

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 file 上传字段"})
			return
		}
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "解析上传内容失败: " + err.Error()})
			return
		}
		if part.FormName() != "file" {
			// 其余字段（如 dir 等预留字段）读少量后丢弃，防止恶意超长字段
			_, _ = io.Copy(io.Discard, io.LimitReader(part, 1<<20))
			continue
		}

		res, saveErr := h.svc.Uploads.Save(c.Request.Context(), part.FileName(), part)
		_ = part.Close()
		if saveErr != nil {
			if c.Request.Context().Err() != nil {
				c.Status(http.StatusRequestTimeout) // 客户端取消（xhr.abort）
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": saveErr.Error()})
			return
		}
		h.log.Infof("已上传视频: %s (%d bytes)", res.Path, res.Size)
		c.JSON(http.StatusOK, gin.H{
			"ok":      true,
			"path":    res.Path,
			"name":    res.Name,
			"size":    res.Size,
			"renamed": res.Renamed,
			"dir":     h.svc.Cfg.UploadsDir(),
		})
		return
	}
}
