package tmux

import (
	"errors"
	"testing"

	"github.com/Wingsdh/cc-sesh/v2/shell"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListClients(t *testing.T) {
	t.Run("parses session names, trims blanks, dedupes", func(t *testing.T) {
		mockShell := &shell.MockShell{}
		tm := &RealTmux{shell: mockShell, bin: "tmux"}
		mockShell.EXPECT().
			Cmd("tmux", "list-clients", "-F", "#{client_session}").
			Return("main\n\nside\nmain\n", nil)

		got, err := tm.ListClients()
		require.NoError(t, err)
		assert.Equal(t, []string{"main", "side"}, got)
	})

	t.Run("returns empty when no tmux server (Cmd returns empty string)", func(t *testing.T) {
		// shell.Cmd 把 "no server running on …" 的 stderr 特判成 ("", nil)，
		// ListClients 应该等价于"没人 attach 任何 session"。
		mockShell := &shell.MockShell{}
		tm := &RealTmux{shell: mockShell, bin: "tmux"}
		mockShell.EXPECT().
			Cmd("tmux", "list-clients", "-F", "#{client_session}").
			Return("", nil)

		got, err := tm.ListClients()
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("propagates real errors (binary missing, etc.)", func(t *testing.T) {
		mockShell := &shell.MockShell{}
		tm := &RealTmux{shell: mockShell, bin: "tmux"}
		wantErr := errors.New("tmux: command not found")
		mockShell.EXPECT().
			Cmd("tmux", "list-clients", "-F", "#{client_session}").
			Return("", wantErr)

		got, err := tm.ListClients()
		assert.ErrorIs(t, err, wantErr)
		assert.Nil(t, got)
	})
}

func TestListWindowPanes(t *testing.T) {
	t.Run("parses pane indexes in tmux order", func(t *testing.T) {
		mockShell := &shell.MockShell{}
		tm := &RealTmux{shell: mockShell, bin: "tmux"}
		mockShell.EXPECT().
			ListCmd("tmux", "list-panes", "-t", "s:2", "-F", "#{pane_index}").
			Return([]string{"0", "1", "2"}, nil)

		got, err := tm.ListWindowPanes("s:2")
		require.NoError(t, err)
		assert.Equal(t, []int{0, 1, 2}, got)
	})

	t.Run("fail-soft: ListCmd error returns empty slice and nil error", func(t *testing.T) {
		mockShell := &shell.MockShell{}
		tm := &RealTmux{shell: mockShell, bin: "tmux"}
		mockShell.EXPECT().
			ListCmd("tmux", "list-panes", "-t", "gone:9", "-F", "#{pane_index}").
			Return(nil, errors.New("can't find window"))

		got, err := tm.ListWindowPanes("gone:9")
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("skips blank lines", func(t *testing.T) {
		mockShell := &shell.MockShell{}
		tm := &RealTmux{shell: mockShell, bin: "tmux"}
		mockShell.EXPECT().
			ListCmd("tmux", "list-panes", "-t", "s:1", "-F", "#{pane_index}").
			Return([]string{"0", "", " ", "1"}, nil)

		got, err := tm.ListWindowPanes("s:1")
		require.NoError(t, err)
		assert.Equal(t, []int{0, 1}, got)
	})
}
