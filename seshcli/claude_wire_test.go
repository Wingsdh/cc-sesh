package seshcli

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Wingsdh/cc-sesh/v2/claude/attention"
	"github.com/Wingsdh/cc-sesh/v2/claude/live"
	"github.com/Wingsdh/cc-sesh/v2/model"
	"github.com/Wingsdh/cc-sesh/v2/tmux"
)

func newAttentionStore(t *testing.T) *attention.Store {
	t.Helper()
	dir := t.TempDir()
	return attention.New(filepath.Join(dir, "attention.json"))
}

func makeSessions(names ...string) model.SeshSessions {
	s := model.SeshSessions{
		Directory:    model.SeshSessionMap{},
		OrderedIndex: make([]string, 0, len(names)),
	}
	for _, n := range names {
		s.Directory[n] = model.SeshSession{Src: "tmux", Name: n, Path: "/tmp/" + n}
		s.OrderedIndex = append(s.OrderedIndex, n)
	}
	return s
}

// 关键 wiring 行为：liveByName 中缺失的 session 必须被当作"cc 消失"，
// 不会被悄悄当成 idle 触发误 flag。
func TestReconcileAttention_MissingLiveIsTreatedAsCCDisappeared(t *testing.T) {
	store := newAttentionStore(t)
	mockTmux := &tmux.MockTmux{}
	mockTmux.EXPECT().ListClients().Return(nil, nil)

	sessions := makeSessions("alpha")

	// 第一轮：cc 在 alpha 里 busy
	flags := reconcileAttention(store, mockTmux, sessions,
		map[string]live.Status{"alpha": {Total: 1, Busy: 1}}, true)
	assert.Empty(t, flags, "纯 busy 不该触发 flag")

	// 第二轮：cc 消失（liveByName 里没 alpha 这个 key）
	mockTmux.EXPECT().ListClients().Return(nil, nil)
	flags = reconcileAttention(store, mockTmux, sessions, map[string]live.Status{}, true)
	assert.NotContains(t, flags, "alpha", "cc 消失不该被当成 busy→idle 触发 flag")

	mockTmux.AssertExpectations(t)
}

// 关键 wiring 行为：ListClients 返回的 session 名要进入 suppress 集合，
// 真的能阻止 flag 触发；这是 Bug 2 的 end-to-end 保护。
func TestReconcileAttention_ListClientsOutputBecomesSuppress(t *testing.T) {
	store := newAttentionStore(t)
	mockTmux := &tmux.MockTmux{}

	sessions := makeSessions("alpha")

	// 第一轮：alpha busy，用户也在 alpha
	mockTmux.EXPECT().ListClients().Return([]string{"alpha"}, nil).Once()
	_ = reconcileAttention(store, mockTmux, sessions,
		map[string]live.Status{"alpha": {Total: 1, Busy: 1}}, true)

	// 第二轮：alpha idle，用户仍在 alpha → 不该触发 flag
	mockTmux.EXPECT().ListClients().Return([]string{"alpha"}, nil).Once()
	flags := reconcileAttention(store, mockTmux, sessions,
		map[string]live.Status{"alpha": {Total: 1}}, true)

	assert.NotContains(t, flags, "alpha", "当前 attach 的 session 不该被打 flag")

	mockTmux.AssertExpectations(t)
}

// 关键 wiring 行为：ListClients 真的 error 时显式选择 fail-open —
// 降级为空 suppress、记 warn、状态机继续跑，flag 该触发还能触发。
// 反过来 fail-closed（拿不到 client list 就全部 suppress）会让用户永远
// 收不到提醒，比偶尔多提示更糟。这里把"err 时 busy→idle 仍能触发 flag"
// 也断言上，挡住后续把它改成 fail-closed 的回归。
func TestReconcileAttention_ListClientsErrorFailsOpen(t *testing.T) {
	store := newAttentionStore(t)
	mockTmux := &tmux.MockTmux{}

	sessions := makeSessions("alpha")

	// 第一轮：busy + ListClients err → fail-open，状态机跑通
	mockTmux.EXPECT().ListClients().Return(nil, errors.New("boom")).Once()
	require.NotPanics(t, func() {
		_ = reconcileAttention(store, mockTmux, sessions,
			map[string]live.Status{"alpha": {Total: 1, Busy: 1}}, true)
	})

	// 第二轮：idle + ListClients err → fail-open 下应该正常触发 flag
	mockTmux.EXPECT().ListClients().Return(nil, errors.New("boom")).Once()
	flags := reconcileAttention(store, mockTmux, sessions,
		map[string]live.Status{"alpha": {Total: 1}}, true)
	assert.Contains(t, flags, "alpha",
		"ListClients err 时应 fail-open：不知道 client 状态，宁可多提醒也不漏")

	mockTmux.AssertExpectations(t)
}

// 关键 wiring 行为：用户离开 attached session 后 cc 完成，flag 应正常触发。
// 验证 suppress 不会破坏 tracking 维护，端到端走通。
func TestReconcileAttention_FlagTriggersAfterUserLeaves(t *testing.T) {
	store := newAttentionStore(t)
	mockTmux := &tmux.MockTmux{}

	sessions := makeSessions("alpha", "beta")

	// 用户在 alpha，cc 在 alpha busy
	mockTmux.EXPECT().ListClients().Return([]string{"alpha"}, nil).Once()
	_ = reconcileAttention(store, mockTmux, sessions,
		map[string]live.Status{"alpha": {Total: 1, Busy: 1}}, true)

	// 用户切到 beta，cc 在 alpha 仍 busy
	mockTmux.EXPECT().ListClients().Return([]string{"beta"}, nil).Once()
	_ = reconcileAttention(store, mockTmux, sessions,
		map[string]live.Status{"alpha": {Total: 1, Busy: 1}}, true)

	// cc 在 alpha 完成（idle），用户还在 beta → 应触发 alpha 的 flag
	mockTmux.EXPECT().ListClients().Return([]string{"beta"}, nil).Once()
	flags := reconcileAttention(store, mockTmux, sessions,
		map[string]live.Status{"alpha": {Total: 1}}, true)

	assert.Contains(t, flags, "alpha", "用户离开后 cc 完成应触发 flag")
	assert.NotContains(t, flags, "beta")

	mockTmux.AssertExpectations(t)
}

// 状态机覆盖：subagent 状态也必须当 busy 看待（"在跑活"）。
// claude_wire 层把 Busy+Subagent>0 都聚合成 busyByName=true，
// 所以 Busy=0, Subagent>0 → idle 仍是合法 busy→idle 转换，应触发 flag。
// 反过来 needs-input（Needing）不算 busy —— 这条由 Needing 独立断言。
func TestReconcileAttention_SubagentIsTreatedAsBusy(t *testing.T) {
	store := newAttentionStore(t)
	mockTmux := &tmux.MockTmux{}

	sessions := makeSessions("alpha")

	// 第一轮：alpha 只有 subagent 在跑（Busy=0, Subagent=1）
	mockTmux.EXPECT().ListClients().Return(nil, nil).Once()
	_ = reconcileAttention(store, mockTmux, sessions,
		map[string]live.Status{"alpha": {Total: 1, Subagent: 1}}, true)

	// 第二轮：subagent 结束，alpha 变 idle —— 应触发 flag
	mockTmux.EXPECT().ListClients().Return(nil, nil).Once()
	flags := reconcileAttention(store, mockTmux, sessions,
		map[string]live.Status{"alpha": {Total: 1}}, true)

	assert.Contains(t, flags, "alpha", "subagent 也是'跑活'，结束后应触发 flag")
	mockTmux.AssertExpectations(t)
}

// 状态机覆盖：needs-input 单独不应被当 busy。
// 用户拒绝/忽略 permission prompt 时 cc 处于 waiting，但不是"在跑活"，
// 不该形成 busy→idle 转换的"起点"。
func TestReconcileAttention_NeedingAloneIsNotBusy(t *testing.T) {
	store := newAttentionStore(t)
	mockTmux := &tmux.MockTmux{}

	sessions := makeSessions("alpha")

	// 第一轮：alpha 只有 needs-input（不算 busy）
	mockTmux.EXPECT().ListClients().Return(nil, nil).Once()
	_ = reconcileAttention(store, mockTmux, sessions,
		map[string]live.Status{"alpha": {Total: 1, Needing: 1}}, true)

	// 第二轮：用户处理了 prompt，alpha 真的变 idle —— 不应触发 flag
	mockTmux.EXPECT().ListClients().Return(nil, nil).Once()
	flags := reconcileAttention(store, mockTmux, sessions,
		map[string]live.Status{"alpha": {Total: 1}}, true)

	assert.NotContains(t, flags, "alpha", "needs-input 不是'跑活'，不应形成 busy→idle 触发点")
	mockTmux.AssertExpectations(t)
}

// Codex Round-3 找到的 Medium Bug 回归：当 live read 失败（ReadInstances 或
// ListAllPanes 瞬时报错）时，liveByName 整个 nil/空，但旧代码仍传完整
// activeNames 给 Reconcile —— store 会把所有 session 都判成"cc 消失"，
// 清掉合法 tracking → 之后 cc 真的完成不再触发 flag。
// 现在的修法：liveOk=false 时 reconcileAttention 传 nil activeNames，
// 跳过"cc 消失清 tracking"和"GC dead session"两段，只跑 suppress 那一段。
func TestReconcileAttention_LiveReadFailureDoesNotEraseTracking(t *testing.T) {
	store := newAttentionStore(t)
	mockTmux := &tmux.MockTmux{}

	sessions := makeSessions("alpha")

	// 第一轮：live 正常，cc 在 alpha busy → tracking[alpha]=true
	mockTmux.EXPECT().ListClients().Return(nil, nil).Once()
	_ = reconcileAttention(store, mockTmux, sessions,
		map[string]live.Status{"alpha": {Total: 1, Busy: 1}}, true)

	// 第二轮：live read 失败，liveByName=nil & liveOk=false
	// 旧代码会把 alpha 判成"cc 消失"清掉 tracking。
	mockTmux.EXPECT().ListClients().Return(nil, nil).Once()
	_ = reconcileAttention(store, mockTmux, sessions, nil, false)

	// 第三轮：live 恢复，cc 在 alpha 真的变 idle → 必须触发 flag
	mockTmux.EXPECT().ListClients().Return(nil, nil).Once()
	flags := reconcileAttention(store, mockTmux, sessions,
		map[string]live.Status{"alpha": {Total: 1}}, true)

	assert.Contains(t, flags, "alpha",
		"live read 失败的一轮不该清掉 tracking，下一轮 idle 转换仍要触发 flag")

	mockTmux.AssertExpectations(t)
}
