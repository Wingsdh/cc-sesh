package seshcli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/Wingsdh/cc-sesh/v2/lister"
	"github.com/Wingsdh/cc-sesh/v2/model"
	"github.com/Wingsdh/cc-sesh/v2/picker"
)

// sessionNameOf 把 picker 的选中结果还原成会话名，用于 ATTN 确认。
// 判定与 connector 的 parseWindowTarget 同款：含且仅含一个冒号、冒号后非空且全为数字
// 才拆分；不是这个形态就原样返回（会话名本身不含冒号时零影响）。
func sessionNameOf(chosen string) string {
	if strings.Count(chosen, ":") != 1 {
		return chosen
	}
	sep := strings.Index(chosen, ":")
	session, digits := chosen[:sep], chosen[sep+1:]
	if session == "" || digits == "" {
		return chosen
	}
	for _, r := range digits {
		if r < '0' || r > '9' {
			return chosen
		}
	}
	return session
}

func NewPickerCommand(base *BaseDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "picker",
		Aliases: []string{"pick", "pk"},
		Short:   "Interactive session picker",
		RunE: func(cmd *cobra.Command, args []string) error {
			deps, err := buildDeps(cmd, base)
			if err != nil {
				return err
			}
			if deps.CachingLister != nil {
				defer deps.CachingLister.Wait()
			}

			config, _ := cmd.Flags().GetBool("config")
			tmux, _ := cmd.Flags().GetBool("tmux")
			zoxide, _ := cmd.Flags().GetBool("zoxide")
			hideAttached, _ := cmd.Flags().GetBool("hide-attached")
			tmuxinator, _ := cmd.Flags().GetBool("tmuxinator")
			hideDuplicates, _ := cmd.Flags().GetBool("hide-duplicates")

			listerOpts := lister.ListOptions{
				Config:         config,
				HideAttached:   hideAttached,
				Tmux:           tmux,
				Zoxide:         zoxide,
				Tmuxinator:     tmuxinator,
				HideDuplicates: hideDuplicates,
			}
			fetchFunc := makeClaudeFetcher(deps, listerOpts)

			var pickerOpts picker.PickerOptions
			pickerOpts.Dismisser = &claudeDismisser{store: deps.Attention}
			pickerOpts.Killer = &tmuxKiller{tmux: deps.Tmux, attention: deps.Attention}
			if deps.PickerUIState != nil {
				pickerOpts.ExpandStore = deps.PickerUIState
			}
			if deps.Tmux != nil {
				pickerOpts.Capturer = &tmuxCapturer{tmux: deps.Tmux}
			}
			if cmd.Flags().Changed("icons") {
				showIcons := true
				pickerOpts.ShowIcons = &showIcons
			} else {
				showIcons := deps.Config.TUI.ShowIcons
				pickerOpts.ShowIcons = &showIcons
			}
			if cmd.Flags().Changed("separator-aware") {
				separatorAware := true
				pickerOpts.SeparatorAware = &separatorAware
			}
			if cmd.Flags().Changed("prompt") {
				prompt, _ := cmd.Flags().GetString("prompt")
				pickerOpts.Prompt = &prompt
			} else if deps.Config.TUI.Prompt != "" {
				pickerOpts.Prompt = &deps.Config.TUI.Prompt
			}
			if cmd.Flags().Changed("placeholder") {
				placeholder, _ := cmd.Flags().GetString("placeholder")
				pickerOpts.Placeholder = &placeholder
			} else if deps.Config.TUI.Placeholder != "" {
				pickerOpts.Placeholder = &deps.Config.TUI.Placeholder
			}

			chosen, err := deps.Picker.Pick(fetchFunc, pickerOpts)
			if err != nil {
				return err
			}

			if chosen == "" {
				return nil
			}

			// attach 之前先 ack：用户「点进去」的语义就是清掉粘性标记。
			// chosen 可能是 "会话名:window序号"，attention 只按会话名跟踪，
			// 直接拿完整目标串去 Ack 会落空——用户从 ATTN 区进了 window，
			// 标记却还挂着。
			_ = deps.Attention.Ack(sessionNameOf(chosen))

			if _, err := deps.Connector.Connect(chosen, model.ConnectOpts{}); err != nil {
				return err
			}

			return nil
		},
	}

	cmd.Flags().BoolP("config", "c", false, "show configured sessions")
	cmd.Flags().BoolP("tmux", "t", false, "show tmux sessions")
	cmd.Flags().BoolP("zoxide", "z", false, "show zoxide results")
	cmd.Flags().BoolP("hide-attached", "H", false, "don't show currently attached sessions")
	cmd.Flags().BoolP("icons", "i", false, "show icons")
	cmd.Flags().BoolP("tmuxinator", "T", false, "show tmuxinator configs")
	cmd.Flags().BoolP("hide-duplicates", "d", false, "hide duplicate entries")
	cmd.Flags().BoolP("separator-aware", "s", false, "match spaces to separators (-_/\\)")
	cmd.Flags().StringP("prompt", "p", "", "prompt shown in the picker TUI")
	cmd.Flags().String("placeholder", "", "placeholder text in the picker TUI")

	return cmd
}
