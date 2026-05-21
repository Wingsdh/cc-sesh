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
