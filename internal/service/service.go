// Package service 承载业务逻辑：媒体浏览与探测、编码器能力查询、
// 转码预设管理与转码任务调度。
package service

import (
	"go.uber.org/zap"

	"github.com/meimolihan/fan-video-tr/internal/config"
	"github.com/meimolihan/fan-video-tr/internal/ffmpeg"
)

// Service 聚合全部业务能力。
type Service struct {
	Cfg      *config.Config
	FFmpeg   *ffmpeg.Client
	Log      *zap.SugaredLogger
	Media    *MediaService
	Tasks    *TaskManager
	Profiles *ProfileManager
}

// New 构建 Service。
func New(cfg *config.Config, ffc *ffmpeg.Client, log *zap.SugaredLogger) *Service {
	s := &Service{
		Cfg:    cfg,
		FFmpeg: ffc,
		Log:    log,
	}
	s.Media = NewMediaService(cfg, ffc, log)
	s.Tasks = NewTaskManager(cfg, ffc, s.Media, log)
	pm, err := NewProfileManager(cfg.App.DataDir, log)
	if err != nil {
		log.Errorf("初始化转码预设失败（已用内置模板兜底）: %v", err)
	}
	s.Profiles = pm
	return s
}
