package seshcli

// tmuxCapturer 多 pane 拼接的行为契约：
//
//	场景                          期望                                          锚点
//	window 只有 1 个 pane         透传 CapturePane(target)，不拼接              退回原行为
//	window 有多个 pane            按序逐 pane 抓，pane 间夹 dim 分隔线          多 pane 可见
//	ListWindowPanes 失败(空)      透传 CapturePane(target)                      fail-soft
//	某个 pane 抓取失败            跳过该 pane，其余照拼                         fail-soft
//	pane 内容尾部成片空行         拼接前裁掉，分隔线紧跟真内容                  尾部空行回归

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Wingsdh/cc-sesh/v2/tmux"
)

func TestTmuxCapturer_SinglePane_PassesThroughWithoutComposing(t *testing.T) {
	mockTmux := &tmux.MockTmux{}
	mockTmux.EXPECT().ListWindowPanes("s:2").Return([]int{0}, nil)
	mockTmux.EXPECT().CapturePane("s:2").Return("only-pane", nil)

	c := &tmuxCapturer{tmux: mockTmux}
	got, err := c.Capture("s:2")
	require.NoError(t, err)
	assert.Equal(t, "only-pane", got)
	assert.NotContains(t, got, "── pane")
	mockTmux.AssertExpectations(t)
}

func TestTmuxCapturer_MultiPane_ComposesAllPanesWithSeparators(t *testing.T) {
	mockTmux := &tmux.MockTmux{}
	mockTmux.EXPECT().ListWindowPanes("s:2").Return([]int{0, 1, 2}, nil)
	mockTmux.EXPECT().CapturePane("s:2.0").Return("pane-zero", nil)
	mockTmux.EXPECT().CapturePane("s:2.1").Return("pane-one", nil)
	mockTmux.EXPECT().CapturePane("s:2.2").Return("pane-two", nil)

	c := &tmuxCapturer{tmux: mockTmux}
	got, err := c.Capture("s:2")
	require.NoError(t, err)

	// 三个 pane 的内容按序全在，pane 间恰好两条分隔线（首个 pane 前不加）
	zeroAt := strings.Index(got, "pane-zero")
	oneAt := strings.Index(got, "pane-one")
	twoAt := strings.Index(got, "pane-two")
	require.NotEqual(t, -1, zeroAt)
	require.NotEqual(t, -1, oneAt)
	require.NotEqual(t, -1, twoAt)
	assert.Less(t, zeroAt, oneAt)
	assert.Less(t, oneAt, twoAt)
	assert.Equal(t, 2, strings.Count(got, "── pane "))
	assert.Less(t, zeroAt, strings.Index(got, "── pane 1"), "分隔线应在首个 pane 内容之后")
	mockTmux.AssertExpectations(t)
}

func TestTmuxCapturer_ListPanesFailSoft_FallsBackToPlainCapture(t *testing.T) {
	mockTmux := &tmux.MockTmux{}
	// ListWindowPanes 的契约是 fail-soft：失败返回空切片 + nil
	mockTmux.EXPECT().ListWindowPanes("s:2").Return([]int{}, nil)
	mockTmux.EXPECT().CapturePane("s:2").Return("active-pane", nil)

	c := &tmuxCapturer{tmux: mockTmux}
	got, err := c.Capture("s:2")
	require.NoError(t, err)
	assert.Equal(t, "active-pane", got)
	mockTmux.AssertExpectations(t)
}

func TestTmuxCapturer_OnePaneCaptureFails_OthersStillComposed(t *testing.T) {
	mockTmux := &tmux.MockTmux{}
	mockTmux.EXPECT().ListWindowPanes("s:2").Return([]int{0, 1}, nil)
	mockTmux.EXPECT().CapturePane("s:2.0").Return("", errors.New("pane gone"))
	mockTmux.EXPECT().CapturePane("s:2.1").Return("survivor", nil)

	c := &tmuxCapturer{tmux: mockTmux}
	got, err := c.Capture("s:2")
	require.NoError(t, err)
	assert.Contains(t, got, "survivor")
	assert.NotContains(t, got, "pane gone")
	mockTmux.AssertExpectations(t)
}

func TestTmuxCapturer_TrimsTrailingBlanksBeforeJoining(t *testing.T) {
	mockTmux := &tmux.MockTmux{}
	mockTmux.EXPECT().ListWindowPanes("s:2").Return([]int{0, 1}, nil)
	// pane 0 内容贴顶，capture-pane 按 pane 高度补了成片空行（含带 ANSI 的视觉空行）
	mockTmux.EXPECT().CapturePane("s:2.0").Return("top-content\n\n\n   \n\x1b[0m  \n", nil)
	mockTmux.EXPECT().CapturePane("s:2.1").Return("bottom-content", nil)

	c := &tmuxCapturer{tmux: mockTmux}
	got, err := c.Capture("s:2")
	require.NoError(t, err)

	lines := strings.Split(got, "\n")
	require.GreaterOrEqual(t, len(lines), 3)
	assert.Equal(t, "top-content", lines[0], "空尾行应被裁掉，分隔线紧跟真内容")
	assert.Contains(t, lines[1], "── pane 1")
	mockTmux.AssertExpectations(t)
}
