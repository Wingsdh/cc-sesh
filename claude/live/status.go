package live

// Logical 是单个 Claude 实例归类后的逻辑状态。
// 排序即严重度：值越大越严重，用于 session 聚合时取 max。
type Logical int

const (
	LogicalIdle Logical = iota
	LogicalSubagent
	LogicalBusy
	LogicalNeedsInput
)

func (l Logical) String() string {
	switch l {
	case LogicalNeedsInput:
		return "needs-input"
	case LogicalBusy:
		return "busy"
	case LogicalSubagent:
		return "subagent"
	default:
		return "idle"
	}
}

// Status 是一个 SeshSession（按 cwd 聚合后）下所有活实例的统计。
// Idle 由 Total - Busy - Subagent - Needing 推得。
type Status struct {
	Total    int
	Busy     int
	Subagent int
	Needing  int
}

func (s Status) Idle() int {
	idle := s.Total - s.Busy - s.Subagent - s.Needing
	if idle < 0 {
		return 0
	}
	return idle
}

func (s Status) IsEmpty() bool { return s.Total == 0 }

// Severity 返回该 session 的最高严重度，用于决定主徽章字符。
func (s Status) Severity() Logical {
	switch {
	case s.Needing > 0:
		return LogicalNeedsInput
	case s.Busy > 0:
		return LogicalBusy
	case s.Subagent > 0:
		return LogicalSubagent
	default:
		return LogicalIdle
	}
}

// isBackgroundKind 标识非交互式实例：后台任务（bg）与子代理（subagent）。
// 这些实例由 agent team / 后台 job 产生，生命周期独立于用户操作。
func isBackgroundKind(kind string) bool {
	return kind == "bg" || kind == "subagent"
}

// countsAsInstance 决定一个实例是否计入聚合统计。
//
// 后台 / 子代理实例只在「正在跑活」（busy/subagent/needs-input）时计入；
// 一旦转 idle（任务跑完但进程未退出，或 session 文件未清理）就不再计数。
// 否则 agent team 关闭后残留的 idle 后台进程会一直污染状态表，
// 让用户看到凭空多出的 idle 实例。
//
// 交互式实例（interactive / 旧版本缺失 kind）即使 idle 也保留——
// 那是用户自己开着的 session，理应显示。
func countsAsInstance(l Logical, kind string) bool {
	if l == LogicalIdle && isBackgroundKind(kind) {
		return false
	}
	return true
}

// classify 把 Claude 写到 json 的 raw status / kind 归到 Logical。
// 未知 status 一律当 Idle，避免穷举遗漏导致显示异常。
func classify(rawStatus, kind string) Logical {
	switch rawStatus {
	case "auth_url", "pending", "waiting":
		// waiting：Claude Code 在等用户批准（如 "approve Bash"），
		// 与 auth_url/pending 同属"等用户输入"语义。
		return LogicalNeedsInput
	case "busy", "running", "in_progress", "async_launched", "compacting":
		if kind == "subagent" {
			return LogicalSubagent
		}
		return LogicalBusy
	default:
		// idle / completed / 未知值 / 缺失
		return LogicalIdle
	}
}
