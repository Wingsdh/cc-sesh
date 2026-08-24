package picker

// Partition table (ECP + BVA) — step: 02-visible-rows — picker 包
//
// 用例编号（tc-aXX）对应 ai-plans/testcases-picker.md §1.1/1.2/1.3/1.4/1.10。
// 渲染样式（tc-a5/a72/a67 等纯渲染内容）不在本 step 范围内（plan §7 明确渲染留给 step03），
// 本文件只钉死「模型与交互」：可见行构建、展开集合分离、光标/滚动夹紧、搜索匹配范围。
//
// buildVisibleRows(filtered, expanded, temp, windowsBySession) 优先级链条 —
// pairwise 分析（3 个独立维度：inExpanded / inTemp / hasMatches，各 2 取值）：
//
//	uv run --script .../applying-pairwise-testing/scripts/pairwise.py \
//	  --params '{"inExpanded":["true","false"],"inTemp":["true","false"],"hasMatches":["true","false"]}' \
//	  --format table
//	# pairwise: 3 params, 4 combos (from 8 full combos)
//	inExpanded  inTemp  hasMatches
//	true        true    true
//	false       false   true
//	false       true    false
//	true        false   false
//
// 这条优先级链条是「展开哪些行」的核心分支，错一步就会让某个会话显示错误的行集合，
// 属于安全关键分支：在 pairwise 给出的 4 组最小集之上，补全到全部 2^3=8 组合做穷举
// （TestBuildVisibleRows_PriorityRules），而不是只测 pairwise 挑出的子集。
//
// ECP/BVA 划分表（节选，其余散落在各测试函数的行内注释里，均标了 tc- 编号）：
//
//	目标                          等价类                          期望                                  来源
//	buildVisibleRows              全部折叠                        只出 session 行，顺序不变            tc-a1
//	buildVisibleRows              手动展开+有window                紧随插入全部window行(Index升序)      tc-a2/a4
//	buildVisibleRows              手动展开+window表为空(边界)      不插入任何window行                   tc-a3
//	buildVisibleRows              搜索临时展开命中                只插入命中的window行                 tc-a62/a64
//	isExpandable                  tmux+window数>=1(下边界=1)      true
//	isExpandable                  tmux+window数=0(边界)           false                                 tc-a25
//	isExpandable                  非tmux来源                      false                                 tc-a24
//	windowTarget                  会话名不含冒号                  "session:index", true
//	windowTarget                  会话名含冒号(边界)               ok=false                              行为契约
//	clampCursor                   rows为空(下边界0)                cursor=offset=0
//	clampCursor                   cursor>len(rows)-1(越界)         收到末行                              tc-a8
//	clampCursor                   cursor<0(边界外)                 收到0
//	clampCursor                   offset>cursor                   offset=cursor                        tc-a9
//	clampCursor                   cursor>=offset+visibleCount()   offset前移                            tc-a9
//	ExpandStore.SaveExpanded      →展开可展开项                    调用且入参含该session                覆盖锚点①
//	ExpandStore.SaveExpanded      ←折起手动展开项                  调用且入参不含该session               覆盖锚点②/tc-a21
//	ExpandStore.SaveExpanded      ←折起仅temp展开项                零调用                                覆盖锚点③/tc-a20
//	ExpandStore.SaveExpanded      →不可展开项                      零调用                                覆盖锚点④/tc-a24/a25
//	ExpandStore.SaveExpanded      返回error(异常)                  picker仍照常渲染、不panic             覆盖锚点⑤
//
// pairwise（其余场景）：均为单维度或双维度穷举，未达到 ≥3 参数×≥2取值的强制门槛，
// 未再调用脚本，改用直接穷举/table-driven 覆盖。

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Wingsdh/cc-sesh/v2/model"
)

// ---------- 测试基础设施 ----------

// fakeExpandStore 是 ExpandStore 的可记录假实现：记录 LoadExpanded 调用次数、
// SaveExpanded 每次调用的入参快照，并可配置 SaveExpanded 返回 error。
type fakeExpandStore struct {
	loaded    map[string]struct{}
	loadCalls int
	saveCalls [][]string
	saveErr   error
}

func (f *fakeExpandStore) LoadExpanded() map[string]struct{} {
	f.loadCalls++
	if f.loaded == nil {
		return map[string]struct{}{}
	}
	return f.loaded
}

func (f *fakeExpandStore) SaveExpanded(names []string) error {
	cp := append([]string(nil), names...)
	f.saveCalls = append(f.saveCalls, cp)
	return f.saveErr
}

type fakeKiller struct {
	calls []string
	err   error
}

func (f *fakeKiller) Kill(name string) error {
	f.calls = append(f.calls, name)
	return f.err
}

type fakeDismisser struct {
	calls []string
	err   error
}

func (f *fakeDismisser) Dismiss(name string) error {
	f.calls = append(f.calls, name)
	return f.err
}

// attnFlagDecorator 让指定 session 名的行携带触发中的 Attention 徽章，
// 用于测试「ATTN 置顶排序只作用于 session 层」这条布局锚点。
type attnFlagDecorator struct{ flagged map[string]bool }

func (d attnFlagDecorator) Decorate(s model.SeshSession) Decoration {
	if d.flagged[s.Name] {
		return Decoration{Attention: AttentionBadge{Triggered: true}}
	}
	return Decoration{}
}

// expandTestSessions 是本文件共用的 5 会话夹具：
//
//	s1 proj-a      tmux    → 可展开，3 个 window（用于验证分组排序 + 层级插入）
//	s2 proj-b      tmux    → 可展开，1 个 window "dev-server"（用于分隔符归一/搜索测试）
//	s3 proj-c      tmux    → tmux 来源但 0 个 window → 不可展开（tc-a25）
//	s4 cfgapp      config  → 非 tmux 来源 → 不可展开（tc-a24）
//	s5 weird:name  tmux    → 会话名含冒号的防御性边界，1 个 window（windowTarget 退化场景）
func expandTestSessions() model.SeshSessions {
	dir := model.SeshSessionMap{
		"s1": {Name: "proj-a", Src: "tmux", Path: "/h/proj-a"},
		"s2": {Name: "proj-b", Src: "tmux", Path: "/h/proj-b"},
		"s3": {Name: "proj-c", Src: "tmux", Path: "/h/proj-c"},
		"s4": {Name: "cfgapp", Src: "config", Path: "/h/cfgapp"},
		"s5": {Name: "weird:name", Src: "tmux", Path: "/h/weird"},
	}
	return model.SeshSessions{
		OrderedIndex: []string{"s1", "s2", "s3", "s4", "s5"},
		Directory:    dir,
	}
}

// expandTestWindows 刻意把 proj-a 的 window 顺序打乱（3,1,2），
// 用来验证「window 分组时按 Index 升序排序」这条行为契约真的在 Update 阶段生效，
// 而不是依赖调用方提前排好序。
func expandTestWindows() []WindowItem {
	return []WindowItem{
		{SessionName: "proj-a", Index: 3, Name: "shell", Active: false},
		{SessionName: "proj-a", Index: 1, Name: "claude", Active: true},
		{SessionName: "proj-a", Index: 2, Name: "server", Active: false},
		{SessionName: "proj-b", Index: 1, Name: "dev-server", Active: false},
		{SessionName: "weird:name", Index: 1, Name: "shell", Active: false},
	}
}

// newExpandModel 通过真实的 New()+WithExpandStore()+Update(sessionsLoadedMsg) 路径
// 构建一个已完成取数、rows 已计算好的 Model —— 不直接摸 m.windows / m.windowsBySession /
// m.rows 等私有字段赋值，这样才测得到分组、排序与可见行构建的真实逻辑。
// store 允许传 nil：模拟 WithExpandStore(nil) / 未注入的纯内存展开状态。
func newExpandModel(t *testing.T, store ExpandStore, dis Dismisser, kil Killer) Model {
	t.Helper()
	sessions := expandTestSessions()
	windows := expandTestWindows()
	fetch := func(string) (FetchResult, error) {
		return FetchResult{Sessions: sessions, Decorator: NoDecoration{}, Windows: windows}, nil
	}
	m := New(fetch, NoDecoration{}, dis, kil, false, false, "> ", "Filter sessions...")
	m = m.WithExpandStore(store)
	result, _ := m.Update(sessionsLoadedMsg{sessions: sessions, decorator: NoDecoration{}, windows: windows})
	m2 := result.(Model)
	m2.height = 30
	return m2
}

func testFilteredSession(name, src string) filteredItem {
	return filteredItem{item: sessionItem{
		session: model.SeshSession{Name: name, Src: src},
		name:    name,
		src:     src,
	}}
}

// ---------- Section A: buildVisibleRows（纯函数，直接单测） ----------

func TestBuildVisibleRows_AllCollapsedReturnsOnlySessionRows(t *testing.T) {
	// tc-a1：3 会话均不在 expanded/temp 里 → 3 行，全部 session 行，顺序与 filtered 一致，
	// 即使 proj-a 在 windowsBySession 里有数据，没被展开就不该出现 window 行。
	filtered := []filteredItem{
		testFilteredSession("proj-a", "tmux"),
		testFilteredSession("proj-b", "tmux"),
		testFilteredSession("proj-c", "tmux"),
	}
	windowsBySession := map[string][]WindowItem{
		"proj-a": {{SessionName: "proj-a", Index: 1}},
	}

	rows := buildVisibleRows(filtered, map[string]struct{}{}, map[string]struct{}{}, windowsBySession)

	require.Len(t, rows, 3)
	for i, r := range rows {
		assert.Equal(t, rowSession, r.kind, "row %d 应为 session 行", i)
		assert.Equal(t, i, r.sessionIdx)
	}
}

func TestBuildVisibleRows_ManualExpandInsertsWindowRowsInOrder(t *testing.T) {
	// tc-a2 + tc-a4：会话 2（proj-b）手动展开，其 window 表含 3 个 window（1 号活动）。
	// 期望 6 行：proj-a、proj-b、proj-b 的 window1/2/3、proj-c，且 window 行携带正确的
	// 所属会话下标、序号与活动标记。
	filtered := []filteredItem{
		testFilteredSession("proj-a", "tmux"),
		testFilteredSession("proj-b", "tmux"),
		testFilteredSession("proj-c", "tmux"),
	}
	expanded := map[string]struct{}{"proj-b": {}}
	windowsBySession := map[string][]WindowItem{
		"proj-b": {
			{SessionName: "proj-b", Index: 1, Name: "claude", Active: true},
			{SessionName: "proj-b", Index: 2, Name: "server", Active: false},
			{SessionName: "proj-b", Index: 3, Name: "shell", Active: false},
		},
	}

	rows := buildVisibleRows(filtered, expanded, map[string]struct{}{}, windowsBySession)

	require.Len(t, rows, 6, "3 会话 + proj-b 的 3 个 window")
	wantKinds := []rowKind{rowSession, rowSession, rowWindow, rowWindow, rowWindow, rowSession}
	for i, want := range wantKinds {
		assert.Equal(t, want, rows[i].kind, "row %d kind", i)
	}
	assert.Equal(t, 1, rows[1].sessionIdx) // proj-b 的 session 行
	assert.Equal(t, 1, rows[2].sessionIdx) // window 行也指回 proj-b
	assert.Equal(t, 1, rows[2].window.Index)
	assert.True(t, rows[2].window.Active, "window 1 是活动 window")
	assert.Equal(t, 2, rows[3].window.Index)
	assert.False(t, rows[3].window.Active)
	assert.Equal(t, 3, rows[4].window.Index)
	assert.Nil(t, rows[2].matchedIndexes, "手动展开的 window 行不应带高亮下标")
	assert.Equal(t, 2, rows[5].sessionIdx) // proj-c
}

func TestBuildVisibleRows_ExpandedSessionWithNoWindowsProducesNoWindowRows(t *testing.T) {
	// tc-a3：会话在展开集合里，但 window 表为空（如会话已消失、展开记录残留）
	filtered := []filteredItem{testFilteredSession("ghost", "tmux")}
	expanded := map[string]struct{}{"ghost": {}}

	rows := buildVisibleRows(filtered, expanded, map[string]struct{}{}, map[string][]WindowItem{})

	require.Len(t, rows, 1)
	assert.Equal(t, rowSession, rows[0].kind)
}

func TestBuildVisibleRows_PriorityRules(t *testing.T) {
	// 优先级链条穷举（见文件头部 pairwise 说明）：
	// 该session∈temp且len(windowMatches)>0 → 只出命中行；
	// 否则该session∈expanded → 出全部；否则不出。
	fullWindows := []WindowItem{
		{SessionName: "s", Index: 1, Name: "a"},
		{SessionName: "s", Index: 2, Name: "b"},
	}
	matchedOnly := []windowMatch{{window: WindowItem{SessionName: "s", Index: 2, Name: "b"}, matchedIndexes: []int{0}}}

	cases := []struct {
		name        string
		inExpanded  bool
		inTemp      bool
		hasMatches  bool
		wantExtra   int
		wantOnlyIdx []int
	}{
		{"F,F,F 全不沾边→无window行(tc-a1)", false, false, false, 0, nil},
		{"F,F,T 有残留matches但不在temp→无window行(防御)", false, false, true, 0, nil},
		{"F,T,F 在temp但matches为空、未展开→无window行(防御)", false, true, false, 0, nil},
		{"F,T,T 仅temp命中→只出命中行(tc-a62)", false, true, true, 1, []int{2}},
		{"T,F,F 仅手动展开→全部window行(tc-a2)", true, false, false, 2, []int{1, 2}},
		{"T,F,T 手动展开+残留matches(不在temp)→仍是全部window行(防御)", true, false, true, 2, []int{1, 2}},
		{"T,T,F 手动展开+在temp但matches为空→回落到全部window行(边界)", true, true, false, 2, []int{1, 2}},
		{"T,T,T 手动展开+temp命中→temp优先，只出命中行(tc-a64)", true, true, true, 1, []int{2}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			filtered := []filteredItem{testFilteredSession("s", "tmux")}
			if tc.hasMatches {
				filtered[0].windowMatches = matchedOnly
			}
			expanded := map[string]struct{}{}
			if tc.inExpanded {
				expanded["s"] = struct{}{}
			}
			temp := map[string]struct{}{}
			if tc.inTemp {
				temp["s"] = struct{}{}
			}
			windowsBySession := map[string][]WindowItem{"s": fullWindows}

			rows := buildVisibleRows(filtered, expanded, temp, windowsBySession)

			require.Len(t, rows, 1+tc.wantExtra)
			if tc.wantOnlyIdx != nil {
				gotIdx := make([]int, 0, len(tc.wantOnlyIdx))
				for _, r := range rows[1:] {
					gotIdx = append(gotIdx, r.window.Index)
				}
				assert.Equal(t, tc.wantOnlyIdx, gotIdx)
			}
		})
	}
}

func TestBuildVisibleRows_WindowRowsSortedByIndexRegardlessOfInputOrder(t *testing.T) {
	// tc-a6（window 排序部分）：windowsBySession 内顺序打乱，buildVisibleRows 的输出
	// 必须按 Index 升序，不能原样透传输入顺序。
	filtered := []filteredItem{testFilteredSession("proj-a", "tmux")}
	expanded := map[string]struct{}{"proj-a": {}}
	windowsBySession := map[string][]WindowItem{
		"proj-a": {
			{SessionName: "proj-a", Index: 3, Name: "c"},
			{SessionName: "proj-a", Index: 1, Name: "a"},
			{SessionName: "proj-a", Index: 2, Name: "b"},
		},
	}

	rows := buildVisibleRows(filtered, expanded, map[string]struct{}{}, windowsBySession)

	require.Len(t, rows, 4)
	assert.Equal(t, 1, rows[1].window.Index)
	assert.Equal(t, 2, rows[2].window.Index)
	assert.Equal(t, 3, rows[3].window.Index)
}

func TestBuildVisibleRows_MultipleExpandedSessionsDoNotInterleave(t *testing.T) {
	// 布局锚点：每个 session 行之后紧跟它自己的 window 行，不与其他 session 的行交错。
	filtered := []filteredItem{
		testFilteredSession("proj-a", "tmux"),
		testFilteredSession("proj-b", "tmux"),
	}
	expanded := map[string]struct{}{"proj-a": {}, "proj-b": {}}
	windowsBySession := map[string][]WindowItem{
		"proj-a": {{SessionName: "proj-a", Index: 1}},
		"proj-b": {{SessionName: "proj-b", Index: 1}, {SessionName: "proj-b", Index: 2}},
	}

	rows := buildVisibleRows(filtered, expanded, map[string]struct{}{}, windowsBySession)

	require.Len(t, rows, 5)
	wantSessionIdx := []int{0, 0, 1, 1, 1}
	wantKinds := []rowKind{rowSession, rowWindow, rowSession, rowWindow, rowWindow}
	for i := range rows {
		assert.Equal(t, wantSessionIdx[i], rows[i].sessionIdx, "row %d 所属 session 下标", i)
		assert.Equal(t, wantKinds[i], rows[i].kind, "row %d kind", i)
	}
}

// ---------- Section B: isExpandable / windowTarget ----------

func TestIsExpandable_TmuxSessionsWithAtLeastOneWindow(t *testing.T) {
	m := newExpandModel(t, nil, nil, nil)
	// 全折叠态下 filtered 顺序 = 夹具原始顺序：proj-a(0) proj-b(1) proj-c(2) cfgapp(3) weird:name(4)
	assert.True(t, m.isExpandable(0), "proj-a 有 3 个 window，应可展开")
	assert.True(t, m.isExpandable(1), "proj-b 有 1 个 window（下边界=1），应可展开")
}

func TestIsExpandable_TmuxZeroWindows_False(t *testing.T) {
	// tc-a25：来源是 tmux，但 window 数为 0（边界）
	m := newExpandModel(t, nil, nil, nil)
	assert.False(t, m.isExpandable(2), "proj-c 是 tmux 来源但 window 数为 0，不可展开")
}

func TestIsExpandable_NonTmuxSource_False(t *testing.T) {
	// tc-a24：来源非 tmux（config），即使某种意外情况下有 window 数据也不可展开
	m := newExpandModel(t, nil, nil, nil)
	assert.False(t, m.isExpandable(3), "cfgapp 来源是 config，非 tmux 不可展开")
}

func TestWindowTarget_NormalSession(t *testing.T) {
	target, ok := windowTarget("proj-a", 2)
	assert.True(t, ok)
	assert.Equal(t, "proj-a:2", target)
}

func TestWindowTarget_SessionNameContainsColon_DegradesToNotOk(t *testing.T) {
	// 行为契约：会话名含 ':' 时退化为 ok=false，宁可少切一层也不拼出错误目标。
	_, ok := windowTarget("weird:name", 1)
	assert.False(t, ok)
}

// ---------- Section C: rowAt 边界 ----------

func TestRowAt_InRangeReturnsRowAndTrue(t *testing.T) {
	m := newExpandModel(t, nil, nil, nil)
	row, ok := m.rowAt(0)
	require.True(t, ok)
	assert.Equal(t, rowSession, row.kind)
}

func TestRowAt_OutOfRangeReturnsFalse(t *testing.T) {
	m := newExpandModel(t, nil, nil, nil)
	_, ok := m.rowAt(-1)
	assert.False(t, ok, "负下标越界")
	_, ok = m.rowAt(len(m.rows))
	assert.False(t, ok, "等于长度即越界（合法下标是 [0,len)）")
}

// ---------- Section D: expandCurrent / collapseCurrent + ExpandStore 覆盖锚点 ----------

func TestExpandCurrent_ExpandableSessionRow_GrowsRowsAndSaves(t *testing.T) {
	// 覆盖锚点①：→ 手动展开时 SaveExpanded 被调用且入参含该 session
	store := &fakeExpandStore{}
	m := newExpandModel(t, store, nil, nil)
	before := len(m.rows)

	m.cursor = 0 // proj-a
	m.expandCurrent()

	assert.Equal(t, before+3, len(m.rows), "proj-a 有 3 个 window，rows 应恰好增加 3（布局锚点）")
	require.Len(t, store.saveCalls, 1)
	assert.Contains(t, store.saveCalls[0], "proj-a")
}

func TestExpandCurrent_NonExpandableCases(t *testing.T) {
	// 覆盖锚点「→：不可展开的非 tmux 项 / window 数为 0 的 tmux session → rows 不变、不写盘」
	cases := []struct {
		name      string
		cursorIdx int // 全折叠态下 rows == filtered，下标即会话下标
	}{
		{"tc-a24 非tmux来源(cfgapp)", 3},
		{"tc-a25 tmux但window数为0(proj-c)", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeExpandStore{}
			m := newExpandModel(t, store, nil, nil)
			before := len(m.rows)

			m.cursor = tc.cursorIdx
			m.expandCurrent()

			assert.Equal(t, before, len(m.rows), "不可展开项按 → 不应改变 rows")
			assert.Empty(t, store.saveCalls, "不可展开项按 → 不应调用 SaveExpanded")
		})
	}
}

func TestExpandCurrent_CursorOnWindowRow_NoOp(t *testing.T) {
	// 覆盖锚点「→：光标已在 window 行 → 无反应」
	store := &fakeExpandStore{}
	m := newExpandModel(t, store, nil, nil)
	m.cursor = 0
	m.expandCurrent() // 先展开 proj-a
	require.Len(t, store.saveCalls, 1)

	before := len(m.rows)
	m.cursor = 1 // proj-a 的第一个 window 行
	m.expandCurrent()

	assert.Equal(t, before, len(m.rows), "光标已在 window 行时 → 应静默无反应")
	assert.Len(t, store.saveCalls, 1, "不应产生新的 SaveExpanded 调用")
}

func TestCollapseCurrent_ExpandedSessionRow_ShrinksRowsAndSaves(t *testing.T) {
	// tc-a21 + 覆盖锚点②：← 手动折起时 SaveExpanded 被调用且入参不含该 session
	store := &fakeExpandStore{}
	m := newExpandModel(t, store, nil, nil)
	m.cursor = 0
	m.expandCurrent()
	grown := len(m.rows)

	m.cursor = 0 // 回到 proj-a 的 session 行
	m.collapseCurrent()

	assert.Less(t, len(m.rows), grown)
	require.Len(t, store.saveCalls, 2, "展开 1 次 + 折起 1 次")
	assert.NotContains(t, store.saveCalls[1], "proj-a", "折起后 SaveExpanded 入参不应再含该 session")
}

func TestCollapseCurrent_WindowRow_ShrinksRowsAndMovesCursorToSessionRow(t *testing.T) {
	// tc-a7 + 覆盖锚点「←：window 行 → rows 变短 + 光标落到该 session 行」
	store := &fakeExpandStore{}
	m := newExpandModel(t, store, nil, nil)
	m.cursor = 0
	m.expandCurrent()

	m.cursor = 2 // proj-a 的第 2 个 window 行（Index=2）
	m.collapseCurrent()

	assert.Equal(t, 0, m.cursor, "光标应收回所属 session 行，而不是保持原数值")
	require.True(t, m.cursor < len(m.rows))
	assert.Equal(t, rowSession, m.rows[m.cursor].kind)
	require.Len(t, store.saveCalls, 2)
}

func TestCollapseCurrent_AlreadyCollapsedSessionRow_NoOp(t *testing.T) {
	// 覆盖锚点「←：已折叠 session 行 → 无反应、不写盘」
	store := &fakeExpandStore{}
	m := newExpandModel(t, store, nil, nil)
	before := len(m.rows)

	m.cursor = 0 // proj-a 本就折叠
	m.collapseCurrent()

	assert.Equal(t, before, len(m.rows))
	assert.Empty(t, store.saveCalls, "本来就折叠 → 无反应、不写盘")
}

func TestCollapseCurrent_TempExpandedWindowRow_ShrinksButNeverSaves(t *testing.T) {
	// tc-a20 + 覆盖锚点③：搜索临时展开态按 ← 一次都不调用 SaveExpanded
	store := &fakeExpandStore{}
	m := newExpandModel(t, store, nil, nil)
	m.filterInput.SetValue("dev-server") // 命中 proj-b 唯一的 window，触发临时展开
	m.applyFilter()

	windowRowIdx := -1
	for i, r := range m.rows {
		if r.kind == rowWindow && r.window.SessionName == "proj-b" {
			windowRowIdx = i
			break
		}
	}
	require.NotEqual(t, -1, windowRowIdx, "搜索应命中 proj-b 的 window 并临时展开")
	before := len(m.rows)

	m.cursor = windowRowIdx
	m.collapseCurrent()

	assert.Less(t, len(m.rows), before, "临时展开的 window 行折起后应消失")
	assert.Empty(t, store.saveCalls, "搜索临时展开 NEVER 写盘")
}

func TestExpandCurrent_SaveExpandedErrorDoesNotBlockOrPanic(t *testing.T) {
	// 覆盖锚点⑤：SaveExpanded 返回 error 时 picker 照常渲染、不退出、不 panic
	store := &fakeExpandStore{saveErr: errors.New("disk full")}
	m := newExpandModel(t, store, nil, nil)
	before := len(m.rows)

	require.NotPanics(t, func() {
		m.cursor = 0
		m.expandCurrent()
	})

	assert.Greater(t, len(m.rows), before, "SaveExpanded 报错不应阻止展开本身生效")
	v := m.View()
	assert.NotZero(t, v, "SaveExpanded 报错不应导致渲染崩溃")
}

func TestWithExpandStore_LoadExpandedSeedsInitialExpandedSet(t *testing.T) {
	// 覆盖锚点「LoadExpanded：WithExpandStore 时调用一次，返回集合成为初始 expanded」
	store := &fakeExpandStore{loaded: map[string]struct{}{"proj-b": {}}}
	m := newExpandModel(t, store, nil, nil)

	found := false
	for _, r := range m.rows {
		if r.kind == rowWindow && r.window.SessionName == "proj-b" {
			found = true
		}
	}
	assert.True(t, found, "LoadExpanded 返回的集合应成为初始 expanded，proj-b 应已展开而无需手动按 →")
	assert.Equal(t, 1, store.loadCalls, "WithExpandStore 应恰好调用一次 LoadExpanded")
}

func TestWithExpandStore_LoadExpandedEmptyMap_StartsFullyCollapsed(t *testing.T) {
	// 覆盖锚点「返回空 map 时从全折叠开始」
	store := &fakeExpandStore{loaded: map[string]struct{}{}}
	m := newExpandModel(t, store, nil, nil)

	for _, r := range m.rows {
		assert.Equal(t, rowSession, r.kind, "空 map 起步应全部折叠，不产生任何 window 行")
	}
}

func TestWithExpandStore_NilStoreNeverWritesStaysInMemory(t *testing.T) {
	// 行为契约：WithExpandStore(nil) / 未注入 → expanded 起始为空、永不写盘
	m := newExpandModel(t, nil, nil, nil)
	before := len(m.rows)

	m.cursor = 0
	require.NotPanics(t, func() { m.expandCurrent() })
	assert.Greater(t, len(m.rows), before, "即使没有 store，展开仍应在内存中生效")

	m.cursor = 0
	require.NotPanics(t, func() { m.collapseCurrent() })
}

func TestNew_WithoutCallingWithExpandStore_StartsFullyCollapsed(t *testing.T) {
	// "未注入" 场景：完全不调用 WithExpandStore，等价于传 nil。
	sessions := expandTestSessions()
	windows := expandTestWindows()
	fetch := func(string) (FetchResult, error) {
		return FetchResult{Sessions: sessions, Decorator: NoDecoration{}, Windows: windows}, nil
	}
	m := New(fetch, NoDecoration{}, nil, nil, false, false, "> ", "Filter sessions...")
	result, _ := m.Update(sessionsLoadedMsg{sessions: sessions, decorator: NoDecoration{}, windows: windows})
	m2 := result.(Model)

	for _, r := range m2.rows {
		assert.Equal(t, rowSession, r.kind, "未注入 ExpandStore 应等价于全折叠起步")
	}
}

func TestExpandCurrent_CursorPositionUnchangedWhenStillValid(t *testing.T) {
	// tc-a10：原 3 会话光标在第 2 行（proj-b，下标 1），展开后行数暴增，
	// 光标数值应保持不变，仍指向同一个会话行，不发生跳动或被重置。
	store := &fakeExpandStore{}
	m := newExpandModel(t, store, nil, nil)
	m.cursor = 1 // proj-b

	m.expandCurrent()

	assert.Equal(t, 1, m.cursor, "展开后光标数值应保持不变")
	assert.Equal(t, rowSession, m.rows[m.cursor].kind)
	assert.Equal(t, "proj-b", m.filtered[m.rows[m.cursor].sessionIdx].item.name)
}

func TestSessionsLoaded_GroupsWindowsBySessionAndSortsByIndexAscending(t *testing.T) {
	// 行为契约「window 分组」：取数完成时按 SessionName 分组存进 windowsBySession，
	// 组内按 Index 升序排序。expandTestWindows() 里 proj-a 是乱序输入（3,1,2），
	// 这里验证 Update 本身完成了排序，而不是依赖调用方提前排好序。
	m := newExpandModel(t, nil, nil, nil)
	m.cursor = 0
	m.expandCurrent()

	require.Len(t, m.rows, 8) // 5 会话 + proj-a 的 3 个 window
	assert.Equal(t, 1, m.rows[1].window.Index)
	assert.Equal(t, 2, m.rows[2].window.Index)
	assert.Equal(t, 3, m.rows[3].window.Index)
}

// ---------- Section F: clampCursor 边界（BVA） ----------

func TestClampCursor_EmptyRowsResetsToZero(t *testing.T) {
	// BVA 下边界：rows 为空
	m := &Model{rows: nil, cursor: 5, offset: 3}
	m.clampCursor()
	assert.Equal(t, 0, m.cursor)
	assert.Equal(t, 0, m.offset)
}

func TestClampCursor_CursorBeyondLastRow_ClampsToEnd(t *testing.T) {
	// tc-a8：原 8 行光标在第 7 行，折起后剩 3 行 → 光标应收到末行（下标 2）
	m := &Model{rows: make([]visibleRow, 3), cursor: 6, offset: 0, height: 30}
	m.clampCursor()
	assert.Equal(t, 2, m.cursor)
}

func TestClampCursor_NegativeCursor_ClampsToZero(t *testing.T) {
	m := &Model{rows: make([]visibleRow, 5), cursor: -1, offset: 0, height: 30}
	m.clampCursor()
	assert.Equal(t, 0, m.cursor)
}

func TestClampCursor_OffsetRealignsWhenBeyondCursor(t *testing.T) {
	// tc-a9：原 10 行 offset=5、光标在第 9 行（下标8），折起后剩 4 行 →
	// 光标夹紧到末行（下标3），offset 必须重新对齐到不超过光标位置。
	m := &Model{rows: make([]visibleRow, 4), cursor: 8, offset: 5, height: 30}
	m.clampCursor()
	assert.Equal(t, 3, m.cursor)
	assert.LessOrEqual(t, m.offset, m.cursor, "offset 不能超过 cursor")
}

func TestClampCursor_OffsetBelowZero_ClampsToZero(t *testing.T) {
	m := &Model{rows: make([]visibleRow, 5), cursor: 2, offset: -3, height: 30}
	m.clampCursor()
	assert.GreaterOrEqual(t, m.offset, 0)
}

func TestClampCursor_CursorBeyondVisibleWindow_AdvancesOffset(t *testing.T) {
	m := &Model{rows: make([]visibleRow, 50), height: 30}
	visible := m.visibleCount()
	m.cursor = visible + 5
	m.offset = 0

	m.clampCursor()

	assert.Equal(t, m.cursor-visible+1, m.offset)
	assert.True(t, m.cursor >= m.offset && m.cursor < m.offset+visible,
		"[offset, offset+visibleCount()) 必须始终包含 cursor（布局锚点）")
}

// ---------- Section G: 搜索匹配范围与临时展开 ----------

func TestApplyFilter_WindowNameMatchTriggersTempExpandOnlyShowsMatched(t *testing.T) {
	// tc-a62：搜索词命中 proj-b 唯一的 window「dev-server」，该会话此前折叠
	m := newExpandModel(t, nil, nil, nil)
	m.filterInput.SetValue("dev-server")
	m.applyFilter()

	windowRows := 0
	for _, r := range m.rows {
		if r.kind == rowWindow {
			windowRows++
			assert.Equal(t, "proj-b", r.window.SessionName)
			assert.Equal(t, "dev-server", r.window.Name)
		}
	}
	assert.Equal(t, 1, windowRows, "只应渲染命中的那一个 window 行")
}

func TestApplyFilter_SessionNameMatchOnlyDoesNotAutoExpand(t *testing.T) {
	// tc-a63：搜索词命中会话名本身（"proj-a"），但该会话下没有名为 proj-a 的 window
	m := newExpandModel(t, nil, nil, nil)
	m.filterInput.SetValue("proj-a")
	m.applyFilter()

	for _, r := range m.rows {
		assert.NotEqual(t, rowWindow, r.kind, "会话名命中但无 window 命中，不应自动展开")
	}
}

func TestApplyFilter_SessionAndWindowBothMatchOnlyShowsMatchedWindow(t *testing.T) {
	// tc-a64：搜索词 "shell" 同时命中 proj-a 的 window（Index3）与 weird:name 的 window，
	// 两个会话的会话名本身都不含 "shell"，此处验证「命中 window 只渲染命中行」不受干扰。
	m := newExpandModel(t, nil, nil, nil)
	m.filterInput.SetValue("shell")
	m.applyFilter()

	windowRows := 0
	for _, r := range m.rows {
		if r.kind == rowWindow {
			windowRows++
			assert.Equal(t, "shell", r.window.Name)
		}
	}
	assert.Equal(t, 2, windowRows, "proj-a 和 weird:name 各有一个名为 shell 的 window 命中")
}

func TestApplyFilter_SeparatorAwareNormalizesWindowNameMatch(t *testing.T) {
	// tc-a65：分隔符归一开启时，window 名 "dev-server" 应命中搜索词 "dev server"
	sessions := expandTestSessions()
	windows := expandTestWindows()
	fetch := func(string) (FetchResult, error) {
		return FetchResult{Sessions: sessions, Decorator: NoDecoration{}, Windows: windows}, nil
	}
	m := New(fetch, NoDecoration{}, nil, nil, false, true, "> ", "Filter sessions...") // separatorAware=true
	result, _ := m.Update(sessionsLoadedMsg{sessions: sessions, decorator: NoDecoration{}, windows: windows})
	m2 := result.(Model)
	m2.height = 30

	m2.filterInput.SetValue("dev server")
	m2.applyFilter()

	found := false
	for _, r := range m2.rows {
		if r.kind == rowWindow && r.window.Name == "dev-server" {
			found = true
		}
	}
	assert.True(t, found, "分隔符归一后 'dev server' 应命中 'dev-server'")
}

func TestApplyFilter_ClearingSearchClearsTempButKeepsManualExpanded(t *testing.T) {
	// tc-a22 / tc-a66：手动展开 proj-a，搜索触发 proj-b 的临时展开，
	// 清空搜索后 temp 应清空但手动展开的 proj-a 应保留。
	store := &fakeExpandStore{}
	m := newExpandModel(t, store, nil, nil)

	m.cursor = 0
	m.expandCurrent() // 手动展开 proj-a

	m.filterInput.SetValue("dev-server") // 搜索命中 proj-b，产生临时展开
	m.applyFilter()

	// 手动展开只决定"命中过滤后还留在 filtered 里的会话要不要出 window 行"，
	// 不豁免过滤本身：proj-a 的会话名与它的 3 个 window 名（claude/server/shell）
	// 都不含 "dev-server" 的子序列，搜索期间 proj-a 整个会话都不在 filtered 里，
	// 因此它此前手动展开的 3 个 window 行也不会出现；期间只有 proj-b 靠 window 名
	// 命中被追加到 filtered 并临时展开，产生 1 个 window 行。
	windowRowsDuringSearch := 0
	for _, r := range m.rows {
		if r.kind == rowWindow {
			windowRowsDuringSearch++
		}
	}
	require.Equal(t, 1, windowRowsDuringSearch, "搜索期间 proj-a 因不匹配被过滤掉，只剩 proj-b 靠 window 名命中临时展开的 1 行")

	m.filterInput.SetValue("")
	m.applyFilter()

	gotSessions := map[string]bool{}
	for _, r := range m.rows {
		if r.kind == rowWindow {
			gotSessions[r.window.SessionName] = true
		}
	}
	assert.True(t, gotSessions["proj-a"], "清空搜索后手动展开的 proj-a 应仍展开")
	assert.False(t, gotSessions["proj-b"], "清空搜索后临时展开的 proj-b 应回到折叠")
}

func TestApplyFilter_WindowOnlyMatchAppendedAfterNameMatches(t *testing.T) {
	// 行为契约：命中 window 但会话名未命中的 session，应追加到 filtered 末尾。
	// 用刻意反字典序命名（zz-session 会话名命中在前、bravo 仅靠 window 命中在后）
	// 排除「碰巧按字典序排列」造成的假阳性。
	dir := model.SeshSessionMap{
		"s1": {Name: "zz-session", Src: "tmux"},
		"s2": {Name: "bravo", Src: "tmux"},
	}
	sessions := model.SeshSessions{OrderedIndex: []string{"s1", "s2"}, Directory: dir}
	windows := []WindowItem{{SessionName: "bravo", Index: 1, Name: "zz-win"}}
	fetch := func(string) (FetchResult, error) {
		return FetchResult{Sessions: sessions, Decorator: NoDecoration{}, Windows: windows}, nil
	}
	m := New(fetch, NoDecoration{}, nil, nil, false, false, "> ", "Filter sessions...")
	result, _ := m.Update(sessionsLoadedMsg{sessions: sessions, decorator: NoDecoration{}, windows: windows})
	m2 := result.(Model)
	m2.height = 30

	m2.filterInput.SetValue("zz")
	m2.applyFilter()

	require.Len(t, m2.filtered, 2)
	assert.Equal(t, "zz-session", m2.filtered[0].item.name, "会话名命中的项应排在前面")
	assert.Equal(t, "bravo", m2.filtered[1].item.name, "仅靠 window 命中的会话应追加到末尾")
}

func TestSwitchMode_ClearsSearchAndTempKeepsManualExpandedResetsCursor(t *testing.T) {
	// tc-a23 + 覆盖锚点「Ctrl-a/t/g/x/f：切模式后 temp 空、expanded 保留、cursor==0、offset==0」
	store := &fakeExpandStore{}
	m := newExpandModel(t, store, nil, nil)

	m.cursor = 0
	m.expandCurrent() // 手动展开 proj-a

	m.filterInput.SetValue("dev-server") // 触发 proj-b 的临时展开
	m.applyFilter()
	m.cursor = 5
	m.offset = 2

	result, cmd := m.Update(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl})
	m2 := result.(Model)

	assert.NotNil(t, cmd, "切模式应触发新的取数 Cmd")
	assert.Equal(t, 0, m2.cursor)
	assert.Equal(t, 0, m2.offset)
	assert.Equal(t, "", m2.filterInput.Value(), "切模式应清空搜索框")

	// 完成新一轮取数（沿用同一批 sessions/windows，只是模式换了）后，
	// 手动展开的 proj-a 应仍然展开；临时展开的 proj-b 不应残留。
	sessions := expandTestSessions()
	windows := expandTestWindows()
	result2, _ := m2.Update(sessionsLoadedMsg{sessions: sessions, decorator: NoDecoration{}, windows: windows})
	m3 := result2.(Model)

	gotSessions := map[string]bool{}
	for _, r := range m3.rows {
		if r.kind == rowWindow {
			gotSessions[r.window.SessionName] = true
		}
	}
	assert.True(t, gotSessions["proj-a"], "expanded 应在切模式后保留")
	assert.False(t, gotSessions["proj-b"], "temp 应在切模式后清空，不残留")
}

func TestApplyFilter_AttentionSessionStaysAheadOfExpandedNonAttentionSession(t *testing.T) {
	// 布局锚点：ATTN 置顶排序只作用于 session 层；展开一个非 ATTN session 后，
	// ATTN session 行仍应排在其之前。
	dir := model.SeshSessionMap{
		"s1": {Name: "normal-first", Src: "tmux"},
		"s2": {Name: "attn-session", Src: "tmux"},
	}
	sessions := model.SeshSessions{OrderedIndex: []string{"s1", "s2"}, Directory: dir}
	windows := []WindowItem{{SessionName: "normal-first", Index: 1, Name: "w1"}}
	dec := attnFlagDecorator{flagged: map[string]bool{"attn-session": true}}
	fetch := func(string) (FetchResult, error) {
		return FetchResult{Sessions: sessions, Decorator: dec, Windows: windows}, nil
	}
	m := New(fetch, dec, nil, nil, false, false, "> ", "Filter sessions...")
	result, _ := m.Update(sessionsLoadedMsg{sessions: sessions, decorator: dec, windows: windows})
	m2 := result.(Model)
	m2.height = 30

	sessionRowIdx := -1
	for i, r := range m2.rows {
		if r.kind == rowSession && m2.filtered[r.sessionIdx].item.name == "normal-first" {
			sessionRowIdx = i
		}
	}
	require.NotEqual(t, -1, sessionRowIdx)
	m2.cursor = sessionRowIdx
	m2.expandCurrent()

	attnRowIdx, normalRowIdx := -1, -1
	for i, r := range m2.rows {
		if r.kind != rowSession {
			continue
		}
		switch m2.filtered[r.sessionIdx].item.name {
		case "attn-session":
			attnRowIdx = i
		case "normal-first":
			normalRowIdx = i
		}
	}
	require.NotEqual(t, -1, attnRowIdx)
	require.NotEqual(t, -1, normalRowIdx)
	assert.Less(t, attnRowIdx, normalRowIdx, "ATTN session 行应排在非 ATTN session 行之前，即使后者已展开")
}

// ---------- Section H: Enter 按行类型分流 ----------

func TestUpdateEnter_SessionRow_ReturnsSessionName(t *testing.T) {
	// 现状回归：session 行 Enter 仍是 chosen = 会话名
	m := newExpandModel(t, nil, nil, nil)
	m.cursor = 0 // proj-a

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m2 := result.(Model)

	assert.Equal(t, "proj-a", m2.Chosen())
}

func TestUpdateEnter_WindowRow_ReturnsSessionColonIndex(t *testing.T) {
	// 覆盖锚点「Enter：window 行 → chosen == 'proj-a:2'」
	m := newExpandModel(t, nil, nil, nil)
	m.cursor = 0
	m.expandCurrent()
	m.cursor = 2 // proj-a 的 window Index=2（server）

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m2 := result.(Model)

	assert.Equal(t, "proj-a:2", m2.Chosen())
}

func TestUpdateEnter_WindowRowWithColonInSessionName_DegradesToSessionNameOnly(t *testing.T) {
	// 覆盖锚点「Enter：会话名含冒号 → 退化为会话名」
	m := newExpandModel(t, nil, nil, nil)
	m.cursor = 4 // weird:name 的 session 行（全折叠态下标 4）
	m.expandCurrent()
	m.cursor = 5 // weird:name 唯一的 window 行

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m2 := result.(Model)

	assert.Equal(t, "weird:name", m2.Chosen(), "会话名含冒号时应退化为只返回会话名")
}

func TestUpdateEnter_EmptyFilteredListWithWindowsLoaded_ReturnsEmpty(t *testing.T) {
	// 覆盖锚点「Enter：空列表 → chosen == ''（现状）」，确认加入 window 行模型后仍不回归
	m := newExpandModel(t, nil, nil, nil)
	m.filterInput.SetValue("zzz-no-match")
	m.applyFilter()

	result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m2 := result.(Model)

	assert.Equal(t, "", m2.Chosen())
}

// ---------- Section I: Ctrl-d / Alt-d 按行类型分流 ----------

func TestUpdateCtrlD_WindowRow_KillerZeroCallsRowsUnchanged(t *testing.T) {
	// 覆盖锚点「Ctrl-d：window 行 → killer 零调用、rows 不变」
	killer := &fakeKiller{}
	m := newExpandModel(t, nil, nil, killer)
	m.cursor = 0
	m.expandCurrent()
	m.cursor = 1 // window 行
	before := len(m.rows)

	result, _ := m.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	m2 := result.(Model)

	assert.Empty(t, killer.calls, "光标在 window 行时 Ctrl-d 不应调用 killer")
	assert.Equal(t, before, len(m2.rows))
}

func TestUpdateCtrlD_SessionRow_KillsTmuxSession(t *testing.T) {
	// 覆盖锚点「Ctrl-d：session 行 → 行为与现状一致」（此前仓库里没有任何 kill 测试，这里补上基线）
	killer := &fakeKiller{}
	m := newExpandModel(t, nil, nil, killer)
	m.cursor = 0 // proj-a，tmux 来源

	result, _ := m.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	_ = result.(Model)

	require.Len(t, killer.calls, 1)
	assert.Equal(t, "proj-a", killer.calls[0])
}

func TestUpdateAltD_WindowRow_DismisserZeroCalls(t *testing.T) {
	// 覆盖锚点「Alt-d：window 行 → dismisser 零调用」
	dismisser := &fakeDismisser{}
	m := newExpandModel(t, nil, dismisser, nil)
	m.cursor = 0
	m.expandCurrent()
	m.cursor = 1

	result, _ := m.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModAlt})
	_ = result.(Model)

	assert.Empty(t, dismisser.calls, "光标在 window 行时 Alt-d 不应调用 dismisser")
}

func TestUpdateAltD_AttentionSessionRow_DismissesFlag(t *testing.T) {
	// 覆盖锚点「Alt-d：ATTN session 行 → 行为与现状一致」（此前仓库里没有任何 dismiss 测试，补基线）
	dismisser := &fakeDismisser{}
	dec := attnFlagDecorator{flagged: map[string]bool{"proj-a": true}}
	sessions := expandTestSessions()
	windows := expandTestWindows()
	fetch := func(string) (FetchResult, error) {
		return FetchResult{Sessions: sessions, Decorator: dec, Windows: windows}, nil
	}
	m := New(fetch, dec, dismisser, nil, false, false, "> ", "Filter sessions...")
	result, _ := m.Update(sessionsLoadedMsg{sessions: sessions, decorator: dec, windows: windows})
	m2 := result.(Model)
	m2.height = 30
	m2.cursor = 0 // proj-a：唯一的 ATTN 项，置顶排序后应仍在第 0 行

	result2, _ := m2.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModAlt})
	_ = result2.(Model)

	require.Len(t, dismisser.calls, 1)
	assert.Equal(t, "proj-a", dismisser.calls[0])
}

// ---------- Section J: 上/下移动穿行 window 行 ----------

func TestUpdateArrowDown_WalksThroughWindowRowsThenNextSession(t *testing.T) {
	// 覆盖锚点「↑/↓：展开后能从 session 行走进 window 行、再走到下一个 session 行」
	m := newExpandModel(t, nil, nil, nil)
	m.cursor = 0
	m.expandCurrent() // proj-a 展开，rows[1..3] 变成它的 window 行

	wantKinds := []rowKind{rowWindow, rowWindow, rowWindow, rowSession}
	for i, want := range wantKinds {
		result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m = result.(Model)
		require.Equal(t, want, m.rows[m.cursor].kind, "第 %d 次下移", i+1)
	}
}

func TestUpdateArrowDown_ClampsAtLastRow(t *testing.T) {
	// 覆盖锚点「末行下移被夹紧」
	m := newExpandModel(t, nil, nil, nil)
	last := len(m.rows) - 1

	for i := 0; i < last+10; i++ {
		result, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
		m = result.(Model)
	}

	assert.Equal(t, last, m.cursor, "下移超出末行应被夹紧，不指向不存在的行")
}

// ---------- Section K: kill session 不留孤儿 window 行 ----------

func TestUpdateCtrlD_KillingExpandedSession_RemovesItsWindowRowsWithoutOrphans(t *testing.T) {
	// tc-a14：会话此前处于展开态，kill 后其全部 window 行必须一并消失，不留孤儿
	killer := &fakeKiller{}
	m := newExpandModel(t, nil, nil, killer)
	m.cursor = 0
	m.expandCurrent() // proj-a 展开，3 个 window 行

	m.cursor = 0 // 回到 proj-a 的 session 行再 kill
	result, _ := m.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	m2 := result.(Model)

	for _, r := range m2.rows {
		if r.kind == rowWindow {
			assert.NotEqual(t, "proj-a", r.window.SessionName, "被 kill 的 session 不应留下孤儿 window 行")
		}
		if r.kind == rowSession {
			assert.NotEqual(t, "proj-a", m2.filtered[r.sessionIdx].item.name)
		}
	}
}

// ---------- Section L: 取数完成重算 rows（tc-a11） ----------

func TestSessionsLoaded_RecalculatesRowsFromLatestData(t *testing.T) {
	// tc-a11：取数完成后可见行必须基于最新数据重建，不能沿用取数前的旧结果
	dir := model.SeshSessionMap{"s1": {Name: "proj-a", Src: "tmux"}, "s2": {Name: "proj-b", Src: "tmux"}}
	sessions := model.SeshSessions{OrderedIndex: []string{"s1", "s2"}, Directory: dir}
	fetch := func(string) (FetchResult, error) {
		return FetchResult{Sessions: sessions, Decorator: NoDecoration{}}, nil
	}
	m := New(fetch, NoDecoration{}, nil, nil, false, false, "> ", "Filter sessions...")
	result, _ := m.Update(sessionsLoadedMsg{sessions: sessions, decorator: NoDecoration{}})
	m2 := result.(Model)
	require.Len(t, m2.rows, 2)

	dir3 := model.SeshSessionMap{
		"s1": {Name: "proj-a", Src: "tmux"},
		"s2": {Name: "proj-b", Src: "tmux"},
		"s3": {Name: "proj-c", Src: "tmux"},
	}
	sessions3 := model.SeshSessions{OrderedIndex: []string{"s1", "s2", "s3"}, Directory: dir3}
	result2, _ := m2.Update(sessionsLoadedMsg{sessions: sessions3, decorator: NoDecoration{}})
	m3 := result2.(Model)

	require.Len(t, m3.rows, 3, "重新取数后 rows 应基于最新数据重建，而不是沿用旧的 2 行")
}
