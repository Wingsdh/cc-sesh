package seshcli

import (
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/Wingsdh/cc-sesh/v2/claude/attention"
	"github.com/Wingsdh/cc-sesh/v2/claude/live"
	"github.com/Wingsdh/cc-sesh/v2/lister"
	"github.com/Wingsdh/cc-sesh/v2/model"
	"github.com/Wingsdh/cc-sesh/v2/picker"
	"github.com/Wingsdh/cc-sesh/v2/tmux"
)

// makeClaudeFetcher 把 lister.List + claude/live + claude/attention 串成一个 picker.FetchFunc。
//
// 支持运行时切换 mode：
//   - all     ：用调用方传入的 listerOpts（默认行为）
//   - tmux    ：仅 tmux session
//   - config  ：仅 sesh.toml 配置 session
//   - zoxide  ：仅 zoxide 历史目录
//   - find    ：用 filepath.WalkDir 在 home 下深度 ≤2 列目录（替代 fzf 路径里的 fd）
//
// 任何一步失败都不阻塞 picker —— 走 fallback（无 live / 无 attention）继续。
func makeClaudeFetcher(deps *Deps, listerOpts lister.ListOptions) picker.FetchFunc {
	return func(mode string) (model.SeshSessions, picker.Decorator, error) {
		if mode == picker.ModeFind {
			return fetchFindResults(deps)
		}

		opts := listerOpts
		switch mode {
		case picker.ModeTmux:
			opts = lister.ListOptions{Tmux: true}
		case picker.ModeConfig:
			opts = lister.ListOptions{Config: true}
		case picker.ModeZoxide:
			opts = lister.ListOptions{Zoxide: true}
		}

		sessions, err := deps.Lister.List(opts)
		if err != nil {
			return model.SeshSessions{}, nil, err
		}

		instances, instancesOk := readInstancesOrEmpty(deps.LiveReader)
		var liveByName map[string]live.Status
		liveOk := false
		if instancesOk {
			liveByName, liveOk = aggregateBySession(instances, deps.Tmux)
		}

		flags := reconcileAttention(deps.Attention, deps.Tmux, sessions, liveByName, liveOk)

		return sessions, &claudeDecorator{
			liveByName: liveByName,
			flags:      flags,
		}, nil
	}
}

// fetchFindResults 用 filepath.WalkDir 列 home 下深度 ≤2 的目录，
// 对应 fzf 路径里的 `fd -H -d 2 -t d -E .Trash . ~` 行为。
// 不依赖外部 fd，跨环境通用。
func fetchFindResults(deps *Deps) (model.SeshSessions, picker.Decorator, error) {
	home, err := deps.Os.UserHomeDir()
	if err != nil {
		return model.SeshSessions{}, picker.NoDecoration{}, err
	}
	dir := make(model.SeshSessionMap)
	index := []string{}
	const maxDepth = 2
	_ = filepath.WalkDir(home, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if path == home {
			return nil
		}
		base := filepath.Base(path)
		if base == ".Trash" || strings.HasPrefix(base, ".") && base != "." {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(home, path)
		depth := len(strings.Split(rel, string(filepath.Separator)))
		if depth > maxDepth {
			return filepath.SkipDir
		}
		key := "find:" + path
		index = append(index, key)
		dir[key] = model.SeshSession{
			Src:  "find",
			Name: path,
			Path: path,
		}
		return nil
	})
	return model.SeshSessions{Directory: dir, OrderedIndex: index}, picker.NoDecoration{}, nil
}

// readInstancesOrEmpty 返回 live 实例切片和"读取是否成功"标志。
// ok=false 表示瞬时读失败 —— 调用方应把这视作"live 数据不可信"，
// 不能据此推断"某 session 的 cc 消失了"（否则会清掉合法 tracking）。
func readInstancesOrEmpty(r *live.Reader) (items []live.Instance, ok bool) {
	if r == nil {
		// 没有 reader 配置等价于"系统里就没有 cc"，是合法 empty
		return nil, true
	}
	items, err := r.ReadInstances()
	if err != nil {
		slog.Warn("claude: live read failed", "error", err)
		return nil, false
	}
	return items, true
}

// aggregateBySession 返回 cwd→session 聚合后的状态，以及"聚合数据是否可信"。
// 任何一步失败（tmux 不在 / ListAllPanes 失败）→ ok=false，调用方不应据此清 tracking。
func aggregateBySession(instances []live.Instance, t tmux.Tmux) (map[string]live.Status, bool) {
	if t == nil {
		return nil, false
	}
	rawPanes, err := t.ListAllPanes()
	if err != nil {
		slog.Warn("claude: list panes failed", "error", err)
		return nil, false
	}
	paneInfos := make([]live.PaneInfo, 0, len(rawPanes))
	for _, p := range rawPanes {
		if p == nil {
			continue
		}
		paneInfos = append(paneInfos, live.PaneInfo{
			SessionName: p.SessionName,
			Cwd:         p.PaneCurrentPath,
		})
	}
	return live.AggregateBySession(instances, paneInfos), true
}

// reconcileAttention 调度 live 数据 + tmux client 信息更新 attention store。
//
// liveOk=false 时 live 数据不可信（瞬时读失败），这一轮只跑 suppress 路径
// （清掉当前 attach session 的已有 flag），跳过任何会动 tracking 的逻辑——
// 否则会把"看不见 cc"误判为"cc 消失了"，把合法 tracking 清掉，导致后续
// cc 真的完成时无法触发 flag。
func reconcileAttention(
	store *attention.Store,
	t tmux.Tmux,
	sessions model.SeshSessions,
	liveByName map[string]live.Status,
	liveOk bool,
) map[string]attention.Flag {
	if store == nil {
		return nil
	}
	busyByName := map[string]bool{}
	// activeNames 只在 live 可信时填充：传 nil 给 Reconcile 会跳过
	// "cc disappeared 清 tracking" 和 "GC dead session" 两段——
	// 前者在 live 不可信时必须跳过；后者顺便跳过没大碍，等下一轮再 GC。
	var activeNames []string
	if liveOk {
		activeNames = make([]string, 0, len(sessions.Directory))
	}
	for _, key := range sessions.OrderedIndex {
		s := sessions.Directory[key]
		// 只对真实 tmux session 跟踪 attention：其他 src 还没起 session 无法 attach 清除
		if s.Src != "tmux" {
			continue
		}
		if liveOk {
			activeNames = append(activeNames, s.Name)
		}
		// 只把"有 live cc 实例"的 session 写进 busyByName；
		// cc 消失的 session 不写，Store 会清掉 tracking 不触发 flag。
		st, ok := liveByName[s.Name]
		if !ok {
			continue
		}
		// busy/subagent 算「在跑活」；needs-input 不算（用户拒绝/忽略不该算"完成"）
		busyByName[s.Name] = st.Busy+st.Subagent > 0
	}

	// 取所有当前被 client attach 的 session 作为 suppress 集合：
	// 这些 session 不触发 flag、清掉已存在 flag（用户正在看）。
	var suppress []string
	if t != nil {
		if names, err := t.ListClients(); err != nil {
			slog.Warn("claude: list tmux clients failed", "error", err)
		} else {
			suppress = names
		}
	}

	if err := store.Reconcile(busyByName, activeNames, suppress); err != nil {
		slog.Warn("claude: attention reconcile failed", "error", err)
	}
	return store.Load()
}

// claudeDecorator：只对真实存在的 tmux session（src=tmux）显示徽章和 attention。
// zoxide / config / tmuxinator 模板等"还没起 session"的 entry 不显示——
// 因为徽章语义是"这个 session 内有 Claude"，没 session 时贴徽章会与
// 真实 tmux session 重复，且 attention 也无法被 attach 清除。
type claudeDecorator struct {
	liveByName map[string]live.Status
	flags      map[string]attention.Flag
}

func (d *claudeDecorator) Decorate(s model.SeshSession) picker.Decoration {
	var dec picker.Decoration
	if s.Src != "tmux" {
		return dec
	}

	if st, ok := d.liveByName[s.Name]; ok && st.Total > 0 {
		dec.Live = picker.LiveBadge{
			Total:    st.Total,
			Busy:     st.Busy,
			Subagent: st.Subagent,
			Needing:  st.Needing,
		}
	}

	if f, ok := d.flags[s.Name]; ok {
		dec.Attention = picker.AttentionBadge{
			Triggered: true,
			FirstAt:   f.FirstAt,
		}
	}
	return dec
}

// claudeDismisser 把 attention.Store 适配为 picker.Dismisser，便于 alt+d 手动清除。
type claudeDismisser struct {
	store *attention.Store
}

func (d *claudeDismisser) Dismiss(name string) error {
	if d.store == nil {
		return nil
	}
	return d.store.Ack(name)
}

// tmuxKiller 把 tmux.KillSession 适配为 picker.Killer，便于 ctrl+d 直接 kill。
// kill 后顺手 ack 一下 attention，避免幽灵 flag。
type tmuxKiller struct {
	tmux      tmux.Tmux
	attention *attention.Store
}

func (k *tmuxKiller) Kill(name string) error {
	if k.tmux == nil {
		return nil
	}
	if _, err := k.tmux.KillSession(name); err != nil {
		return err
	}
	if k.attention != nil {
		_ = k.attention.Ack(name)
	}
	return nil
}

