package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/meimolihan/fan-video-tr/internal/config"
	"github.com/meimolihan/fan-video-tr/internal/ffmpeg"
	"github.com/meimolihan/fan-video-tr/internal/handler"
	"github.com/meimolihan/fan-video-tr/internal/logger"
	"github.com/meimolihan/fan-video-tr/internal/service"
	"github.com/meimolihan/fan-video-tr/internal/version"
)

// runServer 启动 Web 服务。
func runServer(dataFlag string, portFlag *int, mediaFlag, outputFlag string, workerFlag *int) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}

	// 命令行参数优先级最高
	if dataFlag != "" {
		cfg.App.DataDir = filepath.Clean(dataFlag)
	}
	if portFlag != nil {
		cfg.App.Port = *portFlag
	}
	if mediaFlag != "" {
		cfg.App.MediaDir = filepath.Clean(mediaFlag)
	}
	if outputFlag != "" {
		cfg.App.OutputDir = filepath.Clean(outputFlag)
	}
	if workerFlag != nil && *workerFlag > 0 {
		cfg.App.Worker = *workerFlag
	}
	for _, dir := range []string{cfg.App.DataDir, cfg.OutputDir()} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("创建目录 %s 失败: %w", dir, err)
		}
	}

	log, err := logger.New(cfg)
	if err != nil {
		return err
	}
	defer log.Sync() //nolint:errcheck
	slog := log.Sugar()

	// FFmpeg 自检
	ffc, err := ffmpeg.New(ffmpeg.Options{
		FFmpegBin:    cfg.FFmpegBin(),
		FFprobeBin:   cfg.FFprobeBin(),
		Threads:      cfg.FFmpeg.Threads,
		Preset:       cfg.FFmpeg.Preset,
		CRF:          cfg.FFmpeg.CRF,
		AudioBitrate: cfg.FFmpeg.AudioBitrate,
		Accel:        cfg.FFmpeg.Accel,
		VAAPIDevice:  cfg.FFmpeg.VAAPIDevice,
		HWDecode:     cfg.FFmpeg.HWDecode,
	})
	if err != nil {
		slog.Errorf("FFmpeg 环境检查失败: %v（请安装 ffmpeg / ffprobe，或在配置中指定路径）", err)
		fmt.Fprintln(os.Stderr, "FFmpeg 环境检查失败: ", err)
		fmt.Fprintln(os.Stderr, "请安装 ffmpeg：  Debian/Ubuntu: apt install ffmpeg | Alpine: apk add ffmpeg | CentOS: yum install ffmpeg")
		return err
	}
	if vstr, err := ffc.EnsureFFmpegAvailable(context.Background()); err == nil {
		slog.Infof("%s", vstr)
	}

	// 硬件加速探测（失败不阻塞启动，仅影响可选加速方式）
	if cfg.FFmpeg.DetectAccel {
		caps := ffc.Detect(context.Background())
		slog.Infof("编码能力: %s", summarizeCaps(caps))
	}

	svc := service.New(cfg, ffc, slog)
	h := handler.New(cfg, svc, slog)
	router := h.Router()

	addr := fmt.Sprintf(":%d", cfg.App.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: 15 * time.Second,
	}

	// PID 文件（便于 status / uninstall 检查）
	writePidFile()

	slog.Infof("fan-video-tr %s 启动成功，监听 %s（数据目录: %s，输出目录: %s，转码并发: %d）",
		version.Current(), addr, cfg.App.DataDir, cfg.OutputDir(), svc.Tasks.Workers())
	fmt.Printf("fan-video-tr %s 已启动：http://127.0.0.1:%d\n", version.Current(), cfg.App.Port)

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Errorf("HTTP 服务异常退出: %v", err)
			_ = os.Remove(defaultPidFile)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("收到退出信号，正在优雅关闭 ...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Warnf("优雅关闭超时: %v", err)
	}
	_ = os.Remove(defaultPidFile)
	slog.Info("服务已停止")
	return nil
}

// summarizeCaps 生成一行编码能力摘要（启动日志用）。
func summarizeCaps(caps *ffmpeg.Capabilities) string {
	var hw []string
	for _, a := range caps.HWAccel {
		if a.ID != "none" && a.Available {
			hw = append(hw, a.ID)
		}
	}
	if len(hw) == 0 {
		hw = append(hw, "无（仅软件编码）")
	}
	return fmt.Sprintf("软件编码族 %v，可用硬件加速 %v", caps.SoftwareCodecs, hw)
}

// writePidFile 将当前进程 PID 写入默认 PID 文件。
func writePidFile() {
	pid := os.Getpid()
	_ = os.WriteFile(defaultPidFile, []byte(fmt.Sprintf("%d\n", pid)), 0644)
}
