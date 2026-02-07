package internal

import (
	"fmt"
	"os"
	"sync"
	"time"
)

var throttledDebugLogMu sync.Mutex
var throttledDebugLogState = make(map[string]time.Time)

// debugLog prints debug messages only when debug mode is enabled
func DebugLog(format string, v ...interface{}) {
	if DebugMode {
		fmt.Fprintf(os.Stderr, "[DEBUG] "+format, v...)
	}
}

// DebugLogPeriodic prints a debug message at most once per interval for each key.
// interval <= 0 の場合は毎回出力する。
func DebugLogPeriodic(key string, interval time.Duration, format string, v ...interface{}) {
	if !DebugMode {
		return
	}
	if interval <= 0 {
		DebugLog(format, v...)
		return
	}

	now := time.Now()

	throttledDebugLogMu.Lock()
	last, exists := throttledDebugLogState[key]
	if exists && now.Sub(last) < interval {
		throttledDebugLogMu.Unlock()
		return
	}
	throttledDebugLogState[key] = now
	throttledDebugLogMu.Unlock()

	DebugLog(format, v...)
}
