# picker-expand-preview · 进度

> 创建：2026-08-24 18:45
> 模式：plan（`ai-plans/20260824-picker-expand-preview/plan.html`，`data-status=plan_ready`，4 steps）
> skip_test: false · skip_review: false
> tidy: off（用户未要求整理；但**任务书明确要求 commit 到当前分支**，故每 step 出口按需求打行为 commit，不做 Tidy First / Tidy After）
> codex MCP: unavailable（本会话无 `mcp__codex__codex`）→ reviewer 走自审
> Makefile: absent（有 justfile；任务书指定的验证入口是 mockery → go build → go vet → go test，一律照用）
> Baseline: 通过 (18:41) — mockery v3.7.4 OK / `go build ./...` OK / `go vet ./...` OK / `go test ./...` 全绿（18 个含测试包 ok + 12 个 no test files）
> 干净起点: 是 (18:39) — `git status --porcelain` 空
> Team 工具说明：本 harness 无 `TeamCreate` / `TaskCreate`（单一隐式 team），writer / reviewer 用 `Agent` 具名 spawn + `SendMessage` 寻址；进度追踪落在本文件。

## 验证入口（任务书注入）

```
/Users/promelo/Workspace/sesh-harness/tools/mockery   # 改 interface 后必跑，v3.7.4
go build ./...
go vet ./...
go test ./...
```

NEVER 用 `~/go/bin/mockery`（v2.51.1，跑 v3 配置会 panic）。

## Step 进度

| # | Step ID | 状态 | RED | GREEN | REVIEW | 备注 |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | 01-window-data | done | 23 断言全红 | 第 1 轮通过 | review_pass self R01 | 2 条非阻塞建议判定不采纳；指标2: ok；行为 commit 4c4b123 |
| 2 | 02-visible-rows | done | 61 场景全红 | 第 1 轮通过 | review_pass self R01 | 1 条 test_bug 经订正（reviewer 复核判定正确）；reviewer 指出的覆盖缺口已补测 commit 6795973；指标2: ok；行为 commit 8607dd3 |
| 3 | 03-render-preview | done | 49 场景全红 | 第 1 轮通过（44/49 直接绿） | R01 未回（收口时仍在审） | 5 条列位 test_bug 经订正，writer 独立复核全部认同；`ansi` 转直接依赖；指标2: ok；行为 commit 5182738 |
| 4 | 04-connect-window | done | 27+2 场景全红 | 第 1 轮通过 | R01 未回（收口时仍在审） | 零 test_bug；策略链顺序补测并做变异验证；README ×2 7→10 行；指标2: ok；行为 commit e7911b1 |
