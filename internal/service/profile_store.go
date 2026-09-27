package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"go.uber.org/zap"
)

// ErrProfileNotFound 预设不存在。
var ErrProfileNotFound = fmt.Errorf("预设不存在")

// ProfileManager 转码预设（模板）管理器：内置模板 + 用户自定义模板，
// 自定义部分持久化到 <数据目录>/profiles.json。
type ProfileManager struct {
	mu       sync.RWMutex
	path     string
	profiles []Profile
	log      *zap.SugaredLogger
}

// NewProfileManager 创建预设管理器并加载磁盘数据。
// 文件缺失 / 损坏时回退到内置模板，保证界面始终可用。
func NewProfileManager(dataDir string, log *zap.SugaredLogger) (*ProfileManager, error) {
	m := &ProfileManager{
		path: filepath.Join(dataDir, "profiles.json"),
		log:  log,
	}
	if err := m.load(); err != nil {
		if log != nil {
			log.Errorf("加载转码预设失败（已用内置模板兜底）: %v", err)
		}
		m.profiles = builtinProfiles()
	}
	return m, nil
}

// load 读取磁盘上的自定义模板并与内置模板合并。
func (m *ProfileManager) load() error {
	data, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			m.profiles = builtinProfiles()
			return nil
		}
		return err
	}
	var custom []Profile
	if err := json.Unmarshal(data, &custom); err != nil {
		// 备份损坏文件后重建，避免用户手工改坏 JSON 后无法启动
		_ = os.Rename(m.path, m.path+".bak")
		m.profiles = builtinProfiles()
		return nil
	}

	list := builtinProfiles()
	// 自定义模板若与内置同名则覆盖（保留 builtin 标记）
	index := make(map[string]int, len(list))
	for i, p := range list {
		index[p.Name] = i
	}
	for _, p := range custom {
		p.Name = strings.TrimSpace(p.Name)
		if p.Name == "" {
			continue
		}
		if i, ok := index[p.Name]; ok {
			list[i].Description = p.Description
			list[i].Params = p.Params
			continue
		}
		index[p.Name] = len(list)
		list = append(list, p)
	}
	m.profiles = list
	return nil
}

// persist 原子写入自定义模板（内置模板不落盘）。
func (m *ProfileManager) persist() error {
	builtin := map[string]bool{}
	for _, p := range builtinProfiles() {
		builtin[p.Name] = true
	}
	custom := make([]Profile, 0, len(m.profiles))
	for _, p := range m.profiles {
		if builtin[p.Name] {
			continue
		}
		p.Builtin = false
		custom = append(custom, p)
	}
	if len(custom) == 0 {
		// 无自定义模板时清理残留文件
		if err := os.Remove(m.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	data, err := json.MarshalIndent(custom, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

// List 返回全部预设（内置在前，自定义按名称排序）。
func (m *ProfileManager) List() []Profile {
	m.mu.RLock()
	defer m.mu.RUnlock()
	list := make([]Profile, len(m.profiles))
	copy(list, m.profiles)
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Builtin != list[j].Builtin {
			return list[i].Builtin
		}
		return list[i].Name < list[j].Name
	})
	return list
}

// Get 按名称获取预设。
func (m *ProfileManager) Get(name string) (Profile, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, p := range m.profiles {
		if p.Name == name {
			return p, true
		}
	}
	return Profile{}, false
}

// Save 新增或更新预设（同名覆盖）。内置模板名不允许覆盖。
func (m *ProfileManager) Save(p Profile) ([]Profile, error) {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" {
		return nil, fmt.Errorf("预设名称不能为空")
	}
	if len(p.Name) > 40 {
		return nil, fmt.Errorf("预设名称过长（最多 40 字符）")
	}
	for _, b := range builtinProfiles() {
		if b.Name == p.Name {
			return nil, fmt.Errorf("预设 %q 为内置模板，不可覆盖", p.Name)
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for i, cur := range m.profiles {
		if cur.Name == p.Name {
			m.profiles[i].Description = p.Description
			m.profiles[i].Params = p.Params
			return copyList(m.profiles), m.persist()
		}
	}
	p.Builtin = false
	m.profiles = append(m.profiles, p)
	return copyList(m.profiles), m.persist()
}

// Delete 删除自定义预设；内置模板不可删除。
func (m *ProfileManager) Delete(name string) ([]Profile, error) {
	for _, b := range builtinProfiles() {
		if b.Name == name {
			return nil, fmt.Errorf("预设 %q 为内置模板，不可删除", name)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, cur := range m.profiles {
		if cur.Name == name {
			m.profiles = append(m.profiles[:i], m.profiles[i+1:]...)
			return copyList(m.profiles), m.persist()
		}
	}
	return nil, ErrProfileNotFound
}

// Reset 恢复出厂预设（清空全部自定义模板）。
func (m *ProfileManager) Reset() ([]Profile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.profiles = builtinProfiles()
	return copyList(m.profiles), m.persist()
}

func copyList(list []Profile) []Profile {
	out := make([]Profile, len(list))
	copy(out, list)
	return out
}
