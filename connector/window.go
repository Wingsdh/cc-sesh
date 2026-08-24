package connector

import (
	"fmt"
	"strings"

	"github.com/Wingsdh/cc-sesh/v2/model"
)

// srcTmuxWindow 是 window 目标在策略链里的来源标识，与既有 "tmux-pane" 同构
// （都靠特殊的 name 格式识别后分流到专属的连接动作）。
const srcTmuxWindow = "tmux-window"

// parseWindowTarget 解析 "会话名:序号"。
//
// 仅当「含且仅含一个冒号」且「冒号后非空且全为数字」时 ok=true。
// 前导 0 视为合法数字（"proj:01" → index 1）。
func parseWindowTarget(name string) (string, int, bool) {
	if strings.Count(name, ":") != 1 {
		return "", 0, false
	}
	sep := strings.Index(name, ":")
	session, digits := name[:sep], name[sep+1:]
	if session == "" || digits == "" {
		return "", 0, false
	}
	index := 0
	for _, r := range digits {
		if r < '0' || r > '9' {
			return "", 0, false
		}
		index = index*10 + int(r-'0')
	}
	return session, index, true
}

// tmuxWindowStrategy 认领形如 "会话名:序号" 的 window 目标。
//
// 四条判定必须全部满足才认领：
//  1. 含且仅含一个冒号
//  2. 冒号后非空且全为数字
//  3. 会话名确实是一个活着的 tmux 会话
//  4. 该序号确实存在于该会话的 window 列表里
//
// 任一条不满足就放行给后续策略。**第 3、4 条一条都不能省**——否则形如
// "notes:1" 的目录名会被误认领，把用户切到毫不相干的地方。
func tmuxWindowStrategy(c *RealConnector, name string) (model.Connection, error) {
	session, index, ok := parseWindowTarget(name)
	if !ok {
		return model.Connection{Found: false}, nil
	}

	found, exists := c.lister.FindTmuxSession(session)
	if !exists {
		return model.Connection{Found: false}, nil
	}

	windows, err := c.tmux.ListWindows(session)
	if err != nil {
		// fail-soft：列 window 失败就放行，不打断整条策略链
		return model.Connection{Found: false}, nil
	}
	for _, w := range windows {
		if w != nil && w.Index == index {
			return model.Connection{
				Found:       true,
				New:         false,
				AddToZoxide: false, // window 目标不改动 zoxide 频次（沿用 tmuxPaneStrategy 先例）
				Session: model.SeshSession{
					Src:  srcTmuxWindow,
					Name: name,
					Path: found.Path,
				},
			}, nil
		}
	}
	return model.Connection{Found: false}, nil
}

// connectToTmuxWindow 把用户切到具体 window。
//
// 已在 tmux 里 → 先切会话再选 window；不在 tmux 里 → 直接 attach 到完整目标串。
// NEVER 在这条路径上创建 session / 创建 window / 重命名——认领失败就该放行，不兜底新建。
func connectToTmuxWindow(c *RealConnector, connection model.Connection, _ model.ConnectOpts) (string, error) {
	target := connection.Session.Name
	session, _, ok := parseWindowTarget(target)
	if !ok {
		return "", fmt.Errorf("invalid tmux window target: %s", target)
	}

	if c.tmux.IsAttached() {
		if _, err := c.tmux.SwitchClient(session); err != nil {
			return "", fmt.Errorf("failed to switch to session %s: %w", session, err)
		}
		if _, err := c.tmux.SelectWindow(target); err != nil {
			// 数据可能滞后：picker 列出来的 window 也许已经被关掉了
			return "", fmt.Errorf("failed to select tmux window %s: %w", target, err)
		}
		return fmt.Sprintf("switching to tmux window: %s", target), nil
	}

	if _, err := c.tmux.AttachSession(target); err != nil {
		return "", fmt.Errorf("failed to attach to tmux window %s: %w", target, err)
	}
	return fmt.Sprintf("attaching to tmux window: %s", target), nil
}
