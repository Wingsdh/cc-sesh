package connector

// Partition table (ECP + BVA) — step: 04-connect-window — connector.Connect 策略链顺序
//
// plan §9「插链位置：tmuxWindowStrategy 排在 tmuxPaneStrategy 之后、tmuxStrategy 之前」
// 目前没有任何测试覆盖（window_test.go 只单测 tmuxWindowStrategy/connectToTmuxWindow
// 这两个函数本身，不经过 Connect() 的策略链）。这里不重构 strategies 切片，纯行为测：
// 构造一个"会话名恰好等于整个目标串"的陷阱场景（真实存在字面量会话 "proj-a:3"），
// 如果排序反了，tmuxStrategy 会先把整串当会话名抢走，走 SwitchOrAttach 而不是
// window 专属的连接动作——返回文案与调用的 tmux 方法完全不同，足以钉死顺序。
//
//	场景                                          等价类   期望                                    来源
//	"proj-a:3" 命中 window，且字面量会话 "proj-a:3" 也存在  陷阱     window 策略先认领，走 AttachSession   plan §9 插链位置
//	                                                                  文案，SwitchOrAttach 零调用
//	"sess:3"（pane 策略要求 name 含 '/'，天然不冲突）        补充     pane 策略不消费 ListTmuxPanes，       行为契约「排除冲突」
//	                                                                  window 策略正常认领
//
// pairwise：不适用——只有 1 个"顺序是否正确"的判定维度，2 个测试分别覆盖两种连接动作
// 分支（attach / switch），不是独立参数的组合空间。

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Wingsdh/cc-sesh/v2/lister"
	"github.com/Wingsdh/cc-sesh/v2/model"
	"github.com/Wingsdh/cc-sesh/v2/tmux"
)

func TestConnect_WindowStrategyClaimsBeforeTmuxStrategyEvenWhenLiteralSessionNameMatches(t *testing.T) {
	// 陷阱：tmux 里同时存在会话 "proj-a"（window 3 的真实归属会话）与一个字面量
	// 就叫 "proj-a:3" 的"诱饵"会话。如果 tmuxWindowStrategy 没有排在 tmuxStrategy
	// 之前，后者会把整串 "proj-a:3" 当会话名直接抢走。
	mockLister := new(lister.MockLister)
	mockTmux := new(tmux.MockTmux)
	c := &RealConnector{lister: mockLister, tmux: mockTmux}

	mockLister.On("FindTmuxSession", "proj-a").Return(model.SeshSession{
		Name: "proj-a", Src: "tmux", Path: "/home/user/proj-a",
	}, true)
	mockTmux.On("ListWindows", "proj-a").Return([]*model.TmuxWindow{
		{Index: 3, Name: "editor"},
	}, nil)
	// 诱饵：字面量会话名恰好等于整个目标串，一旦排序反了会被 tmuxStrategy 直接命中。
	mockLister.On("FindTmuxSession", "proj-a:3").Return(model.SeshSession{
		Name: "proj-a:3", Src: "tmux", Path: "/home/user/literal-proj-a-3",
	}, true)

	mockTmux.On("IsAttached").Return(false)
	mockTmux.On("AttachSession", "proj-a:3").Return("attaching output", nil)

	msg, err := c.Connect("proj-a:3", model.ConnectOpts{})

	require.NoError(t, err)
	assert.Equal(t, "attaching to tmux window: proj-a:3", msg,
		"应走 window 专属连接动作，而不是被 tmuxStrategy 当成字面量会话名抢走")
	// 排序反了的话 tmuxStrategy 会调 SwitchOrAttach（没被 mock，调用会直接 panic
	// 掉这个测试）；这里再显式断言一次零调用，把"为什么会失败"钉得更死。
	mockTmux.AssertNotCalled(t, "SwitchOrAttach")
}

func TestConnect_WindowTargetNotClaimedByPaneStrategy(t *testing.T) {
	// 补充：tmuxPaneStrategy 要求 name 含 '/'；"sess:3" 不含 '/'，天然不冲突，
	// pane 策略应该直接放行（不消费 ListTmuxPanes），交给 window 策略处理。
	mockLister := new(lister.MockLister)
	mockTmux := new(tmux.MockTmux)
	c := &RealConnector{lister: mockLister, tmux: mockTmux}

	mockTmux.On("IsAttached").Return(true)
	mockLister.On("FindTmuxSession", "sess").Return(model.SeshSession{
		Name: "sess", Src: "tmux", Path: "/home/user/sess",
	}, true)
	mockTmux.On("ListWindows", "sess").Return([]*model.TmuxWindow{
		{Index: 3, Name: "editor"},
	}, nil)
	mockTmux.On("SwitchClient", "sess").Return("switched", nil)
	mockTmux.On("SelectWindow", "sess:3").Return("selected", nil)

	msg, err := c.Connect("sess:3", model.ConnectOpts{})

	require.NoError(t, err)
	assert.Equal(t, "switching to tmux window: sess:3", msg)
	mockLister.AssertNotCalled(t, "ListTmuxPanes")
}
