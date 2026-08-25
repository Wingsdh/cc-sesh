# cc-sesh picker 人工验证清单（留白 / 折叠展开 / 预览分栏）

> 本清单覆盖 **单元测试无法替代、必须真人在终端里跑** 的验收项。
> 由 `runtests-a2` 事务产出——该事务的执行环境没有 tmux 权限，以下每一条都**未曾执行**。
> 清单按「操作 → 预期 → 判定」三段写，照着跑即可，不需要了解实现。

---

## 0. 开跑之前（必读）

### 0.1 先确认实现在不在

⚠️ **截至本清单产出时，被验证的实现不在 `bay-fsd/job-1` 分支上。**
开发的 10 个 commit（`60b7f53` → `e236298`）被一次 `git reset` 退回到了 `a2a12db`，
工作树里 `picker/tui.go` 不含任何展开 / 预览代码。跑本清单前先确认：

```bash
cd /Users/promelo/Workspace/cc-sesh
git log --oneline -1
grep -ci expand picker/tui.go     # 必须 > 0；等于 0 说明实现不在，先恢复 e236298
```

`grep` 结果为 0 就**别往下跑**——你验的是一个没有该功能的旧版本，每一条都会「不过」，
但那不是缺陷，是代码没恢复。

### 0.2 构建验证用二进制

**不要动 `/opt/homebrew/bin/cc-sesh`**（brew 装的 2.1.0，须保持原样）。

```bash
cd /Users/promelo/Workspace/cc-sesh
go build -o /tmp/cc-sesh-verify ./
/tmp/cc-sesh-verify --version        # 应显示 dev，不是 2.1.0
```

后文所有 `picker` 均指 `/tmp/cc-sesh-verify picker`。

### 0.3 判定基线速查表

| 项 | 基线值 |
| --- | --- |
| 徽章区左内缩（ATTN 首字符左侧空格） | ≥ 2 格 |
| 徽章区右间隔（WAIT 末字符到会话名首字符） | ≥ 3 格 |
| 列表区宽度上限 | 60 列 |
| 列表区与预览区分隔 | 2 列 |
| 预览区宽度下限 | 40 列 |
| 预览显示阈值（终端总宽） | ≥ 102 列 |
| 取快照防抖 | 光标停留 150ms |
| window 行回车返回值 | `会话名:window序号` |

### 0.4 搭 fixture

```bash
tmux new-session -d -s demo-a -n claude
tmux new-window  -t demo-a -n server  'while true; do date; sleep 1; done'
tmux new-window  -t demo-a -n shell
tmux select-window -t demo-a:1
# demo-a:1 里跑一个 Claude Code 实例（进入该 window 手动敲 claude 并让它跑一轮）

tmux new-session -d -s demo-b -n claude     # 里面也跑一个 claude，跑完置 idle
tmux new-session -d -s demo-c -n shell1     # 不跑 claude
tmux new-window  -t demo-c -n shell2
tmux new-session -d -s plain-x
tmux new-session -d -s plain-y
```

交叉核对用（只读，不改状态）：

```bash
/tmp/cc-sesh-verify window --session demo-a --json    # 看真实 window 序号 / 名称 / 活动标记
/tmp/cc-sesh-verify preview demo-a:2                  # 单独抓一次某 window 的画面
```

### 0.5 跑完清理

```bash
tmux kill-session -t demo-a; tmux kill-session -t demo-b; tmux kill-session -t demo-c
tmux kill-session -t plain-x; tmux kill-session -t plain-y
rm -f /tmp/cc-sesh-verify
```

⚠️ 只 kill 你自己建的这 5 个。机器上其它 session（如 `sesh-harness`）**不要碰**。

---

## 1. 留白（对应 tc-b1 / b2 / b3 / b4 / b5 / b45 / b46）

### 1.1 有 Claude · 宽终端

**操作**
1. 终端拉到 ≥ 120 列，运行 `picker`，确认停在 all 模式（左上角模式标识）
2. 用等宽字体，逐格数光标 / 来源图标列末尾 → `ATTN` 首字符之间的空格数
3. 数 `WAIT` 列数字末尾 → 会话名首字符之间的空格数
4. 对照列头 `ATTN IDLE RUN WAIT` 与下方至少 3 行数据行的四列数字位置
5. 看 `needs you` 分组线上方那条表格横线的左右两端

**预期**
- ATTN 左侧 ≥ 2 格空白，徽章区不贴着图标
- WAIT 右侧 ≥ 3 格空白，明显宽于 ATTN 左侧
- 每列数字都落在对应列头文字正下方
- 横线左端与徽章区左内缩起点对齐，右端覆盖到名字区

**判定**：任一列数字与列头错位 → 不过；右间隔数出来 ≤ 2 格 → 不过。

### 1.2 有 Claude · 窄终端（tc-b45）

**操作**：终端调到 80 列（< 102，预览分栏隐藏），重复 1.1 的第 2~4 步。

**预期**：ATTN 左侧仍 ≥ 2 格、WAIT 右侧仍 ≥ 3 格，列头与数据行仍逐列对齐。

**判定**：留白数值随终端宽度变化 → 不过（留白与预览分栏无关）。

### 1.3 无 Claude（tc-b5 / b46）

**操作**
1. 退出 `demo-a`、`demo-b` 里的 Claude 进程，确认全机当前无任何 Claude 实例
2. 重开 `picker`，进 all 模式，终端 ≥ 102 列
3. 观察 session 行
4. 光标移到 `demo-c`（tmux 来源、2 个 window），按 `→`，再把光标移到某个 window 行停 1 秒

**预期**
- 不出现 `ATTN/IDLE/RUN/WAIT` 列头与徽章区；会话名紧跟来源图标，二者之间无额外留白
- `demo-c` 下方正常插入 2 个 window 行；预览区正常出画面

**判定**：状态表整块消失但会话名前仍留着一段空白 → 不过（留白不该在隐藏态生效）。
展开或预览因为「没有 Claude」而失效 → 不过。

---

## 2. 展开折起（tc-b6 / b7 / b8）

**操作**
1. `picker`，光标停在 `demo-a`（3 个 window，此前未展开过）
2. 按 `→`
3. 按 `←`
4. 再按 `→` 展开，按 `↓` 把光标移到第 2 个 window 行（`server`），按 `←`

**预期**
- 第 2 步：`demo-a` 下方紧跟出现 3 行 window，缩进一级，依次是序号 1/2/3 及名称，序号 1 带活动标记
- 第 3 步：3 行 window 消失，回到折叠外观
- 第 4 步：全部 window 行消失，**光标落在 `demo-a` 的 session 行上**

**判定**：第 4 步后光标停在别的会话行（保持原数值没回收）→ 不过。
window 行序号 / 名称与 `cc-sesh window --session demo-a --json` 对不上 → 不过。

---

## 3. 光标不越界（tc-b9 / b10 / b11）

**操作**
1. 终端高度调小到列表区只能容纳 5~6 行
2. `demo-a` 折叠态，光标停在其上，按 `→` 展开
3. 连按 `↓` 一路走到列表末尾，再连按 `↑` 回到顶部
4. 把光标停在 `demo-a` 最后一个 window 行（第 3 个），按 `←`
5. 继续上下移动几次

**预期**
- 第 3 步：光标能穿过全部新增 window 行直到末尾，滚动窗口跟随，不出现「翻不动」
- 第 4 步：光标回收到 `demo-a` 的 session 行，无花屏、光标不消失
- 第 5 步：上下移动表现正常

**判定**：出现光标停在空行 / 列表末尾之外、界面花屏、按键无响应 → 不过。

---

## 4. 预览出图 + 颜色保留（tc-b12 / b47 / b14 / b48）

**操作**
1. 终端 ≥ 102 列，`picker`，`demo-a` 展开
2. 光标停在 1 号 window（跑 Claude、画面有彩色输出）行上，静置 1 秒
3. 另开一个终端执行 `/tmp/cc-sesh-verify preview demo-a:1`，与预览区画面对照
4. 光标移到 2 号 window（`server`，持续输出 date）行，静置 10 秒不动，然后按 `Ctrl-r`

**预期**
- 第 2 步：停留约 150ms 后右侧出现该 window 的画面；出现之前有一行非空的「读取中」占位
- 第 3 步：预览区的**配色**与直接 `preview` 抓到的一致，不出现整行或局部错色
- 第 4 步：静置 10 秒画面**一字不变**（不自动轮询）；按 `Ctrl-r` 后刷新为最新一屏

**判定**：预览区画面是纯白 / 丢掉全部颜色 → 不过（颜色保留是明确要求）。
静置期间画面自己变了 → 不过。`Ctrl-r` 后画面没更新 → 不过。

---

## 5. 预览不串（tc-b13 / b42）

**操作**
1. 让 `demo-a:1`（Claude 对话）与 `demo-b:1`（简单 shell 提示符）画面差异明显
2. 光标停在 `demo-a` 待画面加载完成，**立即**按 `↓` 移到 `demo-b`
3. 用录屏或连续截图逐帧看这段切换过程中的预览区
4. 另外：在 `demo-a` 抓屏结果还没回来时，连续快速上下移动光标 5~6 次，最后停在 `demo-c`

**预期**
- 第 3 步：预览区先出现「读取中」占位，再变成 `demo-b` 的画面；**全程没有任何一帧**是「行名已经是 demo-b、画面还是 demo-a」
- 第 4 步：光标移动全程流畅不卡；最终只有停下来的 `demo-c` 那一个目标出画面

**判定**：抓到任意一帧 A 的画面配 B 的行名 → 不过（这是详设点名的防护点）。
快速移动时界面卡死等抓屏 → 不过。

---

## 6. 窄终端降级（tc-b18 / b19 / b20 / b21 / b50）

**操作**
1. 终端拉到 120 列开 `picker`，数出列表区字符宽度，记下列表可见行数
2. **不退出 picker**，直接把终端窗口拖窄到 80 列
3. 在窄态下光标停 `demo-a` 按 `→`
4. **不退出 picker**，把终端拖宽回 120 列以上
5. 再次记下列表可见行数，与第 1 步比

**预期**
- 第 1 步：列表区宽度为 **60 列**（不随终端变宽而变宽）
- 第 2 步：预览分栏消失，列表区占满整行，不残留破损的分栏边框
- 第 3 步：窄态下 `demo-a` 照样展开出 3 行 window
- 第 4 步：预览分栏**自动出现**并显示当前光标目标的画面，**无需重开 picker**
- 第 5 步：两次列表可见行数**相同**（预览分栏不占行高）

**判定**：拉宽后必须重开 picker 预览才回来 → 不过。
两次可见行数不同 → 不过。列表区宽度随终端变宽 → 不过。

> 阈值边界值得单独试：102 列应出现预览，101 列应隐藏。

---

## 7. window 行回车直达（tc-b22 / b23 / b49 / b51）

### 7.1 tmux 内

**操作**
1. 先 `tmux attach -t demo-c`，在 `demo-c` 内部开终端跑 `picker`
2. 展开 `demo-a`，光标移到 **2 号 window（server）**行，按回车
3. 落地后执行 `/tmp/cc-sesh-verify window`（不带参数，列当前会话的 window）

**预期**：客户端切到 `demo-a`，且当前所在 window 是 **2 号**。

**判定**：落在 1 号（会话默认 window）或别的 window → 不过。

### 7.2 tmux 外

**操作**
1. 在**不在任何 tmux 会话**的普通终端跑 `picker`
2. 展开 `demo-a`，光标移到 **3 号 window（shell）**行，按回车
3. 落地后执行 `/tmp/cc-sesh-verify window`

**预期**：终端 attach 进 `demo-a`，落地就在 **3 号 window**。

**判定**：attach 后落在 1 号 → 不过。

### 7.3 ATTN 清除（tc-b51）

**操作**：让 `demo-a` 处于 ATTN 激活（在 needs you 分组）→ 展开 → 从某个 window 行回车进入 → 退出 → 重开 `picker`。

**预期**：`demo-a` 已不在 needs you 分组，徽章计数按清除后显示。

**判定**：进了 window 但 ATTN 标记还挂着 → 不过。

---

## 8. 搜索（tc-b24 / b25 / b26 / b27 / b28）

**操作**
1. `picker`，`demo-a` 折叠（从未手动展开过），搜索框为空
2. 输入 `server`（只匹配 `demo-a` 的 2 号 window 名）
3. 清空搜索框
4. 重新输入 `server`，光标移到 `demo-a` 的 session 行按 `←`
5. 清空搜索框，再次输入 `server`
6. 另外：输入只匹配会话名本身、不匹配任何 window 名的关键词（如 `demo-a` 里 window 都不含的字串）

**预期**
- 第 2 步：`demo-a` 自动呈展开外观，但**只显示命中的那一个 window 行**（`server`），1 号和 3 号不显示
- 第 3 步：`demo-a` 回到**折叠态**（因为它从未被手动展开过）
- 第 4 步：`demo-a` 立即收起
- 第 5 步：`demo-a` **重新**触发临时展开（证明刚才的收起没写进永久记忆）
- 第 6 步：`demo-a` 保持折叠，不出现任何 window 行

**判定**：搜索命中 window 时把该会话**全部** window 都列出来 → 不过。
清空搜索后 `demo-a` 仍展开 → 不过。

---

## 9. 折叠记忆（tc-b29 / b30 / b31 / b32 / b27）

折叠记忆文件路径：`${XDG_STATE_HOME:-~/.local/state}/cc-sesh/picker-ui.json`

**操作**
1. `picker` → 手动 `→` 展开 `demo-a` → 退出 picker → 重新 `picker`
2. `cat` 一下上述 JSON 文件
3. 退出 picker，删掉该 JSON 文件，重开 `picker`
4. 让 `demo-a` 同时有「手动展开记忆」+「真实 ATTN 粘性标记」，只删该 JSON（**不动 attention.json**），重开 picker
5. 单独验搜索：`demo-a` 从未手动展开，只用搜索让它临时展开 → 退出 → 重开 picker（不输搜索词）

**预期**
- 第 1 步：重开后 `demo-a` **直接是展开态**，无需再按 `→`
- 第 2 步：内容形如 `{"version":1,"expanded":["demo-a"]}`，且这是**独立文件**，不与 attention.json 混在一起
- 第 3 步：所有 session 回到全折叠
- 第 4 步：展开记忆清空（折叠态），但 ATTN 标记**仍在**（仍在 needs you 分组、徽章计数不变）
- 第 5 步：`demo-a` 呈**折叠态**——搜索导致的临时展开**不该**被记住

**判定**：第 5 步重开后 `demo-a` 是展开的 → 不过（临时展开被错误持久化）。
第 4 步 ATTN 标记跟着没了 → 不过（两个存储没分离干净）。

---

## 10. 范围外反向验证（tc-b33 / b34 / b35 / b44）

**操作**
1. `demo-a` 展开，光标停在其某个 **window 行**，按 `Ctrl-d`
2. 同样位置按 `Alt-d`（`demo-a` 需处于 ATTN 激活态）
3. 按 `Ctrl-x` 切到 zoxide 模式，光标停在一个**没有对应 tmux 会话**的目录项，按 `→`
4. 造一个名字形如 `sample:1` 的目录项（`sample` 不是任何真实会话名），光标停上去按回车

**预期**
- 第 1 步：**没有任何会话被 kill**，window 行与 `demo-a` 都还在，界面无变化
- 第 2 步：`demo-a` 的 ATTN 标记不受影响（仍在 needs you、计数不变），界面无变化
- 第 3 步：界面无任何变化，不出现 window 行，不报错
- 第 4 步：走 zoxide 目录的默认行为（cd / 新建会话），**不会**尝试切到名为 `sample` 的会话或其 1 号 window

**判定**：第 1 步真的 kill 了会话 → 不过（**高危**，这条务必在自建 fixture 上验，别在真实会话上试）。
第 4 步跳去了一个不存在的会话 → 不过。

---

## 11. 存量不回归（tc-b36 / b37 / b38 / b39 / b40 / b41 / b43）

**操作**
1. 依次按 `Ctrl-a` `Ctrl-t` `Ctrl-g` `Ctrl-x` `Ctrl-f`，每次看列表内容
2. 光标停在某折叠态 session 行按回车
3. 光标停在 `plain-x`（一次性测试会话）行按 `Ctrl-d`
4. 光标停在 ATTN 会话的 **session 行**按 `Alt-d`
5. `demo-a` 手动展开且已写记忆，在 all 模式搜出别的会话的临时展开，然后按 `Ctrl-t`
6. `demo-a` 展开（可见 3 个 window 行），光标移回 `demo-a` 的 session 行按 `Ctrl-d`
7. picker 开着时，从另一个终端 `tmux kill-window -t demo-a:3`，再回 picker 对那条已失效的 window 行按回车

**预期**
- 第 1 步：分别切到 all / tmux / configs / zoxide / find，与改造前一致
- 第 2 步：直接进入 / attach 该会话，不涉及 window 选择
- 第 3 步：`plain-x` 被 kill，列表随即移除该行
- 第 4 步：ATTN 标记被清除，从 needs you 分组移除
- 第 5 步：搜索框清空、临时展开消失、光标回到第一行；`demo-a` 在 tmux 模式下**仍是展开态**
- 第 6 步：`demo-a` 及其 **3 个 window 行一并消失**，不残留孤儿行，滚动 / 光标正常
- 第 7 步：显示一行切换失败提示，**进程不崩溃**，列表其余部分仍可操作

**判定**：第 6 步残留任何指向 `demo-a` 的 window 行 → 不过。
第 7 步 picker 崩溃或异常退出 → 不过。

---

## 12. 文档同步（tc-b52，不需要 tmux）

**操作**：打开 `README.md` 与 `README.zh-cn.md`，查各自的 Picker hotkeys 表。

**预期**：两份文档各自都能找到 `→`、`←`、`Ctrl-r` 三行按键说明，且各有一句话说明预览分栏仅在终端宽度达到 102 列时出现。

**判定**：任一份缺任一行 → 不过。

---

## 附：本清单未覆盖的部分

以下判定已由 Go 单元测试覆盖并经**变异测试**核实（改掉生产常量后测试确实失败），
人工跑时可只做外观确认，不必逐格抠数值：

- 留白列位计算（`badgeLeftPad` / `badgeRightGap` 三处一致）
- window 行渲染格式（`└ ` 前缀、活动标记、不渲染徽章列）
- 预览状态机的序号 + 目标串双校验（防「A 的画面配 B 的行名」）
- 预览分栏尺寸分配（60 / 2 / 40，阈值 102）
- connector window 策略四条判定
- 折叠记忆的版本校验、损坏兜底、写盘时机

**但下列三条变异测试「存活」，说明单测没兜住，人工必须重点看**：

| 缺口 | 人工重点验哪条 |
| --- | --- |
| `windowIndentStep` 改 0 测试不失败（重言式断言） | §2 window 行的缩进层级是否真的比 session 行深一级 |
| 防抖时长改 500ms 测试不失败（150ms 无断言） | §4 光标停下到画面出现的延迟是否约 150ms，不是明显更久 |
| 原子写降级成直接写测试不失败 | §9 反复展开 / 折起后 `picker-ui.json` 是否始终是完整 JSON |
