package tmux

import (
	"strings"

	"github.com/Wingsdh/cc-sesh/v2/convert"
	"github.com/Wingsdh/cc-sesh/v2/model"
)

func listWindowsFormat() string {
	variables := []string{
		"#{window_index}",
		"#{window_name}",
		"#{pane_current_path}",
		"#{window_active}",
	}
	return strings.Join(variables, separator)
}

func (t *RealTmux) ListWindows(targetSession string) ([]*model.TmuxWindow, error) {
	var args []string
	args = append(args, "list-windows")
	if targetSession != "" {
		args = append(args, "-t", targetSession)
	}
	args = append(args, "-F", listWindowsFormat())

	output, err := t.shell.ListCmd("tmux", args...)
	if err != nil {
		return nil, err
	}
	return parseTmuxWindowsOutput(output)
}

func parseTmuxWindowsOutput(rawList []string) ([]*model.TmuxWindow, error) {
	windows := make([]*model.TmuxWindow, 0, len(rawList))
	for _, line := range rawList {
		fields := strings.Split(line, separator)
		if len(fields) != 4 {
			continue
		}
		windows = append(windows, &model.TmuxWindow{
			Index:  convert.StringToInt(fields[0]),
			Name:   fields[1],
			Path:   fields[2],
			Active: convert.StringToBool(fields[3]),
		})
	}
	return windows, nil
}

// listAllWindowsFormat 是 ListAllWindows 的 -F 格式串。
// 字段顺序（session_name, window_index, window_name, window_active）是解析契约的一部分，
// 与 model.TmuxWindowAcrossSessions 的字段声明顺序刻意不同，解析时必须按本顺序取值。
func listAllWindowsFormat() string {
	variables := []string{
		"#{session_name}",
		"#{window_index}",
		"#{window_name}",
		"#{window_active}",
	}
	return strings.Join(variables, separator)
}

// ListAllWindows 一次性列出所有 session 的 window，供 picker 展开时直接取用。
//
// fail-soft：ListCmd 报错时返回空切片 + nil error（照 ListAllPanes 先例）——
// 让 picker 退化成「全部 session 不可展开」，而不是整个列表崩掉。
func (t *RealTmux) ListAllWindows() ([]*model.TmuxWindowAcrossSessions, error) {
	output, err := t.shell.ListCmd("tmux", "list-windows", "-a", "-F", listAllWindowsFormat())
	if err != nil {
		return []*model.TmuxWindowAcrossSessions{}, nil
	}
	windows := make([]*model.TmuxWindowAcrossSessions, 0, len(output))
	for _, line := range output {
		fields := strings.Split(line, separator)
		if len(fields) != 4 {
			continue
		}
		windows = append(windows, &model.TmuxWindowAcrossSessions{
			SessionName: fields[0],
			Index:       convert.StringToInt(fields[1]),
			Name:        fields[2],
			Active:      convert.StringToBool(fields[3]),
		})
	}
	return windows, nil
}
