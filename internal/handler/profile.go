package handler

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/meimolihan/fan-video-tr/internal/service"
)

type profileHandler struct {
	svc *service.Service
	log *zap.SugaredLogger
}

// list GET /api/profiles
func (h *profileHandler) list(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"profiles": h.svc.Profiles.List()})
}

// save POST /api/profiles  body: {name, description, params}
// 同名保存视为更新；内置模板名不可覆盖。
func (h *profileHandler) save(c *gin.Context) {
	var p service.Profile
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求参数无效: " + err.Error()})
		return
	}
	list, err := h.svc.Profiles.Save(p)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "profiles": list})
}

// delete DELETE /api/profiles/:name
func (h *profileHandler) delete(c *gin.Context) {
	name, err := url.PathUnescape(c.Param("name"))
	if err != nil {
		name = c.Param("name")
	}
	list, err := h.svc.Profiles.Delete(strings.TrimSpace(name))
	if err != nil {
		if errors.Is(err, service.ErrProfileNotFound) {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "profiles": list})
}

// reset DELETE /api/profiles  恢复出厂预设（清空全部自定义模板）
func (h *profileHandler) reset(c *gin.Context) {
	list, err := h.svc.Profiles.Reset()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true, "profiles": list})
}
