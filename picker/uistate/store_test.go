package uistate

// Partition table (ECP + BVA) — step: 01-window-data — picker/uistate 包
//
// DefaultPath() (string, error)
//   输入                                  等价类         类型   期望输出                                             契约来源
//   XDG_STATE_HOME=/custom/state          已设 XDG       有效   /custom/state/cc-sesh/picker-ui.json                行为契约「uistate.DefaultPath」①
//   XDG_STATE_HOME 空, HOME=/home/test    未设走兜底     有效   /home/test/.local/state/cc-sesh/picker-ui.json      行为契约「uistate.DefaultPath」②
//   同一组环境变量下与 attention.DefaultPath 对比  互斥路径 有效   末段=picker-ui.json 且 != attention 的路径          NEVER「不得与 attention.json 共用文件」③
//
// (*Store).LoadExpanded() map[string]struct{}
//   文件状态                              等价类       类型   期望输出         契约来源
//   version=1, expanded=[a,b,a]（含重复） 正常文件     有效   {a,b}（去重）    行为契约「LoadExpanded」①
//   路径不存在                            文件不存在   无效   空集合           覆盖锚点②
//   写入 "{not json"                      JSON 损坏    无效   空集合           覆盖锚点③
//   version=99                            版本不认识   无效   空集合           覆盖锚点④（BVA：99 是"非 1"的代表值）
//
// (*Store).SaveExpanded(names []string) error
//   输入                          等价类           类型         期望输出                                   契约来源
//   ["b","a"]                     正常写入          有效         写后 LoadExpanded 读回 {a,b}               覆盖锚点①
//   目标目录不存在                目录缺失          无效边界     MkdirAll 自动建目录后写入成功              覆盖锚点②
//   []（空集合）                  空集合，下边界    有效·边界    仍要落盘，读回为空集合                     MUST「空集合也要写」/ 覆盖锚点③
//   写盘后检查目录内容             -                -           不残留 <path>.tmp                          行为契约「原子写」/ 覆盖锚点④
//   ["c","a","b","a","c"]         含重复+乱序       有效·BVA    落盘 JSON 的 expanded 字段 = [a,b,c]（排序去重） 覆盖锚点⑤
//
// pairwise: 不适用 — 每个方法只有 1 个自变量维度在变化，没有 ≥3 参数的组合场景。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Wingsdh/cc-sesh/v2/claude/attention"
)

func TestDefaultPath_UsesXDGStateHome(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/custom/state")

	got, err := DefaultPath()
	require.NoError(t, err)
	assert.Equal(t, "/custom/state/cc-sesh/picker-ui.json", got)
}

func TestDefaultPath_FallbackHomeDotLocal(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/test")

	got, err := DefaultPath()
	require.NoError(t, err)
	assert.Equal(t, "/home/test/.local/state/cc-sesh/picker-ui.json", got)
}

// TestDefaultPath_NeverSharesFileWithAttention 直接钉死 NEVER 约束：
// 同一组环境变量下，picker-ui.json 的路径必须与 attention.json 不同。
// 这里真的调用 attention 包已实现的公开 DefaultPath 作对照，而不是靠字符串猜测，
// 防止两边未来各自改了兜底逻辑却谁都没发现撞了同一个文件。
func TestDefaultPath_NeverSharesFileWithAttention(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/custom/state")

	got, err := DefaultPath()
	require.NoError(t, err)

	attentionPath, err := attention.DefaultPath()
	require.NoError(t, err)

	assert.NotEqual(t, attentionPath, got, "picker-ui.json 不得与 attention.json 共用路径")
	assert.Equal(t, "picker-ui.json", filepath.Base(got))
}

func TestLoadExpanded_NormalFileReturnsDedupedSet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "picker-ui.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"version":1,"expanded":["sessA","sessB","sessA"]}`), 0o644))

	s := New(path)
	got := s.LoadExpanded()

	require.Len(t, got, 2, "重复的 sessA 应被去重")
	_, hasA := got["sessA"]
	_, hasB := got["sessB"]
	assert.True(t, hasA)
	assert.True(t, hasB)
}

func TestLoadExpanded_MissingFileReturnsEmptySet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nonexistent", "picker-ui.json")

	s := New(path)
	assert.Empty(t, s.LoadExpanded())
}

func TestLoadExpanded_CorruptJSONReturnsEmptySet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "picker-ui.json")
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0o644))

	s := New(path)
	assert.Empty(t, s.LoadExpanded())
}

func TestLoadExpanded_UnknownVersionReturnsEmptySet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "picker-ui.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"version":99,"expanded":["x"]}`), 0o644))

	s := New(path)
	assert.Empty(t, s.LoadExpanded(), "version != 1 必须当成空集合，不能硬解析")
}

func TestSaveExpanded_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "picker-ui.json")
	s := New(path)

	require.NoError(t, s.SaveExpanded([]string{"b", "a"}))

	s2 := New(path)
	got := s2.LoadExpanded()
	require.Len(t, got, 2)
	_, hasA := got["a"]
	_, hasB := got["b"]
	assert.True(t, hasA)
	assert.True(t, hasB)
}

func TestSaveExpanded_CreatesMissingDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "sub", "picker-ui.json")
	s := New(path)

	require.NoError(t, s.SaveExpanded([]string{"x"}))

	_, err := os.Stat(filepath.Dir(path))
	require.NoError(t, err, "MkdirAll 应自动建出缺失的目录")
	_, err = os.Stat(path)
	require.NoError(t, err)
}

// TestSaveExpanded_EmptyCollectionPersistsAndLoadsEmpty 对应 MUST「空集合也要写」：
// 用户折起最后一个 session 时，必须真的把空 expanded 落盘覆盖旧内容，而不是
// 因为"空就跳过写入"而让旧的展开态残留在磁盘上。
func TestSaveExpanded_EmptyCollectionPersistsAndLoadsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "picker-ui.json")
	s := New(path)

	require.NoError(t, s.SaveExpanded([]string{"leftover"}))
	require.NoError(t, s.SaveExpanded([]string{}))

	_, err := os.Stat(path)
	require.NoError(t, err, "空集合也要落盘")

	s2 := New(path)
	assert.Empty(t, s2.LoadExpanded(), "写空集合后读回应为空，不能残留上一次的 leftover")
}

func TestSaveExpanded_NoTmpFileLeftBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "picker-ui.json")
	s := New(path)

	require.NoError(t, s.SaveExpanded([]string{"a"}))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		assert.False(t, strings.HasSuffix(e.Name(), ".tmp"),
			"落盘后目录里不该残留 .tmp 文件，实际发现 %s", e.Name())
	}
}

func TestSaveExpanded_OutputIsSortedAndDeduped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "picker-ui.json")
	s := New(path)

	require.NoError(t, s.SaveExpanded([]string{"c", "a", "b", "a", "c"}))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)

	var fileShape struct {
		Version  int      `json:"version"`
		Expanded []string `json:"expanded"`
	}
	require.NoError(t, json.Unmarshal(raw, &fileShape))

	assert.Equal(t, 1, fileShape.Version)
	assert.Equal(t, []string{"a", "b", "c"}, fileShape.Expanded, "expanded 落盘前必须排序去重，保证 diff 友好")
}
