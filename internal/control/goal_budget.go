package control

import (
	"strings"
)

// taskFaultSignals are CN/EN failure phrases used by task recognition.
var taskFaultSignals = []string{
	"not working", "isn't working", "doesn't work", "dont work", "don't work",
	"broken", "error", "bug", "issue", "failed", "failing", "crash", "exception", "panic", "segfault",
	"问题", "不工作", "报错", "错误", "失败", "崩溃", "异常",
	"卡住", "卡住了", "没反应", "不生效", "异常退出", "历史 bug", "历史bug",
}

func taskInputHasFaultSignal(input string) bool {
	normalized := strings.ToLower(strings.TrimSpace(input))
	for _, phrase := range taskFaultSignals {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}
	return false
}
