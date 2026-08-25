package model

type TmuxWindow struct {
	Name   string
	Path   string
	Index  int
	Active bool
}

// TmuxWindowAcrossSessions 是跨所有 session 列出 window 时的精简视图。
// 与 TmuxWindow 的区别：带上所属 session 名，但不带 pane 路径 ——
// 调用方（picker 的折叠展开）只需要「哪个 session 下有哪些 window」。
// 照 TmuxPaneAcrossSessions 的先例单独建类型，不改既有 TmuxWindow。
type TmuxWindowAcrossSessions struct {
	SessionName string
	Name        string
	Index       int
	Active      bool
}
