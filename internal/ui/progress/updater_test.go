package progress_test

import (
	"os"
	"testing"
	"testing/synctest"
	"time"

	"github.com/restic/restic/internal/test"
	"github.com/restic/restic/internal/ui/progress"
)

func TestUpdater(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var (
			finalSeen = false
			ncalls    = 0
			dur       time.Duration
		)

		report := func(d time.Duration, final bool) {
			if final {
				finalSeen = true
			}
			dur = d
			ncalls++
		}
		c := progress.NewUpdaterForTest(10*time.Millisecond, report, make(chan os.Signal))
		time.Sleep(100 * time.Millisecond)
		synctest.Wait()
		c.Done()

		test.Assert(t, finalSeen, "final call did not happen")
		test.Assert(t, ncalls > 0, "no progress was reported")
		test.Assert(t, dur > 0, "duration must be positive")
	})
}

func TestUpdaterStopTwice(_ *testing.T) {
	// must not panic
	c := progress.NewUpdater(0, func(runtime time.Duration, final bool) {})
	c.Done()
	c.Done()
}

func TestUpdaterNoTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		finalSeen := false
		otherSeen := false

		report := func(d time.Duration, final bool) {
			if final {
				finalSeen = true
			} else {
				otherSeen = true
			}
		}
		c := progress.NewUpdaterForTest(0, report, make(chan os.Signal))
		c.Done()

		test.Assert(t, finalSeen, "final call did not happen")
		test.Assert(t, !otherSeen, "unexpected status update")
	})
}
