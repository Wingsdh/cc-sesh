package tmux

// Partition table (ECP + BVA) — step: 01-window-data — tmux package
//
// ListAllWindows() ([]*model.TmuxWindowAcrossSessions, error)
//   输入(ListCmd 返回)                       等价类       类型         期望输出                                    契约来源
//   3 行合法输出，2 session 混合 Active      正常多行    有效         3 个解析后的 window，字段/顺序与输入一致    行为契约「ListAllWindows 解析」
//   ListCmd 返回 err                         命令失败    无效         空切片 + nil error（fail-soft）             行为契约「ListAllWindows 失败一律 fail-soft」/ 覆盖锚点②
//   合法行夹杂字段数 != 4 的脏行             脏数据      无效         脏行跳过，合法行仍解析且不 panic            行为契约「字段数 ≠4 的行跳过」/ 覆盖锚点③
//   ListCmd 返回空切片                       空输出      边界(下界)   空切片，no panic                            覆盖锚点④「空输出→空切片」
//   命令形态                                 -           -            args=[list-windows -a -F <fmt>]，fmt 用     覆盖锚点「命令形态断言」
//                                                                      "::" 分隔且含 4 个占位符（并入happy-path用例）
//
// ListWindows(targetSession) 回归
//   输入                  等价类     类型   期望输出                       契约来源
//   targetSession="proj"  既有路径   回归   命令/解析与改动前一致          行为契约「ListWindows 行为一字不改」/ 覆盖锚点
//
// CapturePane(target)
//   target        等价类                类型   期望输出                契约来源
//   "sess"        纯 session            有效   -t sess 原样透传        覆盖锚点「CapturePane」
//   "sess:3"      session:window       有效   -t sess:3 原样透传      覆盖锚点「CapturePane」
//   "sess:3.0"    session:window.pane  有效   -t sess:3.0 原样透传    行为契约「CapturePane 只改参数名」（补充覆盖）
//
// pairwise: 不适用 — 本文件内没有 ≥3 个独立参数同时变化的场景。

import (
	"errors"
	"testing"

	"github.com/Wingsdh/cc-sesh/v2/model"
	"github.com/Wingsdh/cc-sesh/v2/shell"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// allWindowsFormat 是契约里固定死的格式串："#{session_name}::#{window_index}::#{window_name}::#{window_active}"。
// 字段顺序（session_name, window_index, window_name, window_active）来自 plan §6 step 01 行为契约，
// 与 model.TmuxWindowAcrossSessions 的字段声明顺序（SessionName, Name, Index, Active）刻意不同——
// 解析时必须按这个 format 顺序取值，不能想当然按 struct 字段顺序取。
const allWindowsFormat = "#{session_name}::#{window_index}::#{window_name}::#{window_active}"

func TestListAllWindows_ParsesMultipleSessionsAndWindows(t *testing.T) {
	mockShell := &shell.MockShell{}
	tm := &RealTmux{shell: mockShell}
	mockShell.EXPECT().
		ListCmd("tmux", "list-windows", "-a", "-F", allWindowsFormat).
		Return([]string{
			"s1::0::editor::1",
			"s1::1::server::0",
			"s2::0::main::1",
		}, nil)

	windows, err := tm.ListAllWindows()
	require.NoError(t, err)
	require.Len(t, windows, 3)

	assert.Equal(t, &model.TmuxWindowAcrossSessions{SessionName: "s1", Name: "editor", Index: 0, Active: true}, windows[0])
	assert.Equal(t, &model.TmuxWindowAcrossSessions{SessionName: "s1", Name: "server", Index: 1, Active: false}, windows[1])
	assert.Equal(t, &model.TmuxWindowAcrossSessions{SessionName: "s2", Name: "main", Index: 0, Active: true}, windows[2])
}

func TestListAllWindows_ListCmdErrorReturnsEmptySliceNilError(t *testing.T) {
	mockShell := &shell.MockShell{}
	tm := &RealTmux{shell: mockShell}
	mockShell.EXPECT().
		ListCmd("tmux", "list-windows", "-a", "-F", allWindowsFormat).
		Return(nil, errors.New("tmux: no server running"))

	windows, err := tm.ListAllWindows()

	assert.NoError(t, err, "ListCmd 报错必须 fail-soft，不能把错误往上抛")
	assert.Empty(t, windows, "fail-soft 时应退化成空切片，让 picker 呈现'全部 session 不可展开'而不是崩")
}

func TestListAllWindows_SkipsMalformedRowsWithoutPanic(t *testing.T) {
	mockShell := &shell.MockShell{}
	tm := &RealTmux{shell: mockShell}
	mockShell.EXPECT().
		ListCmd("tmux", "list-windows", "-a", "-F", allWindowsFormat).
		Return([]string{
			"s1::0::editor::1",      // 合法
			"onlythreefields::a::b", // 3 个字段：脏行，应跳过
			"s1::1::server::0",      // 合法
			"",                      // 1 个字段（空字符串本身）：脏行，应跳过
			"toomany::a::b::c::d",   // 5 个字段：脏行，应跳过
		}, nil)

	var windows []*model.TmuxWindowAcrossSessions
	require.NotPanics(t, func() {
		var err error
		windows, err = tm.ListAllWindows()
		require.NoError(t, err)
	})

	require.Len(t, windows, 2, "5 行里只有 2 行字段数=4，脏行必须被跳过而不是让整体失败")
	assert.Equal(t, "editor", windows[0].Name)
	assert.Equal(t, "server", windows[1].Name)
}

func TestListAllWindows_EmptyOutputReturnsEmptySlice(t *testing.T) {
	mockShell := &shell.MockShell{}
	tm := &RealTmux{shell: mockShell}
	mockShell.EXPECT().
		ListCmd("tmux", "list-windows", "-a", "-F", allWindowsFormat).
		Return([]string{}, nil)

	windows, err := tm.ListAllWindows()

	require.NoError(t, err)
	assert.Empty(t, windows)
}

// TestListWindows_TargetSessionRegressionUnaffected 是 step 01 的回归挡板：
// ListAllWindows 新增之后，既有的 ListWindows(targetSession) 命令/解析路径必须
// 一字不改（用生产代码里既有的 listWindowsFormat() 而不是复制一份字面量，
// 防止两处 format 悄悄分叉却测不出来）。
func TestListWindows_TargetSessionRegressionUnaffected(t *testing.T) {
	mockShell := &shell.MockShell{}
	tm := &RealTmux{shell: mockShell}
	mockShell.EXPECT().
		ListCmd("tmux", "list-windows", "-t", "proj", "-F", listWindowsFormat()).
		Return([]string{"2::editor::/proj::1"}, nil)

	windows, err := tm.ListWindows("proj")
	require.NoError(t, err)
	require.Len(t, windows, 1)
	assert.Equal(t, 2, windows[0].Index)
	assert.Equal(t, "editor", windows[0].Name)
	assert.Equal(t, "/proj", windows[0].Path)
	assert.True(t, windows[0].Active)
}

func TestCapturePane_TargetPassthrough(t *testing.T) {
	cases := []struct {
		name   string
		target string
	}{
		{"纯 session 名", "sess"},
		{"session:window", "sess:3"},
		{"session:window.pane", "sess:3.0"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mockShell := &shell.MockShell{}
			tm := &RealTmux{shell: mockShell, bin: "tmux"}
			mockShell.EXPECT().
				Cmd("tmux", "capture-pane", "-e", "-p", "-t", tc.target).
				Return("captured output", nil)

			out, err := tm.CapturePane(tc.target)
			require.NoError(t, err)
			assert.Equal(t, "captured output", out)
		})
	}
}
