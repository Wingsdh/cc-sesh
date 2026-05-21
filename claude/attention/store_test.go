package attention

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type frozenClock struct{ t time.Time }

func (f *frozenClock) Now() time.Time { return f.t }

func ptr[T any](v T) *T { return &v }

func newStore(t *testing.T, clk Clock) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "cc-sesh", "attention.json")
	s := New(path)
	if clk != nil {
		s.WithClock(clk)
	}
	return s, path
}

// 第一次看到 busy 不触发 flag —— 仅记录 tracking。
func TestReconcile_BusyAloneDoesNotTrigger(t *testing.T) {
	clk := &frozenClock{t: time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)}
	s, _ := newStore(t, clk)

	require.NoError(t, s.Reconcile(map[string]bool{"a": true}, []string{"a"}, nil))

	assert.Empty(t, s.Load(), "纯 busy 不该触发 flag")
}

// busy → idle 转换才触发 flag。
func TestReconcile_BusyToIdleTriggers(t *testing.T) {
	t0 := time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)
	clk := &frozenClock{t: t0}
	s, _ := newStore(t, clk)

	// 第一轮：a busy
	require.NoError(t, s.Reconcile(map[string]bool{"a": true}, []string{"a"}, nil))
	require.Empty(t, s.Load())

	// 第二轮：a 不再 busy（变 idle）→ 触发
	clk.t = t0.Add(2 * time.Minute)
	require.NoError(t, s.Reconcile(map[string]bool{"a": false}, []string{"a"}, nil))

	flags := s.Load()
	require.Contains(t, flags, "a")
	assert.Equal(t, clk.t, flags["a"].FirstAt)
}

// 从未 busy 直接 idle 不触发（避免冷启动每个 idle 都被标记）。
func TestReconcile_IdleFromColdStartDoesNotTrigger(t *testing.T) {
	clk := &frozenClock{t: time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)}
	s, _ := newStore(t, clk)

	require.NoError(t, s.Reconcile(map[string]bool{"a": false}, []string{"a"}, nil))

	assert.Empty(t, s.Load())
}

// flag 触发后是粘性的，后续 Reconcile 不会清除。
func TestReconcile_FlagIsSticky(t *testing.T) {
	clk := &frozenClock{t: time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)}
	s, _ := newStore(t, clk)

	// busy → idle：触发
	require.NoError(t, s.Reconcile(map[string]bool{"a": true}, []string{"a"}, nil))
	require.NoError(t, s.Reconcile(map[string]bool{"a": false}, []string{"a"}, nil))
	require.Contains(t, s.Load(), "a")

	// 又一轮 idle：flag 仍在
	require.NoError(t, s.Reconcile(map[string]bool{"a": false}, []string{"a"}, nil))
	assert.Contains(t, s.Load(), "a")
}

// FirstAt 在已存在 flag 时不被覆盖。
func TestReconcile_PreservesFirstAtAcrossRetrigger(t *testing.T) {
	t0 := time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)
	clk := &frozenClock{t: t0}
	s, _ := newStore(t, clk)

	// 第一次转换：触发 t0
	require.NoError(t, s.Reconcile(map[string]bool{"x": true}, []string{"x"}, nil))
	require.NoError(t, s.Reconcile(map[string]bool{"x": false}, []string{"x"}, nil))
	first := s.Load()["x"].FirstAt

	// 又跑一轮再回 idle：flag 仍存在，FirstAt 不变
	clk.t = t0.Add(10 * time.Minute)
	require.NoError(t, s.Reconcile(map[string]bool{"x": true}, []string{"x"}, nil))
	clk.t = t0.Add(15 * time.Minute)
	require.NoError(t, s.Reconcile(map[string]bool{"x": false}, []string{"x"}, nil))

	assert.Equal(t, first, s.Load()["x"].FirstAt)
}

func TestAck_RemovesFlag(t *testing.T) {
	clk := &frozenClock{t: time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)}
	s, _ := newStore(t, clk)

	require.NoError(t, s.Reconcile(map[string]bool{"a": true, "b": true}, []string{"a", "b"}, nil))
	require.NoError(t, s.Reconcile(map[string]bool{"a": false, "b": false}, []string{"a", "b"}, nil))

	require.NoError(t, s.Ack("a"))
	flags := s.Load()
	assert.NotContains(t, flags, "a")
	assert.Contains(t, flags, "b")
}

func TestAck_NoOpWhenAbsent(t *testing.T) {
	s, _ := newStore(t, &frozenClock{t: time.Now()})
	assert.NoError(t, s.Ack("nonexistent"))
}

// Ack 也清 tracking：避免 ack 后又出现 idle 立即重新触发。
func TestAck_ClearsTrackingToo(t *testing.T) {
	clk := &frozenClock{t: time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)}
	s, _ := newStore(t, clk)

	// busy → idle → 触发 flag
	require.NoError(t, s.Reconcile(map[string]bool{"a": true}, []string{"a"}, nil))
	require.NoError(t, s.Reconcile(map[string]bool{"a": false}, []string{"a"}, nil))
	require.NoError(t, s.Ack("a"))

	// 又一轮 busy → 又一轮 idle：要想重新触发，必须重新走完转换
	require.NoError(t, s.Reconcile(map[string]bool{"a": false}, []string{"a"}, nil))
	assert.NotContains(t, s.Load(), "a", "ack 后再来 idle 不该立即触发")

	// 走完完整 busy→idle 又会触发
	require.NoError(t, s.Reconcile(map[string]bool{"a": true}, []string{"a"}, nil))
	require.NoError(t, s.Reconcile(map[string]bool{"a": false}, []string{"a"}, nil))
	assert.Contains(t, s.Load(), "a")
}

func TestReconcile_GarbageCollectsDeadSessions(t *testing.T) {
	clk := &frozenClock{t: time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)}
	s, _ := newStore(t, clk)

	// 触发两个
	require.NoError(t, s.Reconcile(map[string]bool{"alive": true, "dead": true}, []string{"alive", "dead"}, nil))
	require.NoError(t, s.Reconcile(map[string]bool{"alive": false, "dead": false}, []string{"alive", "dead"}, nil))

	// 下一轮 dead 不在 active 列表
	require.NoError(t, s.Reconcile(nil, []string{"alive"}, nil))

	flags := s.Load()
	assert.Contains(t, flags, "alive")
	assert.NotContains(t, flags, "dead")
}

func TestReconcile_NilActiveListSkipsGC(t *testing.T) {
	clk := &frozenClock{t: time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)}
	s, _ := newStore(t, clk)

	require.NoError(t, s.Reconcile(map[string]bool{"a": true}, []string{"a"}, nil))
	require.NoError(t, s.Reconcile(map[string]bool{"a": false}, []string{"a"}, nil))
	require.Contains(t, s.Load(), "a")

	require.NoError(t, s.Reconcile(nil, nil, nil)) // 不做 GC
	assert.Contains(t, s.Load(), "a")
}

func TestPersistence_RoundTrip(t *testing.T) {
	clk := &frozenClock{t: time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)}
	s, path := newStore(t, clk)

	require.NoError(t, s.Reconcile(map[string]bool{"x": true}, []string{"x"}, nil))
	require.NoError(t, s.Reconcile(map[string]bool{"x": false}, []string{"x"}, nil))

	_, err := os.Stat(path)
	require.NoError(t, err)

	s2 := New(path)
	flags := s2.Load()
	require.Contains(t, flags, "x")
	assert.Equal(t, clk.t, flags["x"].FirstAt)
}

// tracking 也要持久化：跨进程 busy→idle 转换仍能识别。
func TestPersistence_TrackingSurvivesRestart(t *testing.T) {
	clk := &frozenClock{t: time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)}
	s, path := newStore(t, clk)

	// 第一进程：仅看到 busy，写 tracking 但无 flag
	require.NoError(t, s.Reconcile(map[string]bool{"x": true}, []string{"x"}, nil))
	require.Empty(t, s.Load())

	// 第二进程：只看到 idle —— 应该识别出 busy→idle 转换并触发
	s2 := New(path).WithClock(clk)
	require.NoError(t, s2.Reconcile(map[string]bool{"x": false}, []string{"x"}, nil))
	assert.Contains(t, s2.Load(), "x")
}

func TestLoad_MissingFileReturnsEmpty(t *testing.T) {
	s, _ := newStore(t, &frozenClock{t: time.Now()})
	assert.Empty(t, s.Load())
}

func TestLoad_CorruptFileReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "attention.json")
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o644))

	s := New(path)
	assert.Empty(t, s.Load())
}

func TestClear_RemovesAll(t *testing.T) {
	clk := &frozenClock{t: time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)}
	s, _ := newStore(t, clk)

	require.NoError(t, s.Reconcile(map[string]bool{"a": true, "b": true}, []string{"a", "b"}, nil))
	require.NoError(t, s.Reconcile(map[string]bool{"a": false, "b": false}, []string{"a", "b"}, nil))

	require.NoError(t, s.Clear())
	assert.Empty(t, s.Load())
}

// Bug 1 回归：cc 进程主动退出（busyByName 里没了，但 tmux session 仍活着）
// 不该触发 attention flag —— 这不是「跑完一轮活」，是用户自己关掉了 cc。
func TestReconcile_CCDisappearedDoesNotTrigger(t *testing.T) {
	clk := &frozenClock{t: time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)}
	s, _ := newStore(t, clk)

	// 第一轮：a busy
	require.NoError(t, s.Reconcile(map[string]bool{"a": true}, []string{"a"}, nil))

	// 第二轮：cc 退出了，busyByName 里没 a，但 tmux session "a" 还活着
	require.NoError(t, s.Reconcile(map[string]bool{}, []string{"a"}, nil))

	assert.Empty(t, s.Load(), "cc 进程消失不该触发 flag")

	// 后续：cc 再次启动 idle 不该立即触发（tracking 已被清掉）
	require.NoError(t, s.Reconcile(map[string]bool{"a": false}, []string{"a"}, nil))
	assert.Empty(t, s.Load(), "cc 消失后 tracking 应该清掉，避免下次 idle 误触发")
}

// 关键边界：live cc 还在但 Busy+Subagent==0（即 cc 还活着但变 idle）
// 仍应触发 flag —— 防止把"idle live cc"和"cc 消失"混掉。
func TestReconcile_LiveButIdleStillTriggers(t *testing.T) {
	clk := &frozenClock{t: time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)}
	s, _ := newStore(t, clk)

	require.NoError(t, s.Reconcile(map[string]bool{"a": true}, []string{"a"}, nil))
	// cc 还在（在 busyByName 里），但变 idle
	require.NoError(t, s.Reconcile(map[string]bool{"a": false}, []string{"a"}, nil))

	assert.Contains(t, s.Load(), "a", "cc 活着但 idle 仍应触发 flag")
}

// Bug 2 回归：当前 attached 的 session 不该触发 flag。
func TestReconcile_SuppressedSessionDoesNotTrigger(t *testing.T) {
	clk := &frozenClock{t: time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)}
	s, _ := newStore(t, clk)

	// 第一轮：a busy，用户也在 a
	require.NoError(t, s.Reconcile(map[string]bool{"a": true}, []string{"a"}, []string{"a"}))
	// 第二轮：a idle，用户仍在 a → 不该写 flag
	require.NoError(t, s.Reconcile(map[string]bool{"a": false}, []string{"a"}, []string{"a"}))

	assert.NotContains(t, s.Load(), "a", "当前 attach 的 session 不该被打 flag")
}

// Bug 2 回归：当前 attached 的 session 上若已有遗留 flag，下一轮 Reconcile 清掉。
func TestReconcile_SuppressedClearsExistingFlag(t *testing.T) {
	clk := &frozenClock{t: time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)}
	s, _ := newStore(t, clk)

	// 先在用户不在场时正常触发 flag
	require.NoError(t, s.Reconcile(map[string]bool{"a": true}, []string{"a"}, nil))
	require.NoError(t, s.Reconcile(map[string]bool{"a": false}, []string{"a"}, nil))
	require.Contains(t, s.Load(), "a")

	// 用户走到 a，下一轮 Reconcile 应该把 flag 清掉
	require.NoError(t, s.Reconcile(map[string]bool{"a": false}, []string{"a"}, []string{"a"}))
	assert.NotContains(t, s.Load(), "a", "进入 session 后已有 flag 应被清")
}

// Bug 2 回归：用户在 X 期间 cc busy，离开 X 后 cc 完成，应该正常触发 flag。
// 验证 suppress 不会破坏 tracking 维护。
func TestReconcile_TrackingSurvivesSuppression(t *testing.T) {
	clk := &frozenClock{t: time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)}
	s, _ := newStore(t, clk)

	// 用户在 a，cc 在 a busy
	require.NoError(t, s.Reconcile(map[string]bool{"a": true}, []string{"a", "b"}, []string{"a"}))
	// 用户切到 b，cc 在 a 仍 busy
	require.NoError(t, s.Reconcile(map[string]bool{"a": true}, []string{"a", "b"}, []string{"b"}))
	// cc 在 a 完成（idle），用户还在 b → 应该触发 a 的 flag
	require.NoError(t, s.Reconcile(map[string]bool{"a": false}, []string{"a", "b"}, []string{"b"}))

	assert.Contains(t, s.Load(), "a", "用户离开后 cc 完成应触发 flag")
}

// 状态机覆盖：cc 退出时用户也在场（Bug 1 ∩ Bug 2 的叠加）。
// 不应触发 flag、不应留 tracking、suppress 路径不应回写 flag。
func TestReconcile_CCDisappearedWhileSuppressed(t *testing.T) {
	clk := &frozenClock{t: time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)}
	s, _ := newStore(t, clk)

	// 用户在 a，cc 在 a busy
	require.NoError(t, s.Reconcile(map[string]bool{"a": true}, []string{"a"}, []string{"a"}))
	// cc 退出，用户仍在 a
	require.NoError(t, s.Reconcile(map[string]bool{}, []string{"a"}, []string{"a"}))

	assert.Empty(t, s.Load(), "用户在场 + cc 退出：不该有任何 flag 残留")

	// 下一轮 cc 重启 idle，用户仍在 a → 仍不该有 flag
	require.NoError(t, s.Reconcile(map[string]bool{"a": false}, []string{"a"}, []string{"a"}))
	assert.Empty(t, s.Load())
}

// 状态机覆盖：cc 退出 + 之前已留 flag（用户离开过又没看就跑了）。
// cc 退出本身不应改变已有 flag（flag 是粘性的），但 tracking 要清。
func TestReconcile_CCDisappearedPreservesExistingFlag(t *testing.T) {
	clk := &frozenClock{t: time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)}
	s, _ := newStore(t, clk)

	// 先制造一个 flag：busy→idle 触发
	require.NoError(t, s.Reconcile(map[string]bool{"a": true}, []string{"a"}, nil))
	require.NoError(t, s.Reconcile(map[string]bool{"a": false}, []string{"a"}, nil))
	require.Contains(t, s.Load(), "a")

	// 用户没 ack，cc 又跑了一轮然后这次直接退出
	require.NoError(t, s.Reconcile(map[string]bool{"a": true}, []string{"a"}, nil))
	require.NoError(t, s.Reconcile(map[string]bool{}, []string{"a"}, nil))

	flags := s.Load()
	assert.Contains(t, flags, "a", "cc 退出不应破坏已有 flag（粘性）")
}

// 状态机覆盖：用户回到一个 cc 还在 busy 的 session，旧 flag 应被清。
// 这是 "回来时如果已有 flag，无论 cc 还在不在跑都该清" 的语义。
func TestReconcile_SuppressedClearsFlagEvenWhileBusy(t *testing.T) {
	clk := &frozenClock{t: time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)}
	s, _ := newStore(t, clk)

	// 制造 flag
	require.NoError(t, s.Reconcile(map[string]bool{"a": true}, []string{"a"}, nil))
	require.NoError(t, s.Reconcile(map[string]bool{"a": false}, []string{"a"}, nil))
	require.Contains(t, s.Load(), "a")

	// 用户回到 a，但 a 又开始跑了（busy=true）
	require.NoError(t, s.Reconcile(map[string]bool{"a": true}, []string{"a"}, []string{"a"}))
	assert.NotContains(t, s.Load(), "a", "用户在场，无论 cc 在跑还是 idle，旧 flag 都该清")
}

// 状态机覆盖：cc 在 busy 时不应清掉已有 flag（除非用户在场）。
// 防止"用户离开 → cc 又开跑 → 旧 flag 被悄悄抹掉"的回归。
func TestReconcile_BusyDoesNotClearExistingFlagWhileNotSuppressed(t *testing.T) {
	clk := &frozenClock{t: time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)}
	s, _ := newStore(t, clk)

	// 制造 flag
	require.NoError(t, s.Reconcile(map[string]bool{"a": true}, []string{"a"}, nil))
	require.NoError(t, s.Reconcile(map[string]bool{"a": false}, []string{"a"}, nil))
	require.Contains(t, s.Load(), "a")

	// cc 又开始跑（busy=true），用户没回来
	require.NoError(t, s.Reconcile(map[string]bool{"a": true}, []string{"a"}, nil))
	assert.Contains(t, s.Load(), "a", "用户不在场时 cc 重跑不该清旧 flag")
}

// 状态机覆盖：多 client 同时 attach 不同 session 的 suppress 集合行为。
// 第三个 session 不在 suppress 里，应按正常状态机走。
func TestReconcile_MultipleSuppressedSessions(t *testing.T) {
	clk := &frozenClock{t: time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)}
	s, _ := newStore(t, clk)

	// 用户 client A 在 a，client B 在 b，c 没人看
	// 三个都 busy
	require.NoError(t, s.Reconcile(
		map[string]bool{"a": true, "b": true, "c": true},
		[]string{"a", "b", "c"},
		[]string{"a", "b"},
	))
	// 三个都 idle
	require.NoError(t, s.Reconcile(
		map[string]bool{"a": false, "b": false, "c": false},
		[]string{"a", "b", "c"},
		[]string{"a", "b"},
	))

	flags := s.Load()
	assert.NotContains(t, flags, "a", "client A 看着的 a 不该有 flag")
	assert.NotContains(t, flags, "b", "client B 看着的 b 不该有 flag")
	assert.Contains(t, flags, "c", "没人看的 c 应正常触发 flag")
}

// 状态机覆盖：Ack 之后 cc 再次 busy→idle + 用户当时在场。
// 双重保护：Ack 已清干净 + suppress 又防止再触发。
func TestReconcile_AckThenSuppressedTransitionStaysClean(t *testing.T) {
	clk := &frozenClock{t: time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)}
	s, _ := newStore(t, clk)

	// busy→idle → flag → Ack
	require.NoError(t, s.Reconcile(map[string]bool{"a": true}, []string{"a"}, nil))
	require.NoError(t, s.Reconcile(map[string]bool{"a": false}, []string{"a"}, nil))
	require.NoError(t, s.Ack("a"))
	require.Empty(t, s.Load())

	// 用户进 a，cc 又跑然后停
	require.NoError(t, s.Reconcile(map[string]bool{"a": true}, []string{"a"}, []string{"a"}))
	require.NoError(t, s.Reconcile(map[string]bool{"a": false}, []string{"a"}, []string{"a"}))

	assert.Empty(t, s.Load(), "Ack + 用户在场期间不该长出新 flag")
}

// 状态机覆盖：跨进程 + suppress 的交互。
// 进程 A 留下 tracking[x]=true，进程 B 启动时 x 在 suppress 里，x 变 idle —
// suppress 应正确阻止 flag，tracking 也按转换清掉。
func TestReconcile_PersistedTrackingHonorsSuppress(t *testing.T) {
	clk := &frozenClock{t: time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)}
	s, path := newStore(t, clk)

	// 进程 A：x busy，持久化 tracking[x]=true
	require.NoError(t, s.Reconcile(map[string]bool{"x": true}, []string{"x"}, nil))

	// 进程 B：x 变 idle，用户此时正在 x
	s2 := New(path).WithClock(clk)
	require.NoError(t, s2.Reconcile(map[string]bool{"x": false}, []string{"x"}, []string{"x"}))

	assert.NotContains(t, s2.Load(), "x", "跨进程持久 tracking 也要受 suppress 保护")
}

// 状态机表驱动覆盖：一次性把"上一轮 tracking + 本轮 cc 状态 + suppress
// + 进入 flag"的关键组合钉死。每行都来自前面 4 轮 review 中识别的真实场景，
// 任何一行回归都意味着两个核心状态（cc 退出 / 用户在场）被破坏。
func TestReconcile_StateMachineMatrix(t *testing.T) {
	t0 := time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)

	cases := []struct {
		name        string
		seedBusy    *bool  // 第一轮 busy 状态：nil 表示跳过 seed
		seedFlag    bool   // 是否在 seed 后注入一个 flag
		thisBusy    *bool  // 第二轮 busy 状态：nil 表示 missing（cc 退出）
		suppressed  bool   // 第二轮是否在 suppress 里
		wantFlag    bool   // 第二轮后是否应有 flag
		wantTracked bool   // 第二轮后 tracking 是否仍为 true
	}{
		{
			name:        "busy→idle, 不 suppress, 无旧 flag → 触发",
			seedBusy:    ptr(true),
			thisBusy:    ptr(false),
			suppressed:  false,
			wantFlag:    true,
			wantTracked: false,
		},
		{
			name:        "busy→idle, suppress, 无旧 flag → 不触发, 用户在场",
			seedBusy:    ptr(true),
			thisBusy:    ptr(false),
			suppressed:  true,
			wantFlag:    false,
			wantTracked: false,
		},
		{
			name:        "busy→missing(cc 退出), 不 suppress, 无旧 flag → 不触发",
			seedBusy:    ptr(true),
			thisBusy:    nil,
			suppressed:  false,
			wantFlag:    false,
			wantTracked: false,
		},
		{
			name:        "busy→missing(cc 退出), suppress, 无旧 flag → 不触发",
			seedBusy:    ptr(true),
			thisBusy:    nil,
			suppressed:  true,
			wantFlag:    false,
			wantTracked: false,
		},
		{
			name:        "idle→idle, 不 suppress, 无旧 flag → 不触发",
			seedBusy:    ptr(false),
			thisBusy:    ptr(false),
			suppressed:  false,
			wantFlag:    false,
			wantTracked: false,
		},
		{
			name:        "idle→busy, 不 suppress, 无旧 flag → 不触发, 开始 track",
			seedBusy:    ptr(false),
			thisBusy:    ptr(true),
			suppressed:  false,
			wantFlag:    false,
			wantTracked: true,
		},
		{
			name:        "busy→busy, suppress, 有旧 flag → flag 被清",
			seedBusy:    ptr(true),
			seedFlag:    true,
			thisBusy:    ptr(true),
			suppressed:  true,
			wantFlag:    false,
			wantTracked: true,
		},
		{
			name:        "busy→busy, 不 suppress, 有旧 flag → flag 保留",
			seedBusy:    ptr(true),
			seedFlag:    true,
			thisBusy:    ptr(true),
			suppressed:  false,
			wantFlag:    true,
			wantTracked: true,
		},
		{
			name:        "missing→missing(cc 一直不在), suppress, 有旧 flag → 清掉",
			thisBusy:    nil,
			seedFlag:    true,
			suppressed:  true,
			wantFlag:    false,
			wantTracked: false,
		},
		{
			name:        "missing→missing, 不 suppress, 有旧 flag → 保留（粘性）",
			thisBusy:    nil,
			seedFlag:    true,
			suppressed:  false,
			wantFlag:    true,
			wantTracked: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clk := &frozenClock{t: t0}
			s, _ := newStore(t, clk)

			// seed 阶段：先建立 tracking 状态
			if tc.seedBusy != nil {
				require.NoError(t, s.Reconcile(
					map[string]bool{"x": *tc.seedBusy},
					[]string{"x"}, nil,
				))
			}

			// 可选地注入一个 flag（模拟用户离开过又没看的历史 flag）
			if tc.seedFlag {
				clk.t = t0.Add(1 * time.Minute)
				require.NoError(t, s.Reconcile(map[string]bool{"x": true}, []string{"x"}, nil))
				clk.t = t0.Add(2 * time.Minute)
				require.NoError(t, s.Reconcile(map[string]bool{"x": false}, []string{"x"}, nil))
				require.Contains(t, s.Load(), "x", "seedFlag 阶段应已写入 flag")
				// 如果后续测的是 busy 状态，再 seed 一轮把 tracking 立起来
				if tc.thisBusy != nil && *tc.thisBusy {
					clk.t = t0.Add(3 * time.Minute)
					require.NoError(t, s.Reconcile(map[string]bool{"x": true}, []string{"x"}, nil))
				}
			}

			// 第二轮：实际要断言的 Reconcile
			clk.t = t0.Add(10 * time.Minute)
			var suppress []string
			if tc.suppressed {
				suppress = []string{"x"}
			}
			busy := map[string]bool{}
			if tc.thisBusy != nil {
				busy["x"] = *tc.thisBusy
			}
			require.NoError(t, s.Reconcile(busy, []string{"x"}, suppress))

			flags := s.Load()
			if tc.wantFlag {
				assert.Contains(t, flags, "x", "应有 flag")
			} else {
				assert.NotContains(t, flags, "x", "不应有 flag")
			}

			// 同包可访问私有 tracking；测试 goroutine 单线程使用，无并发安全问题。
			s.mu.Lock()
			tracked := s.tracking["x"]
			s.mu.Unlock()
			assert.Equal(t, tc.wantTracked, tracked,
				"tracking[x] 应为 %v，实际 %v", tc.wantTracked, tracked)
		})
	}
}

// seedFlagAndTracking 把 store 推到"x 有 flag + 有 tracking"的状态：
// busy → idle 写 flag → 再 busy 重立 tracking。返回后两者都真实存在。
func seedFlagAndTracking(t *testing.T, s *Store, name string) {
	t.Helper()
	require.NoError(t, s.Reconcile(map[string]bool{name: true}, []string{name}, nil))
	require.NoError(t, s.Reconcile(map[string]bool{name: false}, []string{name}, nil))
	require.Contains(t, s.Load(), name, "seed 阶段应已写入 flag")
	require.NoError(t, s.Reconcile(map[string]bool{name: true}, []string{name}, nil))
	s.mu.Lock()
	_, tracked := s.tracking[name]
	s.mu.Unlock()
	require.True(t, tracked, "seed 阶段应已立起 tracking")
}

// 状态机覆盖：纯 GC（无 suppress 干扰）必须自己清掉 dead session 的 flag 和 tracking。
// 不能依赖 suppress 路径"顺便"清 flag —— 否则 dead session 不在任何 client 视野时
// flag 会永远留在 store 里。
func TestReconcile_DeadSessionGCRemovesFlagAndTracking(t *testing.T) {
	clk := &frozenClock{t: time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)}
	s, _ := newStore(t, clk)
	seedFlagAndTracking(t, s, "x")

	// session x 被 kill，suppress 不包含 x —— 完全靠 GC 路径清
	require.NoError(t, s.Reconcile(map[string]bool{}, []string{}, nil))

	flags := s.Load()
	assert.NotContains(t, flags, "x", "纯 GC 应清掉 dead session 的 flag")

	s.mu.Lock()
	_, stillTracked := s.tracking["x"]
	s.mu.Unlock()
	assert.False(t, stillTracked, "纯 GC 应清掉 dead session 的 tracking")
}

// 状态机覆盖：dead session 同时在 suppress 列表里（边界叠加）——
// 不应让 suppress 的 sticky 语义阻止 GC，结果仍是清干净。
// 这个测试是上面纯 GC 的"叠加 suppress 也仍然正确"的回归挡板。
func TestReconcile_DeadSessionGCedEvenIfInSuppressList(t *testing.T) {
	clk := &frozenClock{t: time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)}
	s, _ := newStore(t, clk)
	seedFlagAndTracking(t, s, "x")

	// session x 被 kill + 还在 suppress 列表 —— GC 不应被 suppress 影响
	require.NoError(t, s.Reconcile(map[string]bool{}, []string{}, []string{"x"}))

	flags := s.Load()
	assert.NotContains(t, flags, "x", "dead session 应被 GC，即使在 suppress 列表里")

	s.mu.Lock()
	_, stillTracked := s.tracking["x"]
	s.mu.Unlock()
	assert.False(t, stillTracked, "dead session 的 tracking 也应被 GC")
}

// 状态机覆盖：cc 多实例同 session 的 idle 触发路径。
// 真实场景：一个 cc 退出后 session 还在 busyByName 里（剩下的实例 idle）—
// 这条路径必须把 busyByName[s]=false 当作"cc 完成"触发 flag，
// 不能误判成"cc 消失"。
func TestReconcile_MultiInstanceOneExitsRestIdleTriggers(t *testing.T) {
	clk := &frozenClock{t: time.Date(2026, 5, 3, 9, 0, 0, 0, time.UTC)}
	s, _ := newStore(t, clk)

	// 第一轮：session "x" 内有 2 个 cc，至少一个 busy → busyByName[x]=true
	require.NoError(t, s.Reconcile(map[string]bool{"x": true}, []string{"x"}, nil))

	// 第二轮：一个 cc 退出，另一个变 idle —— 但 session 里还有 live cc，
	// busyByName[x] 仍 = false（不是 missing），是合法 busy→idle 转换。
	require.NoError(t, s.Reconcile(map[string]bool{"x": false}, []string{"x"}, nil))

	assert.Contains(t, s.Load(), "x", "至少一个 cc 仍活着但变 idle 应触发 flag")
}

func TestDefaultPath_UsesXDGStateHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/custom/state")
	got, err := DefaultPath()
	require.NoError(t, err)
	assert.Equal(t, "/custom/state/cc-sesh/attention.json", got)
}

func TestDefaultPath_FallbackHomeDotLocal(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/test")
	got, err := DefaultPath()
	require.NoError(t, err)
	assert.Equal(t, "/home/test/.local/state/cc-sesh/attention.json", got)
}
