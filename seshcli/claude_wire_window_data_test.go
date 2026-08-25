package seshcli

// Partition table (ECP + BVA) — step: 01-window-data — seshcli.makeClaudeFetcher
//
// mode 参数 × ListAllWindows 结果 的组合（2 个维度，未达到 pairwise 强制的 3 参数门槛，
// 3 个场景已覆盖 plan §6 step01 覆盖锚点列出的全部 3 条，逐一枚举即可）：
//   mode        ListAllWindows 结果        等价类                期望 Windows / err                                契约来源
//   all         成功返回 2 个 window       有效（该拿 windows）  Windows 长度=2 且逐字段映射正确，err=nil          覆盖锚点①
//   zoxide      不应被调用                 有效（不该拿 windows）Windows == nil，且 ListAllWindows 未被调用        覆盖锚点②
//   tmux        返回 error                 无效（fail-soft）     Windows == nil，err=nil（sessions 照常返回）      覆盖锚点③
//
// pairwise: 不适用 — 只有 2 个维度且组合数已经是 plan 明确列出的最小必要集合。

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Wingsdh/cc-sesh/v2/lister"
	"github.com/Wingsdh/cc-sesh/v2/model"
	"github.com/Wingsdh/cc-sesh/v2/picker"
	"github.com/Wingsdh/cc-sesh/v2/tmux"
)

// TestMakeClaudeFetcher_AllModeBringsBackWindows 覆盖覆盖锚点①：
// all 模式下 makeClaudeFetcher 必须调 ListAllWindows 并把结果整理成 []picker.WindowItem 带回。
func TestMakeClaudeFetcher_AllModeBringsBackWindows(t *testing.T) {
	sessions := makeSessions("s1", "s2")

	mockLister := &lister.MockLister{}
	mockLister.EXPECT().List(lister.ListOptions{}).Return(sessions, nil)

	mockTmux := &tmux.MockTmux{}
	mockTmux.EXPECT().ListAllPanes().Return(nil, nil)
	mockTmux.EXPECT().ListAllWindows().Return([]*model.TmuxWindowAcrossSessions{
		{SessionName: "s1", Name: "editor", Index: 0, Active: true},
		{SessionName: "s2", Name: "server", Index: 1, Active: false},
	}, nil)

	deps := &Deps{Tmux: mockTmux, Lister: mockLister}
	fetcher := makeClaudeFetcher(deps, lister.ListOptions{})

	result, err := fetcher(picker.ModeAll)
	require.NoError(t, err)

	require.Len(t, result.Windows, 2)
	assert.Equal(t, picker.WindowItem{SessionName: "s1", Index: 0, Name: "editor", Active: true}, result.Windows[0])
	assert.Equal(t, picker.WindowItem{SessionName: "s2", Index: 1, Name: "server", Active: false}, result.Windows[1])
	assert.Equal(t, sessions, result.Sessions)

	mockLister.AssertExpectations(t)
	mockTmux.AssertExpectations(t)
}

// TestMakeClaudeFetcher_ZoxideModeSkipsListAllWindows 覆盖覆盖锚点②：
// 非 all/tmux 模式下 Windows 必须是 nil，且 ListAllWindows 完全不应被调用——
// 故意不给 mockTmux 设置 ListAllWindows 的期望：一旦生产代码误调用它，
// mockery 会因"未设置返回值"直接 panic，比事后断言更硬地钉死这条 NEVER。
func TestMakeClaudeFetcher_ZoxideModeSkipsListAllWindows(t *testing.T) {
	sessions := makeSessions("s1")

	mockLister := &lister.MockLister{}
	mockLister.EXPECT().List(lister.ListOptions{Zoxide: true}).Return(sessions, nil)

	mockTmux := &tmux.MockTmux{}
	mockTmux.EXPECT().ListAllPanes().Return(nil, nil)

	deps := &Deps{Tmux: mockTmux, Lister: mockLister}
	fetcher := makeClaudeFetcher(deps, lister.ListOptions{})

	result, err := fetcher(picker.ModeZoxide)
	require.NoError(t, err)

	assert.Nil(t, result.Windows, "非 all/tmux 模式必须是 nil，不是空切片")

	mockLister.AssertExpectations(t)
	mockTmux.AssertExpectations(t)
}

// TestMakeClaudeFetcher_ListAllWindowsErrorDoesNotBlockSessions 覆盖覆盖锚点③：
// ListAllWindows 失败必须 fail-soft——sessions 照常返回、整体 err 仍为 nil、Windows 退化为 nil。
func TestMakeClaudeFetcher_ListAllWindowsErrorDoesNotBlockSessions(t *testing.T) {
	sessions := makeSessions("s1")

	mockLister := &lister.MockLister{}
	mockLister.EXPECT().List(lister.ListOptions{Tmux: true}).Return(sessions, nil)

	mockTmux := &tmux.MockTmux{}
	mockTmux.EXPECT().ListAllPanes().Return(nil, nil)
	mockTmux.EXPECT().ListAllWindows().Return(nil, errors.New("boom"))

	deps := &Deps{Tmux: mockTmux, Lister: mockLister}
	fetcher := makeClaudeFetcher(deps, lister.ListOptions{})

	result, err := fetcher(picker.ModeTmux)
	require.NoError(t, err, "ListAllWindows 失败不应阻断整个取数")

	assert.Equal(t, sessions, result.Sessions, "sessions 应照常返回")
	assert.Nil(t, result.Windows)

	mockLister.AssertExpectations(t)
	mockTmux.AssertExpectations(t)
}
