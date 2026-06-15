package progress

import (
	"os"
	"time"
)

// NewUpdaterForTest is like NewUpdater but uses signalWake instead of the
// global OS signal channel. This is required for synctest, where goroutines
// blocked on the global channel are not durably blocked.
func NewUpdaterForTest(interval time.Duration, report UpdateFunc, signal <-chan os.Signal) *Updater {
	return newUpdater(interval, report, signal)
}

// NewCounterForTest is like NewCounter but uses signalWake instead of the
// global OS signal channel. This is required for synctest.
func NewCounterForTest(interval time.Duration, total uint64, report Func, signal <-chan os.Signal) *Counter {
	return newCounter(interval, total, report, signal)
}
