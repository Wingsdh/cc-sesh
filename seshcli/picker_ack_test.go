package seshcli

// Partition table (ECP + BVA) — step: 04-connect-window — seshcli.sessionNameOf
//
// 用例编号（tc-aXX）对应 ai-plans/testcases-picker.md §1.9。
//
//	输入            等价类                  期望           来源
//	"proj-a:3"      window 目标(有效)       "proj-a"       tc-a57
//	"proj-a"        纯会话名(有效)          "proj-a"       tc-a58
//	""              空字符串(边界)          ""             行为契约「chosen==""→不 Ack 不 Connect」的输入侧边界
//	"a:b"           冒号后非数字(无效)      "a:b"（原样）  行为契约「不是'名字:数字'形态就原样返回」
//	"a:b:1"         两个冒号(无效)          "a:b:1"（原样）判定与 parseWindowTarget 同款：仅一个冒号
//	"proj:01"       前导0(有效·边界)        "proj"         判定与 parseWindowTarget 同款：前导0合法
//	"proj:"         冒号后为空(无效·边界)   "proj:"（原样）判定与 parseWindowTarget 同款
//	":3"            会话名为空(无效·边界)   ":3"（原样）   判定与 parseWindowTarget 同款
//
// pairwise：不适用——sessionNameOf 只有 1 个字符串输入维度，未达到 ≥3 参数门槛。
//
// 关于 seshcli/picker_ack_test.go 的范围说明：
// NewPickerCommand 的 RunE 是一个内联 cobra 闭包，Ack(sessionNameOf(chosen)) +
// Connect(chosen) 这段逻辑没有被抽成任何独立可测的函数——它与真实 cobra flag 解析、
// deps.Picker.Pick()（内部会真的起一个 tea.NewProgram 交互循环）耦合在一起，
// 在不重构生产代码结构的前提下无法从外部单测这段"选中结果 → Ack/Connect 调用组合"
// 的端到端行为（覆盖锚点里"proj-a→Ack+Connect(proj-a)"/"proj-a:3→Ack(proj-a)+
// Connect(proj-a:3)"/"''→都不调"这三条，本质是在验证这段内联逻辑，而不是
// sessionNameOf 本身）。按团队负责人指示：不为了可测性去改生产代码结构，这是
// GREEN 阶段的判断而非 RED 阶段的职责；本文件只测 sessionNameOf 这一个纯函数，
// 其余覆盖锚点条目暂缺，已在 SendMessage 回信里向 team-lead 说明。

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSessionNameOf(t *testing.T) {
	cases := []struct {
		name   string
		chosen string
		want   string
	}{
		{"window 目标拆出会话名(tc-a57)", "proj-a:3", "proj-a"},
		{"纯会话名原样返回(tc-a58)", "proj-a", "proj-a"},
		{"空字符串原样返回(边界)", "", ""},
		{"冒号后非数字原样返回", "a:b", "a:b"},
		{"两个冒号原样返回", "a:b:1", "a:b:1"},
		{"前导0合法(同 parseWindowTarget)", "proj:01", "proj"},
		{"冒号后为空原样返回(同 parseWindowTarget)", "proj:", "proj:"},
		{"会话名为空原样返回(同 parseWindowTarget)", ":3", ":3"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, sessionNameOf(tc.chosen))
		})
	}
}
