// Package uistate 持久化 picker 的 UI 形态记忆——目前只有「哪些 session 处于展开态」。
//
// 与 claude/attention 刻意分成两个文件：attention.json 是「有活干完了等你看」的业务状态，
// 本包的 picker-ui.json 是纯 UI 偏好。两者生命周期、损坏后的兜底语义都不同，
// 混在一个文件里会让任一方的写失败连坐另一方。
//
// 全部读路径都是 fail-soft：文件不存在 / 读失败 / JSON 损坏 / version 不认识
// 一律当成空集合，绝不返回 error、绝不打断 picker 启动。
package uistate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// fileVersion 是 picker-ui.json 的当前格式版本。读到别的版本一律当空集合处理，
// 不做向前兼容的猜测解析——UI 偏好丢了只是少记一次展开态，代价远小于解析错。
const fileVersion = 1

type fileShape struct {
	Version  int      `json:"version"`
	Expanded []string `json:"expanded"`
}

// Store 读写展开集合。并发安全：一个进程内单实例使用，sync.Mutex 兜底。
type Store struct {
	mu   sync.Mutex
	path string
}

// New 用给定路径创建 Store。路径通常来自 DefaultPath()。
func New(path string) *Store {
	return &Store{path: path}
}

// DefaultPath 返回 picker-ui.json 的标准位置：$XDG_STATE_HOME/cc-sesh/picker-ui.json
// 兜底为 $HOME/.local/state/cc-sesh/picker-ui.json。
// 与 attention.DefaultPath() 同目录但**不同文件**。
func DefaultPath() (string, error) {
	if v := os.Getenv("XDG_STATE_HOME"); v != "" {
		return filepath.Join(v, "cc-sesh", "picker-ui.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home dir: %w", err)
	}
	return filepath.Join(home, ".local", "state", "cc-sesh", "picker-ui.json"), nil
}

// LoadExpanded 返回手动展开的 session 名集合。
// 任何读取 / 解析失败都退化成空集合（不返回 error）——picker 从全折叠开始即可。
func (s *Store) LoadExpanded() map[string]struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]struct{})
	data, err := os.ReadFile(s.path)
	if err != nil {
		return out
	}
	var shape fileShape
	if err := json.Unmarshal(data, &shape); err != nil {
		return out
	}
	if shape.Version != fileVersion {
		return out
	}
	for _, name := range shape.Expanded {
		out[name] = struct{}{}
	}
	return out
}

// SaveExpanded 把展开集合原子写入磁盘：排序去重 → 建目录 → 写 .tmp → Rename。
//
// 空集合也照写不误：用户折起最后一个 session 时必须真的覆盖旧内容，
// 否则下次打开会读回已经被折起的旧展开态。
func (s *Store) SaveExpanded(names []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("mkdir state dir: %w", err)
	}

	// 排序去重：让文件 diff 友好，也让「同一集合」总是产出同一份字节
	seen := make(map[string]struct{}, len(names))
	expanded := make([]string, 0, len(names))
	for _, n := range names {
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		expanded = append(expanded, n)
	}
	sort.Strings(expanded)

	data, err := json.MarshalIndent(fileShape{
		Version:  fileVersion,
		Expanded: expanded,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal picker ui state: %w", err)
	}

	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}
