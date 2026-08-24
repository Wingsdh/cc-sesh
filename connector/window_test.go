package connector

// Partition table (ECP + BVA) — step: 04-connect-window — connector 包
//
// 用例编号（tc-aXX）对应 ai-plans/testcases-picker.md §1.7。
//
// parseWindowTarget(name) (session string, index int, ok bool)
//
//	输入            等价类              类型          期望                      来源
//	"proj-a:1"      正常                有效          ("proj-a",1,true)         行为契约
//	"proj:01"       前导0(BVA)          有效·边界     ("proj",1,true)           行为契约「记得覆盖前导0合法」
//	"foo"           无冒号              无效          ok=false                  覆盖锚点反例①
//	"a:b:1"         两个冒号            无效          ok=false                  覆盖锚点反例②/tc-a41
//	"foo:bar"       冒号后非数字        无效          ok=false                  覆盖锚点反例③/tc-a42
//	"foo:"          冒号后为空(边界)    无效          ok=false                  覆盖锚点反例④
//	":3"            会话名为空(边界)    无效          ok=false                  覆盖锚点反例⑤
//
// tmuxWindowStrategy(c, name) (model.Connection, error) — 四条判定全满足才认领
//
//	场景                                   等价类            期望                          来源
//	四条全满足                             有效              Found=true，字段按契约构造     tc-a40
//	两个冒号                               无效(判定①)      Found=false，放行              tc-a41
//	冒号后非数字                           无效(判定②)      Found=false，放行              tc-a42
//	会话名不存在于 tmux（"目录名陷阱"）    无效(判定③)      Found=false，放行              tc-a43
//	序号不在该会话 window 列表（"序号陷阱"）无效(判定④)     Found=false，放行              tc-a44
//	ListWindows 自身报错                   无效(fail-soft)  Found=false, err=nil，放行     覆盖锚点 tmux.ListWindows③
//	完全不是 "会话:数字" 格式              无效(判定①提前失败) Found=false，放行            防御性补充
//
// connectToTmuxWindow(c, connection, opts) (string, error)
//
//	场景                     等价类   期望                                              来源
//	IsAttached()==true       有效     SwitchClient→SelectWindow，返回 switching 文案     tc-a45
//	IsAttached()==false      有效     AttachSession(完整目标串)，返回 attaching 文案     tc-a46
//	SwitchClient 失败        无效     包装 error，SelectWindow 零调用                    覆盖锚点
//	SelectWindow 失败        无效     包装 error                                        覆盖锚点
//	AttachSession 失败       无效     包装 error                                        覆盖锚点
//
// pairwise：不适用——parseWindowTarget 只有 1 个字符串输入维度；
// tmuxWindowStrategy 的四条判定是顺序短路链条（不是独立正交参数的组合空间，
// 每条判定失败即返回，不存在"多条同时变化"的笛卡尔积场景）；connectToTmuxWindow
// 只有 1 个布尔维度（IsAttached）。均未达到 ≥3 参数×≥2取值的强制门槛。

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Wingsdh/cc-sesh/v2/lister"
	"github.com/Wingsdh/cc-sesh/v2/model"
	"github.com/Wingsdh/cc-sesh/v2/tmux"
)

// ---------- Section A: parseWindowTarget ----------

func TestParseWindowTarget(t *testing.T) {
	cases := []struct {
		name        string
		input       string
		wantSession string
		wantIndex   int
		wantOK      bool
	}{
		{"正常格式", "proj-a:1", "proj-a", 1, true},
		{"前导 0 合法(BVA)", "proj:01", "proj", 1, true},
		{"无冒号", "foo", "", 0, false},
		{"两个冒号(tc-a41)", "a:b:1", "", 0, false},
		{"冒号后非数字(tc-a42)", "foo:bar", "", 0, false},
		{"冒号后为空(边界)", "foo:", "", 0, false},
		{"会话名为空(边界)", ":3", "", 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			session, index, ok := parseWindowTarget(tc.input)
			assert.Equal(t, tc.wantOK, ok, "ok 不符")
			if tc.wantOK {
				assert.Equal(t, tc.wantSession, session)
				assert.Equal(t, tc.wantIndex, index)
			}
		})
	}
}

// ---------- Section B: tmuxWindowStrategy ----------

func newWindowStrategyConnector(mockLister *lister.MockLister, mockTmux *tmux.MockTmux) *RealConnector {
	return &RealConnector{
		lister: mockLister,
		tmux:   mockTmux,
	}
}

func TestTmuxWindowStrategy_AllFourConditionsMet_Claims(t *testing.T) {
	// tc-a40：四条判定全部满足 → 认领为 window 目标，字段按契约构造。
	mockLister := new(lister.MockLister)
	mockTmux := new(tmux.MockTmux)
	c := newWindowStrategyConnector(mockLister, mockTmux)

	mockLister.On("FindTmuxSession", "proj-a").Return(model.SeshSession{
		Name: "proj-a", Src: "tmux", Path: "/home/user/proj-a",
	}, true)
	mockTmux.On("ListWindows", "proj-a").Return([]*model.TmuxWindow{
		{Index: 1, Name: "editor"},
		{Index: 2, Name: "server"},
	}, nil)

	connection, err := tmuxWindowStrategy(c, "proj-a:1")

	require.NoError(t, err)
	require.True(t, connection.Found)
	assert.Equal(t, srcTmuxWindow, connection.Session.Src)
	assert.Equal(t, "proj-a:1", connection.Session.Name, "认领时 Session.Name 应是完整目标串")
	assert.Equal(t, "/home/user/proj-a", connection.Session.Path, "Path 应取自命中会话")
	assert.False(t, connection.New)
	assert.False(t, connection.AddToZoxide, "window 目标不该改动 zoxide 频次（沿用 tmuxPaneStrategy 先例）")
}

func TestTmuxWindowStrategy_TwoColons_DoesNotClaim(t *testing.T) {
	// tc-a41：判定①（仅一个冒号）不满足 → 放行，不该碰 lister/tmux。
	mockLister := new(lister.MockLister)
	mockTmux := new(tmux.MockTmux)
	c := newWindowStrategyConnector(mockLister, mockTmux)

	connection, err := tmuxWindowStrategy(c, "proj-a:1:2")

	require.NoError(t, err)
	assert.False(t, connection.Found)
	mockLister.AssertNotCalled(t, "FindTmuxSession")
	mockTmux.AssertNotCalled(t, "ListWindows")
}

func TestTmuxWindowStrategy_NonDigitSuffix_DoesNotClaim(t *testing.T) {
	// tc-a42：判定②（冒号后纯数字）不满足 → 放行。
	mockLister := new(lister.MockLister)
	mockTmux := new(tmux.MockTmux)
	c := newWindowStrategyConnector(mockLister, mockTmux)

	connection, err := tmuxWindowStrategy(c, "proj-a:main")

	require.NoError(t, err)
	assert.False(t, connection.Found)
	mockLister.AssertNotCalled(t, "FindTmuxSession")
}

func TestTmuxWindowStrategy_NoColonAtAll_DoesNotClaim(t *testing.T) {
	// 防御性补充：完全不是 "会话:数字" 格式，判定①第一关就该短路，不碰任何依赖。
	mockLister := new(lister.MockLister)
	mockTmux := new(tmux.MockTmux)
	c := newWindowStrategyConnector(mockLister, mockTmux)

	connection, err := tmuxWindowStrategy(c, "just-a-directory-name")

	require.NoError(t, err)
	assert.False(t, connection.Found)
	mockLister.AssertNotCalled(t, "FindTmuxSession")
	mockTmux.AssertNotCalled(t, "ListWindows")
}

func TestTmuxWindowStrategy_SessionNotFound_DoesNotClaim(t *testing.T) {
	// tc-a43 + "目录名陷阱"：目标串形如 "notes:1"，但 tmux 里没有 notes 会话 →
	// 判定③不满足，必须放行给后续策略（不能被误认领去连接一个不存在的会话）。
	mockLister := new(lister.MockLister)
	mockTmux := new(tmux.MockTmux)
	c := newWindowStrategyConnector(mockLister, mockTmux)

	mockLister.On("FindTmuxSession", "notes").Return(model.SeshSession{}, false)

	connection, err := tmuxWindowStrategy(c, "notes:1")

	require.NoError(t, err)
	assert.False(t, connection.Found, "会话名对应不到真实 tmux 会话时不该被 window 策略认领")
	mockTmux.AssertNotCalled(t, "ListWindows", "notes")
}

func TestTmuxWindowStrategy_IndexNotInWindowList_DoesNotClaim(t *testing.T) {
	// tc-a44 + "序号陷阱"：会话 "proj-a" 真实存在，但只有 window 1/2，序号 9 不存在 →
	// 判定④不满足，放行，不误切到错误的 window。
	mockLister := new(lister.MockLister)
	mockTmux := new(tmux.MockTmux)
	c := newWindowStrategyConnector(mockLister, mockTmux)

	mockLister.On("FindTmuxSession", "proj-a").Return(model.SeshSession{
		Name: "proj-a", Src: "tmux", Path: "/home/user/proj-a",
	}, true)
	mockTmux.On("ListWindows", "proj-a").Return([]*model.TmuxWindow{
		{Index: 1, Name: "editor"},
		{Index: 2, Name: "server"},
	}, nil)

	connection, err := tmuxWindowStrategy(c, "proj-a:9")

	require.NoError(t, err)
	assert.False(t, connection.Found, "序号 9 不在该会话的 window 列表里，不该被认领")
}

func TestTmuxWindowStrategy_ListWindowsError_FailSoftDoesNotClaim(t *testing.T) {
	// 覆盖锚点「tmux.ListWindows：③报错→放行（err==nil）」：
	// ListWindows 自身报错时，策略必须 fail-soft 放行，而不是把错误往上抛断链。
	mockLister := new(lister.MockLister)
	mockTmux := new(tmux.MockTmux)
	c := newWindowStrategyConnector(mockLister, mockTmux)

	mockLister.On("FindTmuxSession", "proj-a").Return(model.SeshSession{
		Name: "proj-a", Src: "tmux", Path: "/home/user/proj-a",
	}, true)
	mockTmux.On("ListWindows", "proj-a").Return(nil, errors.New("tmux: connection refused"))

	connection, err := tmuxWindowStrategy(c, "proj-a:1")

	require.NoError(t, err, "ListWindows 报错不该打断整条策略链")
	assert.False(t, connection.Found)
}

// ---------- Section C: connectToTmuxWindow ----------

func TestConnectToTmuxWindow_WhenAttached_SwitchesClientThenSelectsWindow(t *testing.T) {
	// tc-a45
	mockTmux := new(tmux.MockTmux)
	c := &RealConnector{tmux: mockTmux}
	connection := model.Connection{
		Found: true,
		Session: model.SeshSession{
			Src:  srcTmuxWindow,
			Name: "proj-a:3",
			Path: "/home/user/proj-a",
		},
	}

	mockTmux.On("IsAttached").Return(true)
	mockTmux.On("SwitchClient", "proj-a").Return("switched", nil)
	mockTmux.On("SelectWindow", "proj-a:3").Return("selected", nil)

	msg, err := connectToTmuxWindow(c, connection, model.ConnectOpts{})

	require.NoError(t, err)
	assert.Equal(t, "switching to tmux window: proj-a:3", msg)
}

func TestConnectToTmuxWindow_WhenNotAttached_AttachesWithFullTarget(t *testing.T) {
	// tc-a46：AttachSession 的入参必须是完整目标串（"proj-a:3"，不是 "proj-a"）。
	mockTmux := new(tmux.MockTmux)
	c := &RealConnector{tmux: mockTmux}
	connection := model.Connection{
		Found: true,
		Session: model.SeshSession{
			Src:  srcTmuxWindow,
			Name: "proj-a:3",
			Path: "/home/user/proj-a",
		},
	}

	mockTmux.On("IsAttached").Return(false)
	mockTmux.On("AttachSession", "proj-a:3").Return("attached", nil)

	msg, err := connectToTmuxWindow(c, connection, model.ConnectOpts{})

	require.NoError(t, err)
	assert.Equal(t, "attaching to tmux window: proj-a:3", msg)
	mockTmux.AssertCalled(t, "AttachSession", "proj-a:3")
}

func TestConnectToTmuxWindow_SwitchClientFails_WrapsErrorAndNeverCallsSelectWindow(t *testing.T) {
	// 覆盖锚点：SwitchClient 失败 → 包装 error 返回，且不再调 SelectWindow。
	mockTmux := new(tmux.MockTmux)
	c := &RealConnector{tmux: mockTmux}
	connection := model.Connection{
		Session: model.SeshSession{Src: srcTmuxWindow, Name: "proj-a:3"},
	}

	mockTmux.On("IsAttached").Return(true)
	mockTmux.On("SwitchClient", "proj-a").Return("", errors.New("no such session"))

	msg, err := connectToTmuxWindow(c, connection, model.ConnectOpts{})

	assert.Equal(t, "", msg)
	require.Error(t, err)
	assert.ErrorContains(t, err, "no such session")
	mockTmux.AssertNotCalled(t, "SelectWindow")
}

func TestConnectToTmuxWindow_SelectWindowFails_WrapsError(t *testing.T) {
	mockTmux := new(tmux.MockTmux)
	c := &RealConnector{tmux: mockTmux}
	connection := model.Connection{
		Session: model.SeshSession{Src: srcTmuxWindow, Name: "proj-a:3"},
	}

	mockTmux.On("IsAttached").Return(true)
	mockTmux.On("SwitchClient", "proj-a").Return("switched", nil)
	mockTmux.On("SelectWindow", "proj-a:3").Return("", errors.New("window not found"))

	msg, err := connectToTmuxWindow(c, connection, model.ConnectOpts{})

	assert.Equal(t, "", msg)
	require.Error(t, err)
	assert.ErrorContains(t, err, "window not found")
}

func TestConnectToTmuxWindow_AttachSessionFails_WrapsError(t *testing.T) {
	mockTmux := new(tmux.MockTmux)
	c := &RealConnector{tmux: mockTmux}
	connection := model.Connection{
		Session: model.SeshSession{Src: srcTmuxWindow, Name: "proj-a:3"},
	}

	mockTmux.On("IsAttached").Return(false)
	mockTmux.On("AttachSession", "proj-a:3").Return("", errors.New("failed to attach"))

	msg, err := connectToTmuxWindow(c, connection, model.ConnectOpts{})

	assert.Equal(t, "", msg)
	require.Error(t, err)
	assert.ErrorContains(t, err, "failed to attach")
}
