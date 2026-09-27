// Package handler 提供 HTTP API 与前端静态资源路由（基于 gin）。
package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/meimolihan/fan-video-tr/internal/config"
	"github.com/meimolihan/fan-video-tr/internal/embedded"
	"github.com/meimolihan/fan-video-tr/internal/service"
	"github.com/meimolihan/fan-video-tr/internal/version"
)

// Handler 聚合 HTTP 处理器。
type Handler struct {
	cfg     *config.Config
	svc     *service.Service
	log     *zap.SugaredLogger
	webRoot http.FileSystem
	assetV  string

	media   *mediaHandler
	task    *taskHandler
	profile *profileHandler
	meta    *metaHandler
}

// assetVersionPlaceholder 是 index.html 内静态资源 URL 上的版本占位符，
// 提供 index.html 时替换为资源内容哈希（见 computeAssetVersion）。
const assetVersionPlaceholder = "__ASSET_V__"

// New 构建 Handler。
func New(cfg *config.Config, svc *service.Service, log *zap.SugaredLogger) *Handler {
	h := &Handler{
		cfg:     cfg,
		svc:     svc,
		log:     log,
		webRoot: embedded.Resolve(cfg.App.WebDir),
		media:   &mediaHandler{svc: svc, log: log},
		task:    &taskHandler{svc: svc, log: log},
		profile: &profileHandler{svc: svc, log: log},
		meta:    &metaHandler{svc: svc, log: log},
	}
	h.assetV = h.computeAssetVersion()
	return h
}

// computeAssetVersion 以 index.html 与 css/js 的内容哈希作为前端资源版本号。
// 资源内容变化 → 版本变化 → 浏览器请求的 URL 变化，从而不会命中旧缓存；
// /css、/js 的长缓存（immutable）也因此变得安全。
// 读取失败时退化为进程内唯一值，保证 URL 依然不会复用。
func (h *Handler) computeAssetVersion() string {
	sum := sha256.New()
	for _, name := range []string{"index.html", "css/style.css", "js/app.js"} {
		f, err := h.webRoot.Open(name)
		if err != nil {
			h.log.Warnf("计算前端资源版本失败（%s）: %v", name, err)
			return strconv.FormatInt(time.Now().UnixNano(), 36)
		}
		_, err = io.Copy(sum, f)
		f.Close()
		if err != nil {
			h.log.Warnf("计算前端资源版本失败（%s）: %v", name, err)
			return strconv.FormatInt(time.Now().UnixNano(), 36)
		}
	}
	return hex.EncodeToString(sum.Sum(nil))[:12]
}

// Router 构建并返回 gin 路由。
func (h *Handler) Router() *gin.Engine {
	if !h.cfg.App.Debug {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Recovery(), requestLogger(h.log))

	api := r.Group("/api")
	{
		api.GET("/health", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"status": "ok"})
		})
		api.GET("/version", h.meta.version)

		// 编码器能力 / 选项字典（前端表单的唯一数据源）
		api.GET("/capabilities", h.meta.capabilities)
		api.GET("/options", h.meta.options)

		api.GET("/media/dir", h.media.listDir)
		api.POST("/media/probe", h.media.probeBatch)
		api.GET("/media/info", h.media.info)
		api.GET("/media/stream", h.media.stream)
		api.GET("/media/thumb", h.media.thumb)

		api.GET("/tasks", h.task.list)
		api.POST("/tasks", h.task.create)
		api.DELETE("/tasks", h.task.cleanFinished)
		// 静态段须先于 /tasks/:id 注册，避免被通配吞掉
		api.GET("/tasks/stats", h.task.stats)
		api.GET("/tasks/output-targets", h.task.outputTargets)
		api.GET("/tasks/:id", h.task.get)
		api.DELETE("/tasks/:id", h.task.cancel)
		api.GET("/tasks/:id/download", h.task.download)
		api.DELETE("/tasks/:id/output", h.task.removeOutput)

		api.GET("/profiles", h.profile.list)
		api.POST("/profiles", h.profile.save)
		api.DELETE("/profiles", h.profile.reset)
		api.DELETE("/profiles/:name", h.profile.delete)
	}

	h.serveStatic(r)
	return r
}

// requestLogger 精简的访问日志：跳过静态资源与健康检查，避免刷屏。
func requestLogger(log *zap.SugaredLogger) gin.HandlerFunc {
	return func(c *gin.Context) {
		p := c.Request.URL.Path
		if p == "/api/health" || p == "/favicon.svg" ||
			strings.HasPrefix(p, "/css/") || strings.HasPrefix(p, "/js/") || strings.HasPrefix(p, "/assets/") {
			c.Next()
			return
		}
		start := nowFunc()
		c.Next()
		log.Debugf("%s %s → %d（%s）", c.Request.Method, p, c.Writer.Status(), since(start))
	}
}

// serveStatic 提供前端静态资源：/ 与 asset 路径从 webRoot（磁盘或内嵌）读取，
// 未匹配到文件的路径回退到 index.html（单页应用友好）。
func (h *Handler) serveStatic(r *gin.Engine) {
	r.GET("/favicon.svg", h.favicon)
	routes := []string{"/css/", "/js/", "/assets/"}
	r.NoRoute(func(c *gin.Context) {
		p := c.Request.URL.Path
		clean := path.Clean("/" + p)
		if !strings.HasPrefix(clean, "/api/") {
			for _, prefix := range routes {
				if strings.HasPrefix(clean, prefix) {
					h.serveFile(c, strings.TrimPrefix(clean, "/"), "public, max-age=31536000, immutable")
					return
				}
			}
			h.serveIndex(c)
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": "接口不存在"})
	})
}

// serveIndex 提供 SPA 入口页：把资源 URL 上的版本占位符替换为内容哈希，
// 使前端资源在重新构建后自动获得新 URL，无需用户手动清缓存。
func (h *Handler) serveIndex(c *gin.Context) {
	f, err := h.webRoot.Open("/index.html")
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		c.Status(http.StatusNotFound)
		return
	}
	raw, err := io.ReadAll(f)
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	page := bytes.ReplaceAll(raw, []byte(assetVersionPlaceholder), []byte(h.assetV))
	if tag := etagOf(page); setETag(c, tag) {
		return
	}
	c.Header("Cache-Control", "no-cache")
	c.Data(http.StatusOK, "text/html; charset=utf-8", page)
}

// etagOf 计算内容 ETag（sha256 前 16 位十六进制）。
func etagOf(b []byte) string {
	sum := sha256.Sum256(b)
	return `"` + hex.EncodeToString(sum[:])[:16] + `"`
}

// setETag 写入 ETag 并在客户端缓存仍然有效时直接返回 304。
func setETag(c *gin.Context, tag string) bool {
	c.Header("ETag", tag)
	for _, v := range strings.Split(c.GetHeader("If-None-Match"), ",") {
		v = strings.TrimSpace(v)
		if v == "*" || strings.TrimPrefix(v, "W/") == tag {
			c.Status(http.StatusNotModified)
			return true
		}
	}
	return false
}

// favicon 提供站点图标（替换免重编译、免清缓存生效）。
// 解析优先级：app.favicon 配置 > <数据目录>/favicon.svg > web_dir/内嵌默认图标。
func (h *Handler) favicon(c *gin.Context) {
	const cacheControl = "no-cache"

	if p := h.cfg.FaviconPath(); p != "" {
		if h.serveDiskFile(c, p, cacheControl) {
			return
		}
	}
	if p := filepath.Join(h.cfg.App.DataDir, "favicon.svg"); h.serveDiskFile(c, p, cacheControl) {
		return
	}
	h.serveFile(c, "favicon.svg", cacheControl)
}

// serveDiskFile 从磁盘提供单个文件；文件缺失返回 false。
func (h *Handler) serveDiskFile(c *gin.Context, p, cacheControl string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return false
	}
	h.serveFileInfo(c, f, info, cacheControl)
	return true
}

// serveFile 从 webRoot 提供单个文件（支持缓存头）。
func (h *Handler) serveFile(c *gin.Context, name, cacheControl string) {
	f, err := h.webRoot.Open(path.Clean("/" + name))
	if err != nil {
		c.Status(http.StatusNotFound)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		c.Status(http.StatusNotFound)
		return
	}
	h.serveFileInfo(c, f, info, cacheControl)
}

// serveFileInfo 写缓存头并输出文件内容（Content-Type 依据真实文件名扩展名推导）。
func (h *Handler) serveFileInfo(c *gin.Context, f io.ReadSeeker, info fs.FileInfo, cacheControl string) {
	if cacheControl != "" {
		c.Header("Cache-Control", cacheControl)
	}
	http.ServeContent(c.Writer, c.Request, info.Name(), info.ModTime(), f)
}

// ==================== 通用工具 ====================

// mimeTypeForExt 简易 MIME 映射（浏览器播放与下载用）。
func mimeTypeForExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".mp4", ".m4v", ".mp4v", ".f4v":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	case ".ts", ".m2ts", ".mts":
		return "video/mp2t"
	case ".mkv":
		return "video/x-matroska"
	case ".avi":
		return "video/x-msvideo"
	case ".flv":
		return "video/x-flv"
	case ".wmv":
		return "video/x-ms-wmv"
	case ".mpg", ".mpeg":
		return "video/mpeg"
	case ".3gp":
		return "video/3gpp"
	case ".rm", ".rmvb":
		return "video/vnd.rn-realvideo"
	case ".vob":
		return "video/dvd"
	case ".ogv":
		return "video/ogg"
	case ".m2v":
		return "video/mpeg"
	default:
		return ""
	}
}

// isASCII 判断字符串是否全部为 ASCII 字符。
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// versionString 便于测试替换的版本读取入口。
var versionString = version.Current
