package picker

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// 预览区的占位与失败文案。PRD 只要求「显示一行简短说明」，具体措辞由计划定稿。
const (
	previewLoadingText  = "Loading preview..."
	previewNoTargetText = "No live session to preview"
	previewFailedPrefix = "Preview failed: "
)

// previewDebounce 是光标停留多久才真正发起抓屏。
// 快速穿行列表时每一行都抓屏会把 tmux 拖垮，也会让按键卡顿。
const previewDebounce = 150 * time.Millisecond

// previewState 是预览区的全部状态。
//
// seq 是这套机制的核心：每次重新取快照都自增一次，定时消息与结果消息各带一份，
// 到达时与当前值比对，对不上就丢弃。两道校验缺一不可——少一道就会出现
// 「抓 A 的画面渲染在 B 行上」。
type previewState struct {
	target  string // 当前预览目标串（"" = 无目标）
	content string // 已取到的快照（含 ANSI）
	err     string // 失败说明
	loading bool   // 读取中
	seq     int    // 请求序号，用于丢弃过期结果
}

// previewTickMsg 是防抖定时到达的信号（六步中的第 4 步在此校验序号）。
type previewTickMsg struct {
	seq    int
	target string
}

// previewResultMsg 是抓屏结果（六步中的第 6 步在此校验序号与目标串）。
type previewResultMsg struct {
	seq     int
	target  string
	content string
	err     error
}

// previewTarget 解析光标所在行对应的抓屏目标。
//
//	window 行            → 会话名:window序号
//	session 行(src=tmux) → 该会话 Active 的 window；无 active（数据滞后）则退回
//	                       按 Index 升序后的首个 window
//	window 表为空 / 非 tmux 来源 / 空列表 / 未注入 Capturer → 无目标
func (m Model) previewTarget() (string, bool) {
	if m.capturer == nil {
		return "", false
	}
	row, ok := m.rowAt(m.cursor)
	if !ok {
		return "", false
	}
	item := m.filtered[row.sessionIdx].item
	if item.src != "tmux" {
		return "", false
	}

	if row.kind == rowWindow {
		return windowTarget(item.name, row.window.Index)
	}

	wins := m.windowsBySession[item.name]
	if len(wins) == 0 {
		return "", false
	}
	// windowsBySession 已在分组时按 Index 升序，wins[0] 即「首个 window」
	chosen := wins[0]
	for _, w := range wins {
		if w.Active {
			chosen = w
			break
		}
	}
	return windowTarget(item.name, chosen.Index)
}

// retargetPreview 走取快照六步里的第 1-3 步：清空并置读取中 → 序号自增 → 150ms 定时。
//
// force=false 时「重复目标不重取」：目标未变、不在 loading、且已有内容或错误，
// 整个流程跳过，沿用已取快照。force=true（Ctrl-r）无条件走完。
//
// 无目标时清空预览状态并返回 nil，绝不发起抓屏。
func (m *Model) retargetPreview(force bool) tea.Cmd {
	target, ok := m.previewTarget()
	if !ok {
		m.preview = previewState{seq: m.preview.seq}
		return nil
	}

	if !force &&
		target == m.preview.target &&
		!m.preview.loading &&
		(m.preview.content != "" || m.preview.err != "") {
		return nil
	}

	// 步骤 1：旧目标的画面绝不保留，当帧就渲染读取中占位
	m.preview.target = target
	m.preview.content = ""
	m.preview.err = ""
	m.preview.loading = true
	// 步骤 2：序号自增，让所有在途的旧定时 / 旧结果自然作废
	m.preview.seq++

	seq := m.preview.seq
	// 步骤 3：防抖定时。NEVER 用 tea.Every——那是轮询，本功能明确不做自动刷新。
	return tea.Tick(previewDebounce, func(time.Time) tea.Msg {
		return previewTickMsg{seq: seq, target: target}
	})
}

// capturePreview 是六步中的第 5 步：异步抓屏。
// 抓屏 NEVER 在渲染路径上同步执行——tmux 慢的时候不能把按键卡住。
func (m Model) capturePreview(seq int, target string) tea.Cmd {
	capturer := m.capturer
	return func() tea.Msg {
		content, err := capturer.Capture(target)
		return previewResultMsg{seq: seq, target: target, content: content, err: err}
	}
}

// renderPreview 把预览状态渲染成固定 width × height 的一块文本。
//
// 每行都走 ansi.Truncate 做「ANSI 感知」截断：按 []byte / []rune 切片会把转义序列
// 从中间切断，导致整屏错色。每行前置 "\x1b[0m" 重置样式，避免上一行未闭合的
// 转义污染下一行。不足高度补空行，保证分栏块高度稳定（否则列表区会跳动）。
func (m Model) renderPreview(width, height int) string {
	if width < 1 || height < 1 {
		return ""
	}

	faint := lipgloss.NewStyle().Faint(true)
	var lines []string
	switch {
	case m.preview.loading:
		lines = []string{faint.Render(previewLoadingText)}
	case m.preview.err != "":
		lines = []string{faint.Render(previewFailedPrefix + m.preview.err)}
	case m.preview.target == "":
		lines = []string{faint.Render(previewNoTargetText)}
	default:
		lines = strings.Split(m.preview.content, "\n")
	}

	// 抓屏（capture-pane）返回的是整个 pane 高度：内容贴顶的 pane 尾部是成片空行。
	// 先裁掉视觉上为空的尾行，再做「保留末尾」，否则留下来的全是空白，
	// 真内容反而被裁走，预览看起来只剩最后一点点。
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}

	// 超高时保留末尾若干行：终端里最新的输出在底部，那才是用户想看的
	if len(lines) > height {
		lines = lines[len(lines)-height:]
	}

	out := make([]string, 0, height)
	for _, line := range lines {
		out = append(out, "\x1b[0m"+ansi.Truncate(line, width, ""))
	}
	for len(out) < height {
		out = append(out, "")
	}
	return strings.Join(out, "\n")
}
