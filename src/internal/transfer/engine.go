// Package transfer runs the app's uploads and downloads: a queue of tasks, a
// few of them running at once, each pausable, resumable and retryable.
//
// The engine knows nothing of OpenList or of files; it runs what a RunFunc
// does and keeps count of the bytes it reports.
package transfer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Direction says whether a task fetches or sends.
type Direction string

const (
	Download Direction = "download"
	Upload   Direction = "upload"
)

// Status is where a task is in its life.
type Status string

const (
	// Active tasks run, or wait for a turn to.
	Active Status = "active"
	Paused Status = "paused"
	Done   Status = "done"
	Failed Status = "failed"
)

// Task is one transfer, as shown and as kept between runs.
type Task struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Direction Direction `json:"direction"`
	SiteID    string    `json:"siteId"`
	SiteName  string    `json:"siteName"`
	// RemotePath is the file on the server.
	RemotePath string `json:"remotePath"`
	// LocalPath is where a download is saved or an upload is read from.
	LocalPath string `json:"localPath,omitempty"`
	// Validator stores the HTTP validator (strong ETag or Last-Modified)
	// associated with an unfinished download so resumes send If-Range safely.
	Validator string `json:"validator,omitempty"`
	Total     int64  `json:"total"`
	Done      int64  `json:"done"`
	Status    Status `json:"status"`
	// Speed is bytes per second while the task runs.
	Speed float64 `json:"-"`
	// Err says why the task failed.
	Err string `json:"err,omitempty"`
}

// Percent is how much of the task is done, 0 to 100.
func (t Task) Percent() int {
	switch {
	case t.Status == Done:
		return 100
	case t.Total <= 0:
		return 0
	}
	return int(min(100, t.Done*100/t.Total))
}

// RunFunc does one task's work: it reports its progress, honors ctx, and
// returns nil when the task is whole. A download is resumed from where its
// file stands; an upload begins again.
type RunFunc func(ctx context.Context, t Task, report func(done, total int64)) error

// Engine is the queue. Its methods may be called from any goroutine.
type Engine struct {
	// Limit is how many tasks run at once; 3 when 0.
	Limit  int
	run    func(Task) RunFunc
	notify func()

	mu         sync.Mutex
	entries    []*entry
	seq        int
	running    int
	lastTel    time.Time
	pendingTel bool
}

type entry struct {
	task    Task
	seq     int
	cancel  context.CancelFunc
	running bool
	pausing bool
	removed bool
	sampled time.Time
	sample  int64
}

// New makes an engine that gets each task's work from run and calls notify
// after its tasks change, at most every 150 ms while bytes move. initial are
// the tasks of an earlier run: the ones that were running wait, paused.
func New(run func(Task) RunFunc, notify func(), initial []Task) *Engine {
	e := &Engine{run: run, notify: notify}
	for _, t := range initial {
		if t.Status == Active {
			t.Status = Paused
		}
		e.seq++
		e.entries = append(e.entries, &entry{task: t, seq: e.seq})
	}
	return e
}

func (e *Engine) limit() int {
	if e.Limit > 0 {
		return e.Limit
	}
	return 3
}

// Snapshot is the tasks, newest first.
func (e *Engine) Snapshot() []Task {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Task, 0, len(e.entries))
	for i := len(e.entries) - 1; i >= 0; i-- {
		out = append(out, e.entries[i].task)
	}
	return out
}

// Add queues t, which starts as soon as there is room, and returns its ID.
func (e *Engine) Add(t Task) string {
	e.mu.Lock()
	e.seq++
	if t.ID == "" {
		t.ID = fmt.Sprintf("tr-%d-%d", time.Now().UnixMilli(), e.seq)
	}
	t.Status, t.Err, t.Speed = Active, "", 0
	e.entries = append(e.entries, &entry{task: t, seq: e.seq})
	e.startLocked()
	e.mu.Unlock()
	e.changed(true)
	return t.ID
}

func (e *Engine) find(id string) *entry {
	for _, en := range e.entries {
		if en.task.ID == id {
			return en
		}
	}
	return nil
}

// Pause stops a task, keeping what it did.
func (e *Engine) Pause(id string) {
	e.mu.Lock()
	if en := e.find(id); en != nil && en.task.Status == Active {
		if en.running {
			en.pausing = true
			en.cancel()
		} else {
			en.task.Status = Paused
		}
	}
	e.mu.Unlock()
	e.changed(true)
}

// Resume queues a paused task again.
func (e *Engine) Resume(id string) { e.requeue(id, Paused) }

// Retry queues a failed task again.
func (e *Engine) Retry(id string) { e.requeue(id, Failed) }

// SetValidator records the HTTP validator (strong ETag or Last-Modified)
// associated with task id's partial download.
func (e *Engine) SetValidator(id, validator string) {
	e.mu.Lock()
	changed := false
	if en := e.find(id); en != nil && en.task.Validator != validator {
		en.task.Validator = validator
		changed = true
	}
	e.mu.Unlock()
	if changed {
		e.changed(false)
	}
}

func (e *Engine) requeue(id string, from Status) {
	e.mu.Lock()
	if en := e.find(id); en != nil && en.task.Status == from && !en.running {
		en.task.Status, en.task.Err, en.task.Speed = Active, "", 0
		if en.task.Direction == Upload {
			en.task.Done = 0
		}
		e.startLocked()
	}
	e.mu.Unlock()
	e.changed(true)
}

// Cancel stops a task and forgets it.
func (e *Engine) Cancel(id string) { e.Remove(id) }

// Remove forgets a task, stopping it when it runs.
func (e *Engine) Remove(id string) {
	e.mu.Lock()
	for i, en := range e.entries {
		if en.task.ID != id {
			continue
		}
		en.removed = true
		if en.running {
			en.cancel()
		}
		e.entries = append(e.entries[:i], e.entries[i+1:]...)
		break
	}
	e.startLocked()
	e.mu.Unlock()
	e.changed(true)
}

// ClearDone forgets the finished tasks and says how many there were.
func (e *Engine) ClearDone() int {
	e.mu.Lock()
	kept := e.entries[:0]
	n := 0
	for _, en := range e.entries {
		if en.task.Status == Done {
			n++
			continue
		}
		kept = append(kept, en)
	}
	clear(e.entries[len(kept):])
	e.entries = kept
	e.mu.Unlock()
	e.changed(true)
	return n
}

// Unfinished is how many tasks are active or paused.
func (e *Engine) Unfinished() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, en := range e.entries {
		if en.task.Status == Active || en.task.Status == Paused {
			n++
		}
	}
	return n
}

// startLocked starts waiting tasks while there is room, oldest first.
func (e *Engine) startLocked() {
	for e.running < e.limit() {
		var next *entry
		for _, en := range e.entries {
			if en.task.Status == Active && !en.running && (next == nil || en.seq < next.seq) {
				next = en
			}
		}
		if next == nil {
			return
		}
		e.running++
		next.running, next.pausing = true, false
		ctx, cancel := context.WithCancel(context.Background())
		next.cancel = cancel
		next.sampled, next.sample = time.Now(), next.task.Done
		task, run := next.task, e.run(next.task)
		go e.work(ctx, next, task, run)
	}
}

func (e *Engine) work(ctx context.Context, en *entry, t Task, run RunFunc) {
	var err error
	if run == nil {
		err = errors.New("transfer: nothing can run this task")
	} else {
		err = run(ctx, t, func(done, total int64) { e.report(en, done, total) })
	}
	e.mu.Lock()
	en.cancel()
	en.running = false
	e.running--
	if !en.removed {
		switch {
		case err == nil:
			en.task.Status, en.task.Speed, en.task.Validator = Done, 0, ""
			if en.task.Total > 0 {
				en.task.Done = en.task.Total
			}
		case en.pausing:
			en.task.Status = Paused
		default:
			en.task.Status, en.task.Speed, en.task.Err = Failed, 0, err.Error()
		}
		if en.task.Status != Active {
			en.task.Speed = 0
		}
	}
	e.startLocked()
	e.mu.Unlock()
	e.changed(true)
}

func (e *Engine) report(en *entry, done, total int64) {
	e.mu.Lock()
	if en.removed {
		e.mu.Unlock()
		return
	}
	en.task.Done = done
	if total > 0 {
		en.task.Total = total
	}
	now := time.Now()
	if done < en.sample {
		en.sampled, en.sample, en.task.Speed = now, done, 0
	} else if dt := now.Sub(en.sampled); dt >= 300*time.Millisecond {
		inst := float64(done-en.sample) / dt.Seconds()
		if en.task.Speed == 0 {
			en.task.Speed = inst
		} else {
			en.task.Speed = 0.6*en.task.Speed + 0.4*inst
		}
		en.sampled, en.sample = now, done
	}
	e.mu.Unlock()
	e.changed(false)
}

// changed tells the app, holding back the chatter of progress reports.
func (e *Engine) changed(force bool) {
	if e.notify == nil {
		return
	}
	e.mu.Lock()
	now := time.Now()
	dt := now.Sub(e.lastTel)
	if !force && dt < 150*time.Millisecond {
		if !e.pendingTel {
			e.pendingTel = true
			e.mu.Unlock()
			time.AfterFunc(150*time.Millisecond-dt, func() {
				e.mu.Lock()
				if !e.pendingTel {
					e.mu.Unlock()
					return
				}
				e.pendingTel = false
				e.lastTel = time.Now()
				e.mu.Unlock()
				e.notify()
			})
			return
		}
		e.mu.Unlock()
		return
	}
	e.pendingTel = false
	e.lastTel = now
	e.mu.Unlock()
	e.notify()
}
