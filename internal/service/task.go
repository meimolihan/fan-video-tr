package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/meimolihan/fan-video-tr/internal/config"
	"github.com/meimolihan/fan-video-tr/internal/ffmpeg"
)

// 任务状态
const (
	TaskQueued    = "queued"
	TaskRunning   = "running"
	TaskDone      = "done"
	TaskFailed    = "failed"
	TaskCancelled = "cancelled"
)

// 转码结果的保存目标
const (
	// SaveTargetDefault 保存到软件数据目录（app.output_dir 解析结果，默认值）
	SaveTargetDefault = "default"
	// SaveTargetSource 保存到原视频所在目录（仅修改文件名，不改变目录）
	SaveTargetSource = "source"
)

// DefaultNameSuffix 输出文件名的默认后缀。
const DefaultNameSuffix = "_转码"

// Task 单个转码任务。
type Task struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	// Name 源文件名（不含目录）
	Name string `json:"name"`
	// Output 输出文件绝对路径
	Output string `json:"output"`
	// OutputName 输出文件名
	OutputName string `json:"output_name"`
	// SaveTarget 保存目标
	SaveTarget string `json:"save_target"`
	// BatchID 同批次标识（单文件创建时为该任务自身 ID）
	BatchID string `json:"batch_id"`

	// Params 转码参数快照
	Params TranscodeParams `json:"params"`
	// Summary 参数摘要（展示用）
	Summary string `json:"summary"`
	// Encoder 实际使用的 ffmpeg 编码器
	Encoder string `json:"encoder"`
	// Accel 实际使用的硬件加速方式
	Accel string `json:"accel"`
	// ResolutionLabel 实际输出分辨率描述
	ResolutionLabel string `json:"resolution_label"`
	// CommandLine 实际执行的 ffmpeg 命令行（仅单遍，用于排障）
	CommandLine string `json:"command_line,omitempty"`

	// SourceSize / SourceDuration 源文件信息
	SourceSize     int64   `json:"source_size"`
	SourceDuration float64 `json:"source_duration"`
	// SourceWidth / SourceHeight 源分辨率
	SourceWidth  int `json:"source_width"`
	SourceHeight int `json:"source_height"`
	// OutputWidth / OutputHeight 目标分辨率（估算值，可能因保持比例而不同）
	OutputWidth  int `json:"output_width"`
	OutputHeight int `json:"output_height"`
	// EstimateSize 预估输出体积
	EstimateSize int64 `json:"estimate_size"`

	Status   string  `json:"status"`
	Progress float64 `json:"progress"`
	// Speed 实时速度（如 "12.34x"）
	Speed string `json:"speed,omitempty"`
	// FPS 实时编码帧率
	FPS float64 `json:"fps"`
	// EncodedSeconds 已编码秒数
	EncodedSeconds float64 `json:"encoded_seconds"`
	// ElapsedSeconds 已耗时（秒）
	ElapsedSeconds float64 `json:"elapsed_seconds"`
	// OutputSize 实际输出体积
	OutputSize int64     `json:"output_size"`
	Error      string    `json:"error,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	// StartedAt / FinishedAt 为 nil 表示尚未开始 / 尚未结束
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`

	mu     sync.RWMutex
	cancel context.CancelFunc
	jobID  string
}

// snapshot 返回加锁读取的任务副本（供 JSON 序列化，避免数据竞争）。
// 逐字段复制以避免复制内部互斥量。
func (t *Task) snapshot() *Task {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return &Task{
		ID:              t.ID,
		Path:            t.Path,
		Name:            t.Name,
		Output:          t.Output,
		OutputName:      t.OutputName,
		SaveTarget:      t.SaveTarget,
		BatchID:         t.BatchID,
		Params:          t.Params,
		Summary:         t.Summary,
		Encoder:         t.Encoder,
		Accel:           t.Accel,
		ResolutionLabel: t.ResolutionLabel,
		CommandLine:     t.CommandLine,
		SourceSize:      t.SourceSize,
		SourceDuration:  t.SourceDuration,
		SourceWidth:     t.SourceWidth,
		SourceHeight:    t.SourceHeight,
		OutputWidth:     t.OutputWidth,
		OutputHeight:    t.OutputHeight,
		EstimateSize:    t.EstimateSize,
		Status:          t.Status,
		Progress:        t.Progress,
		Speed:           t.Speed,
		FPS:             t.FPS,
		EncodedSeconds:  t.EncodedSeconds,
		ElapsedSeconds:  t.ElapsedSeconds,
		OutputSize:      t.OutputSize,
		Error:           t.Error,
		CreatedAt:       t.CreatedAt,
		UpdatedAt:       t.UpdatedAt,
		StartedAt:       t.StartedAt,
		FinishedAt:      t.FinishedAt,
	}
}

func (t *Task) setStatus(status string, errMsg string) {
	t.mu.Lock()
	t.Status = status
	t.Error = errMsg
	t.UpdatedAt = time.Now()
	t.mu.Unlock()
}

// TaskMeta 创建转码任务的入参。
type TaskMeta struct {
	// Paths 待转码的源文件列表（支持批量）
	Paths []string
	// Params 转码参数
	Params TranscodeParams
	// Suffix 输出文件名后缀（留空使用 DefaultNameSuffix，"none" 表示不追加）
	Suffix string
	// SaveTarget 保存目标（default / source）
	SaveTarget string
	// SkipProbe 仅按源文件推导参数，不做 ffprobe（批量预检用）
	SkipProbe bool
}

// OutputTarget 描述一个可选的输出保存位置。
type OutputTarget struct {
	// ID 保存目标标识（default / source）
	ID string `json:"id"`
	// Label 前端展示名
	Label string `json:"label"`
	// Dir 目标目录绝对路径
	Dir string `json:"dir"`
	// File 按当前文件名推算出的输出文件绝对路径（不可用时为空）
	File string `json:"file,omitempty"`
	// Available 目录是否存在且可写
	Available bool `json:"available"`
	// Reason 不可用原因（Available 为 false 时给出）
	Reason string `json:"reason,omitempty"`
}

// TaskStats 队列统计。
type TaskStats struct {
	Total     int `json:"total"`
	Queued    int `json:"queued"`
	Running   int `json:"running"`
	Done      int `json:"done"`
	Failed    int `json:"failed"`
	Cancelled int `json:"cancelled"`
	Active    int `json:"active"`
	Workers   int `json:"workers"`
}

// TaskManager 内存任务调度器：并发上限受 app.worker 限制，
// 超出部分进入等待队列按创建顺序执行。
type TaskManager struct {
	cfg   *config.Config
	ffc   *ffmpeg.Client
	log   *zap.SugaredLogger
	media *MediaService

	mu     sync.RWMutex
	tasks  map[string]*Task
	queue  []*Task
	active int
}

// NewTaskManager 创建任务管理器。
func NewTaskManager(cfg *config.Config, ffc *ffmpeg.Client, media *MediaService, log *zap.SugaredLogger) *TaskManager {
	return &TaskManager{
		cfg:   cfg,
		ffc:   ffc,
		media: media,
		log:   log,
		tasks: make(map[string]*Task),
	}
}

// Workers 返回当前配置的并发上限（至少为 1）。
func (m *TaskManager) Workers() int {
	w := m.cfg.App.Worker
	if w < 1 {
		return 1
	}
	return w
}

// Create 创建并调度一批转码任务。
// 单个文件探测失败不会中断整批，失败项以 failed 状态直接返回给调用方。
func (m *TaskManager) Create(ctx context.Context, meta TaskMeta) ([]*Task, error) {
	paths := make([]string, 0, len(meta.Paths))
	seen := map[string]struct{}{}
	for _, p := range meta.Paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		clean := filepath.Clean(p)
		if _, dup := seen[clean]; dup {
			continue
		}
		seen[clean] = struct{}{}
		paths = append(paths, clean)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("未选择任何视频文件")
	}
	if len(paths) > 500 {
		return nil, fmt.Errorf("单次最多提交 500 个文件，当前 %d 个", len(paths))
	}

	// 参数归一化（先于逐文件探测，快速失败）
	opts := meta.Params.ToOptions(m.ffc.DefaultThreads())
	caps := m.ffc.Detect(ctx)
	if err := opts.Clamp(caps, m.ffc.DefaultThreads()); err != nil {
		return nil, err
	}
	// 归一化结果回写到参数快照，保证前端展示与实际执行一致
	params := meta.Params
	params.Codec = opts.Codec
	params.Accel = opts.ActualAccel(caps)
	params.Container = opts.Container
	params.Resolution = opts.Resolution
	params.FPS = opts.FPS
	params.PixelFormat = opts.PixelFormat
	params.AudioCodec = opts.AudioCodec
	params.AudioChannels = opts.AudioChannels
	params.RateControl = opts.RateControl
	params.VideoBitrate = opts.VideoBitrate
	params.Maxrate = opts.Maxrate
	params.Preset = opts.Preset
	params.TwoPass = opts.TwoPass

	suffix := resolveSuffix(meta.Suffix, params)
	saveTarget := normalizeSaveTarget(meta.SaveTarget)
	// 编码器必须能在当前环境真实落地（例如双遍编码需要 libx264 之类的编码器），
	// 否则在这里直接拒绝，不要让任务排队后再失败
	encoder, err := opts.EncoderName(caps)
	if err != nil {
		return nil, err
	}
	accel := params.Accel
	now := time.Now()
	batchID := uuid.NewString()

	created := make([]*Task, 0, len(paths))
	var firstErr error
	for _, p := range paths {
		task, err := m.buildTask(ctx, p, batchID, params, opts, caps, suffix, saveTarget, now, encoder, accel, meta.SkipProbe)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			m.log.Warnf("创建转码任务失败 %s: %v", p, err)
			continue
		}
		created = append(created, task)
	}
	if len(created) == 0 {
		if firstErr != nil {
			return nil, firstErr
		}
		return nil, fmt.Errorf("没有可转码的视频文件")
	}
	m.log.Infof("已创建 %d 个转码任务（批次 %s，参数: %s）", len(created), batchID, params.Summary())
	return created, nil
}

// buildTask 组装单个任务对象并入队。
func (m *TaskManager) buildTask(ctx context.Context, path, batchID string, params TranscodeParams,
	opts ffmpeg.TranscodeOptions, caps *ffmpeg.Capabilities, suffix, saveTarget string,
	now time.Time, encoder, accel string, skipProbe bool) (*Task, error) {

	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("文件不可访问: %v", err)
	}
	if st.IsDir() {
		return nil, fmt.Errorf("这是一个目录")
	}

	outDir, err := m.resolveOutputDir(saveTarget, path)
	if err != nil {
		return nil, err
	}
	name := buildOutputName(path, suffix, opts.Container)
	name = uniqueName(outDir, name)
	outPath := filepath.Join(outDir, name)

	task := &Task{
		ID:         uuid.NewString(),
		Path:       path,
		Name:       filepath.Base(path),
		Output:     outPath,
		OutputName: name,
		SaveTarget: saveTarget,
		BatchID:    batchID,
		Params:     params,
		Summary:    params.Summary(),
		Encoder:    encoder,
		Accel:      accel,
		SourceSize: st.Size(),
		Status:     TaskQueued,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	task.ResolutionLabel = "原始分辨率"

	// 记录实际执行的命令行，便于界面排障
	if args := m.ffc.PreviewCommand(ctx, path, outPath, &opts); len(args) > 0 {
		task.CommandLine = "ffmpeg " + strings.Join(args, " ")
	}

	if !skipProbe {
		probeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		info, err := m.media.Info(probeCtx, path)
		if err != nil {
			return nil, fmt.Errorf("探测视频失败: %w", err)
		}
		if !info.HasVideo() {
			return nil, fmt.Errorf("文件中没有视频流")
		}
		if opts.Start >= 0 && opts.End > 0 && info.Duration > 0 && opts.End > info.Duration {
			return nil, fmt.Errorf("转码区间结束时间 %.2fs 超出视频时长 %.2fs", opts.End, info.Duration)
		}
		task.SourceDuration = info.Duration
		task.SourceWidth = info.Video.Width
		task.SourceHeight = info.Video.Height
		task.OutputWidth, task.OutputHeight = targetResolution(opts, task.SourceWidth, task.SourceHeight)
		if task.OutputWidth == 0 {
			task.OutputWidth, task.OutputHeight = task.SourceWidth, task.SourceHeight
		}
		task.ResolutionLabel = resolutionLabel(opts, task.SourceWidth, task.SourceHeight)
		est := ffmpeg.EstimateSize(&opts, info)
		task.EstimateSize = est.OutputSize
	}

	m.mu.Lock()
	m.tasks[task.ID] = task
	if m.active < m.Workers() {
		m.active++
		m.mu.Unlock()
		m.dispatch(task)
	} else {
		m.queue = append(m.queue, task)
		m.mu.Unlock()
		m.log.Infof("任务 %s 已排队（当前并发已达上限 %d）", task.ID, m.Workers())
	}
	return task, nil
}

// dispatch 启动任务执行。
func (m *TaskManager) dispatch(task *Task) {
	ctx, cancel := context.WithCancel(context.Background())
	task.mu.Lock()
	task.cancel = cancel
	task.jobID = task.ID
	task.mu.Unlock()

	go func() {
		defer func() {
			m.mu.Lock()
			m.active--
			var next *Task
			if len(m.queue) > 0 {
				next = m.queue[0]
				m.queue = m.queue[1:]
				m.active++
			}
			m.mu.Unlock()
			if next != nil {
				m.dispatch(next)
			}
		}()

		started := time.Now()
		task.mu.Lock()
		task.Status = TaskRunning
		task.Progress = 0
		task.Error = ""
		task.StartedAt = &started
		task.UpdatedAt = started
		task.mu.Unlock()

		m.log.Infof("转码任务 %s 开始（%s → %s，编码器 %s）", task.ID, task.Path, task.Output, task.Encoder)

		opts := optsOf(task)
		// 已知源时长时整段转码也能按时间给出百分比进度
		opts.KnownDuration = task.SourceDuration
		err := m.ffc.RunTranscode(ctx, task.Path, task.Output, &opts, func(p ffmpeg.Progress) {
			elapsed := time.Since(started).Seconds()
			task.mu.Lock()
			task.Progress = p.Percent
			task.Speed = p.Speed
			task.FPS = p.FPS
			task.EncodedSeconds = p.Seconds
			task.ElapsedSeconds = elapsed
			task.UpdatedAt = time.Now()
			task.mu.Unlock()
		})

		finished := time.Now()
		task.mu.Lock()
		task.ElapsedSeconds = finished.Sub(started).Seconds()
		task.FinishedAt = &finished
		task.UpdatedAt = finished
		switch {
		case ctx.Err() != nil:
			task.Status = TaskCancelled
			task.Error = "任务已取消"
		case err != nil:
			task.Status = TaskFailed
			task.Error = err.Error()
		default:
			task.Status = TaskDone
			task.Progress = 1
			if st, serr := os.Stat(task.Output); serr == nil {
				task.OutputSize = st.Size()
			}
		}
		errMsg := task.Error
		status := task.Status
		task.mu.Unlock()

		switch status {
		case TaskCancelled:
			m.log.Infof("转码任务 %s 已取消", task.ID)
		case TaskFailed:
			m.log.Errorf("转码任务 %s 失败: %s", task.ID, errMsg)
		default:
			m.log.Infof("转码任务 %s 完成，输出 %s（%.1f MB，耗时 %.0f 秒）",
				task.ID, task.Output, float64(task.OutputSize)/1024/1024, task.ElapsedSeconds)
		}
	}()
}

// optsOf 从任务快照还原 ffmpeg 层参数。
func optsOf(task *Task) ffmpeg.TranscodeOptions {
	return task.Params.ToOptions(0)
}

// List 返回全部任务快照，按创建时间倒序。
func (m *TaskManager) List() []*Task {
	m.mu.RLock()
	list := make([]*Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		list = append(list, t.snapshot())
	}
	m.mu.RUnlock()
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.After(list[j].CreatedAt) })
	return list
}

// Get 按 ID 获取任务快照。
func (m *TaskManager) Get(id string) (*Task, bool) {
	m.mu.RLock()
	t, ok := m.tasks[id]
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return t.snapshot(), true
}

// Stats 返回队列统计。
func (m *TaskManager) Stats() TaskStats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	st := TaskStats{Total: len(m.tasks), Active: m.active, Workers: m.Workers()}
	for _, t := range m.tasks {
		switch t.Status {
		case TaskQueued:
			st.Queued++
		case TaskRunning:
			st.Running++
		case TaskDone:
			st.Done++
		case TaskFailed:
			st.Failed++
		case TaskCancelled:
			st.Cancelled++
		}
	}
	return st
}

// Cancel 取消任务（运行中或排队中均可）。
func (m *TaskManager) Cancel(id string) error {
	m.mu.RLock()
	t, ok := m.tasks[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("任务不存在: %s", id)
	}
	t.mu.RLock()
	cancel := t.cancel
	status := t.Status
	t.mu.RUnlock()
	if cancel == nil {
		if status == TaskQueued {
			return fmt.Errorf("任务尚未启动，请稍候")
		}
		return nil
	}
	cancel()
	return nil
}

// Remove 移除已结束的任务记录（不删除输出文件）。
func (m *TaskManager) Remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return fmt.Errorf("任务不存在: %s", id)
	}
	switch t.Status {
	case TaskRunning, TaskQueued:
		return fmt.Errorf("任务正在执行，无法删除")
	}
	delete(m.tasks, id)
	return nil
}

// RemoveOutput 删除任务输出文件，并从队列移除该任务。
func (m *TaskManager) RemoveOutput(id string) error {
	t, ok := m.Get(id)
	if !ok {
		return fmt.Errorf("任务不存在: %s", id)
	}
	if t.Status == TaskRunning || t.Status == TaskQueued {
		return fmt.Errorf("任务正在执行，无法删除输出文件")
	}
	if t.Output != "" {
		if err := os.Remove(t.Output); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("删除输出文件失败: %v", err)
		}
	}
	return m.Remove(id)
}

// CleanFinished 清理所有终态任务记录（不删除输出文件）。
func (m *TaskManager) CleanFinished() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for id, t := range m.tasks {
		switch t.Status {
		case TaskDone, TaskFailed, TaskCancelled:
			delete(m.tasks, id)
			n++
		}
	}
	return n
}

// CancelAll 取消全部排队与运行中的任务（清空队列时使用）。
func (m *TaskManager) CancelAll() int {
	m.mu.Lock()
	queue := m.queue
	m.queue = nil
	all := make([]*Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		all = append(all, t)
	}
	m.mu.Unlock()

	n := 0
	for _, t := range queue {
		t.setStatus(TaskCancelled, "队列已清空")
		n++
	}
	for _, t := range all {
		t.mu.RLock()
		running := t.Status == TaskRunning
		cancel := t.cancel
		t.mu.RUnlock()
		if running && cancel != nil {
			cancel()
			n++
		}
	}
	return n
}

// OutputTargets 返回给定源视频的全部可选保存位置，含绝对路径与可写性。
func (m *TaskManager) OutputTargets(srcPath, suffix, container string) []OutputTarget {
	container = strings.ToLower(strings.TrimSpace(container))
	if container == "" {
		container = "mp4"
	}
	name := buildOutputName(srcPath, resolveSuffix(suffix, TranscodeParams{Container: container}), container)

	sourceDir, sourceErr := sourceDirOf(srcPath)
	defs := []struct {
		id, label string
		dir       string
		err       error
	}{
		{SaveTargetDefault, "软件数据目录", m.cfg.OutputDir(), nil},
		{SaveTargetSource, "跟随原视频保存", sourceDir, sourceErr},
	}

	targets := make([]OutputTarget, 0, len(defs))
	for _, d := range defs {
		t := OutputTarget{ID: d.id, Label: d.label, Dir: d.dir}
		// 目录无法定位时只给原因、不暴露任何路径；否则探测可写性并推算完整文件路径
		if d.err != nil {
			t.Reason = d.err.Error()
		} else if err := checkDirWritable(d.dir); err != nil {
			t.Reason = err.Error()
		} else {
			t.Available = true
			t.File = filepath.Join(d.dir, uniqueName(d.dir, name))
		}
		targets = append(targets, t)
	}
	return targets
}

// ==================== 工具函数 ====================

// resolveSuffix 归一化输出文件名后缀；空值用默认，"none" 表示不追加。
func resolveSuffix(suffix string, params TranscodeParams) string {
	suffix = strings.TrimSpace(suffix)
	if strings.EqualFold(suffix, "none") || suffix == "-" {
		return ""
	}
	if suffix == "" {
		if tag := params.Suffix(); tag != "" {
			return tag
		}
		return DefaultNameSuffix
	}
	return invalidNameChars.ReplaceAllString(suffix, "_")
}

// buildOutputName 依据源文件、后缀与容器推导输出文件名。
func buildOutputName(srcPath, suffix, container string) string {
	base := filepath.Base(srcPath)
	if idx := strings.LastIndexByte(base, '.'); idx > 0 {
		base = base[:idx]
	}
	name := base + suffix
	name = invalidNameChars.ReplaceAllString(name, "_")
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		name = "output"
	}
	if ext := ffmpeg.OutputExtension(container); !strings.HasSuffix(strings.ToLower(name), ext) {
		name += ext
	}
	return name
}

// normalizeSaveTarget 归一化保存目标；空值或非法值一律回退到默认目录。
func normalizeSaveTarget(target string) string {
	if target == SaveTargetSource {
		return SaveTargetSource
	}
	return SaveTargetDefault
}

// sourceDirOf 返回原视频所在目录。要求源路径为绝对路径，
// 以保证弹窗展示与实际写入的目录恒为绝对路径。
func sourceDirOf(srcPath string) (string, error) {
	clean := filepath.Clean(strings.TrimSpace(srcPath))
	if clean == "" || clean == "." {
		return "", fmt.Errorf("无法确定原视频所在目录")
	}
	if !filepath.IsAbs(clean) {
		return "", fmt.Errorf("原视频路径不是绝对路径")
	}
	return filepath.Dir(clean), nil
}

// resolveOutputDir 按保存目标解析输出目录。跟随原视频时取源文件所在目录，
// 目录不存在 / 非目录 / 不可写时返回可读错误，避免任务跑到一半才失败。
func (m *TaskManager) resolveOutputDir(target, srcPath string) (string, error) {
	if normalizeSaveTarget(target) != SaveTargetSource {
		return m.cfg.OutputDir(), nil
	}
	dir, err := sourceDirOf(srcPath)
	if err != nil {
		return "", fmt.Errorf("跟随原视频保存失败：%w", err)
	}
	if err := checkDirWritable(dir); err != nil {
		return "", fmt.Errorf("跟随原视频保存失败：%s（%s）", dir, err)
	}
	return dir, nil
}

// checkDirWritable 校验目录存在且可写：以临时文件探测并立即清理，
// 避免只凭权限位判断（ACL、只读挂载、网络盘等场景权限位不可靠）。
func checkDirWritable(dir string) error {
	st, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("目录不可访问")
	}
	if !st.IsDir() {
		return fmt.Errorf("不是目录")
	}
	f, err := os.CreateTemp(dir, ".fan-video-tr-probe-*")
	if err != nil {
		return fmt.Errorf("目录不可写")
	}
	// 用 defer 保证任何返回路径都不会在用户目录里留下探针文件
	defer func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}()
	return nil
}

// targetResolution 依据分辨率档位与源尺寸推算输出尺寸。
// 对齐 ffmpeg 的 force_original_aspect_ratio=decrease 行为：保持原比例、
// 不放大、并对齐到偶数（避免播放器解码异常）。源尺寸未知时返回档位原始值。
func targetResolution(opts ffmpeg.TranscodeOptions, srcW, srcH int) (int, int) {
	if opts.Resolution == "keep" {
		return 0, 0
	}
	var w, h int
	switch {
	case opts.Resolution == "custom":
		w, h, _ = ffmpeg.ParseCustomResolution(opts.CustomSize)
	default:
		if r, ok := ffmpeg.ResolutionByID(opts.Resolution); ok {
			w, h = r.Width, r.Height
		}
	}
	if w <= 0 || h <= 0 {
		return 0, 0
	}
	// 不放大小于等于源分辨率的档位
	if srcW > 0 && srcH > 0 {
		if srcW <= w && srcH <= h {
			return 0, 0
		}
		// 哪个方向溢出就收缩哪个方向
		if srcH*w > h*srcW {
			w = h * srcW / srcH
		} else {
			h = w * srcH / srcW
		}
	}
	if w%2 != 0 {
		w--
	}
	if h%2 != 0 {
		h--
	}
	if w < 2 || h < 2 {
		return 0, 0
	}
	return w, h
}

// resolutionLabel 生成输出分辨率的展示文本。
func resolutionLabel(opts ffmpeg.TranscodeOptions, srcW, srcH int) string {
	if w, h := targetResolution(opts, srcW, srcH); w > 0 && h > 0 {
		return fmt.Sprintf("%d×%d", w, h)
	}
	if opts.Resolution == "keep" || opts.Resolution == "" {
		return "原始分辨率"
	}
	return "原始分辨率"
}

var invalidNameChars = regexp.MustCompile(`[\\/:*?"<>|\r\n]`)

// uniqueName 若输出文件已存在则追加序号，避免覆盖。
func uniqueName(dir, name string) string {
	candidate := name
	for i := 1; ; i++ {
		full := filepath.Join(dir, candidate)
		if _, err := os.Stat(full); os.IsNotExist(err) {
			return candidate
		}
		ext := filepath.Ext(candidate)
		stem := strings.TrimSuffix(candidate, ext)
		candidate = fmt.Sprintf("%s_%d%s", stem, i, ext)
	}
}
