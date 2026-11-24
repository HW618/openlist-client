package transfer

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func statusOf(e *Engine, id string) Status {
	for _, t := range e.Snapshot() {
		if t.ID == id {
			return t.Status
		}
	}
	return ""
}

func TestTaskRunsToDone(t *testing.T) {
	var notified atomic.Int32
	e := New(func(Task) RunFunc {
		return func(ctx context.Context, _ Task, report func(done, total int64)) error {
			report(50, 100)
			report(100, 100)
			return nil
		}
	}, func() { notified.Add(1) }, nil)
	id := e.Add(Task{Name: "a", Direction: Download, Total: 100})
	waitFor(t, "done", func() bool { return statusOf(e, id) == Done })
	if got := e.Snapshot()[0]; got.Done != 100 || got.Percent() != 100 {
		t.Fatalf("task = %+v", got)
	}
	if notified.Load() == 0 {
		t.Fatal("never notified")
	}
}

func TestFailureThenRetry(t *testing.T) {
	var tries atomic.Int32
	e := New(func(Task) RunFunc {
		return func(context.Context, Task, func(int64, int64)) error {
			if tries.Add(1) == 1 {
				return errors.New("504 Gateway Timeout")
			}
			return nil
		}
	}, nil, nil)
	id := e.Add(Task{Name: "a"})
	waitFor(t, "failed", func() bool { return statusOf(e, id) == Failed })
	if got := e.Snapshot()[0].Err; got != "504 Gateway Timeout" {
		t.Fatalf("Err = %q", got)
	}
	e.Retry(id)
	waitFor(t, "done after retry", func() bool { return statusOf(e, id) == Done })
	if e.Snapshot()[0].Err != "" {
		t.Fatal("the error stayed after the retry")
	}
}

func TestPauseKeepsProgressAndResumeContinues(t *testing.T) {
	release := make(chan struct{})
	var starts atomic.Int32
	e := New(func(Task) RunFunc {
		return func(ctx context.Context, tk Task, report func(int64, int64)) error {
			if starts.Add(1) == 1 {
				report(40, 100)
				<-ctx.Done()
				return ctx.Err()
			}
			if tk.Done != 40 {
				return errors.New("resumed from the wrong place")
			}
			<-release
			return nil
		}
	}, nil, nil)
	id := e.Add(Task{Name: "a", Total: 100})
	waitFor(t, "progress", func() bool { return e.Snapshot()[0].Done == 40 })
	e.Pause(id)
	waitFor(t, "paused", func() bool { return statusOf(e, id) == Paused })
	if e.Unfinished() != 1 {
		t.Fatal("a paused task is unfinished")
	}
	e.Resume(id)
	waitFor(t, "second start", func() bool { return starts.Load() == 2 })
	close(release)
	waitFor(t, "done", func() bool { return statusOf(e, id) == Done })
}

func TestLimitRunsThreeAtOnce(t *testing.T) {
	var running, peak atomic.Int32
	release := make(chan struct{})
	e := New(func(Task) RunFunc {
		return func(ctx context.Context, _ Task, _ func(int64, int64)) error {
			n := running.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			defer running.Add(-1)
			select {
			case <-release:
			case <-ctx.Done():
			}
			return nil
		}
	}, nil, nil)
	for i := 0; i < 5; i++ {
		e.Add(Task{Name: "t"})
	}
	waitFor(t, "three running", func() bool { return running.Load() == 3 })
	time.Sleep(30 * time.Millisecond)
	if peak.Load() != 3 {
		t.Fatalf("peak = %d, want 3", peak.Load())
	}
	close(release)
	waitFor(t, "all done", func() bool { return e.Unfinished() == 0 })
}

func TestRemoveCancelsAndClearDone(t *testing.T) {
	e := New(func(Task) RunFunc {
		return func(ctx context.Context, tk Task, _ func(int64, int64)) error {
			if tk.Name == "hang" {
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		}
	}, nil, nil)
	hang := e.Add(Task{Name: "hang"})
	e.Add(Task{Name: "quick"})
	waitFor(t, "quick done", func() bool { return e.Snapshot()[0].Status == Done })
	if n := e.ClearDone(); n != 1 {
		t.Fatalf("ClearDone = %d", n)
	}
	e.Cancel(hang)
	if len(e.Snapshot()) != 0 {
		t.Fatalf("tasks left: %+v", e.Snapshot())
	}
}

func TestRestoredActiveTasksWaitPaused(t *testing.T) {
	e := New(func(Task) RunFunc { return nil }, nil, []Task{
		{ID: "x", Name: "a", Status: Active, Done: 5, Total: 10}, {ID: "y", Name: "b", Status: Done},
	})
	if statusOf(e, "x") != Paused || statusOf(e, "y") != Done {
		t.Fatalf("restored = %+v", e.Snapshot())
	}
}

func TestPercent(t *testing.T) {
	if (Task{Done: 1, Total: 4}).Percent() != 25 || (Task{}).Percent() != 0 || (Task{Status: Done}).Percent() != 100 {
		t.Fatal("Percent is off")
	}
}

func TestUploadRetryResetsDoneAndNeverReportsNegativeSpeed(t *testing.T) {
	var attempt atomic.Int32
	e := New(func(Task) RunFunc {
		return func(ctx context.Context, tk Task, report func(done, total int64)) error {
			if attempt.Add(1) == 1 {
				report(5000, 10000)
				return errors.New("network drop")
			}
			if tk.Done != 0 {
				return errors.New("upload retry did not reset Done to 0")
			}
			report(0, 10000)
			time.Sleep(350 * time.Millisecond)
			report(2000, 10000)
			return nil
		}
	}, nil, nil)
	id := e.Add(Task{Name: "up.bin", Direction: Upload, Total: 10000})
	waitFor(t, "failed", func() bool { return statusOf(e, id) == Failed })
	e.Retry(id)
	waitFor(t, "retried speed", func() bool {
		snap := e.Snapshot()
		return len(snap) == 1 && (snap[0].Status == Done || snap[0].Speed > 0)
	})
	snap := e.Snapshot()
	if snap[0].Speed < 0 {
		t.Fatalf("negative speed after upload retry: %v", snap[0].Speed)
	}
	waitFor(t, "done", func() bool { return statusOf(e, id) == Done })
}

func TestCompletedTaskIsNotOverwrittenByConcurrentPause(t *testing.T) {
	finishing := make(chan struct{})
	var e *Engine
	e = New(func(Task) RunFunc {
		return func(context.Context, Task, func(int64, int64)) error {
			<-finishing
			return nil
		}
	}, nil, nil)
	id := e.Add(Task{Name: "fast.bin", Total: 10})
	waitFor(t, "running", func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.running == 1
	})
	close(finishing)
	// Call Pause racing with completion: even if pausing is set right as run returns nil, Done wins.
	e.Pause(id)
	waitFor(t, "settled", func() bool {
		s := statusOf(e, id)
		return s == Done || s == Paused
	})
	// Direct verification on work state transition:
	if statusOf(e, id) != Done {
		t.Fatalf("completed task became %v instead of Done", statusOf(e, id))
	}
}
