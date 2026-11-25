package app

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"

	"github.com/HW618/openlist-client/src/internal/access"
	"github.com/HW618/openlist-client/src/internal/browse"
	"github.com/HW618/openlist-client/src/internal/openlist"
	"github.com/HW618/openlist-client/src/internal/transfer"
	"github.com/HW618/openlist-client/src/locales"
)

func newTransferHTTPClient(responseHeaderTimeout time.Duration) *http.Client {
	var tr http.RoundTripper = http.DefaultTransport
	if base, ok := http.DefaultTransport.(*http.Transport); ok && base != nil {
		cloned := base.Clone()
		dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 15 * time.Second}
		cloned.DialContext = dialer.DialContext
		cloned.TLSHandshakeTimeout = 15 * time.Second
		cloned.ResponseHeaderTimeout = responseHeaderTimeout
		tr = cloned
	}
	return &http.Client{Transport: tr, CheckRedirect: openlist.CheckRedirect}
}

// downloadClient and uploadClient have no overall timeout: a big file takes as
// long as it takes, bounded by the task's context and FetchResumable's stall
// watchdog.
var (
	downloadClient = newTransferHTTPClient(30 * time.Second)
	uploadClient   = newTransferHTTPClient(0)
)

var (
	downloadRetryPolicy = transfer.RetryPolicy{
		MaxRetries:   5,
		InitialDelay: time.Second,
		MaxDelay:     12 * time.Second,
		StallTimeout: 30 * time.Second,
	}
	uploadMaxRetries  = 2
	transferUserAgent = "openlist-client/" + appVersion
	dialogStagingBase = defaultDialogStagingBase
)

func defaultDialogStagingBase() string {
	if dir := os.Getenv("NSGO_CACHE_DIR"); dir != "" && filepath.IsAbs(dir) {
		return filepath.Join(dir, "openlist-save")
	}
	if dir, err := os.UserCacheDir(); err == nil && dir != "" {
		return filepath.Join(dir, "openlist-save")
	}
	return filepath.Join(os.TempDir(), "openlist-save")
}

func dialogStageDir(taskID string) string {
	return filepath.Join(dialogStagingBase(), transfer.SafeName(taskID))
}

func (a *App) existingPartSize(t transfer.Task) int64 {
	var best int64
	if t.LocalPath != "" {
		if st, err := os.Stat(transfer.PartPath(t.LocalPath, t.Name, t.ID)); err == nil && st.Size() > best {
			best = st.Size()
		}
	}
	if savesThroughDialog() {
		if st, err := os.Stat(transfer.PartPath(dialogStageDir(t.ID), t.Name, t.ID)); err == nil && st.Size() > best {
			best = st.Size()
		}
	}
	return best
}

func (a *App) downloadPolicy(taskID, validator string) transfer.RetryPolicy {
	p := downloadRetryPolicy
	p.WakeCh = a.transferWakeCh
	p.Validator = validator
	if taskID != "" && a.engine != nil {
		p.OnValidator = func(v string) { a.engine.SetValidator(taskID, v) }
	}
	return p
}

func (a *App) transferWakeCh() <-chan struct{} {
	a.wakeMu.Lock()
	defer a.wakeMu.Unlock()
	if a.wakeCh == nil {
		a.wakeCh = make(chan struct{})
	}
	return a.wakeCh
}

func (a *App) wakeTransfers() {
	a.wakeMu.Lock()
	ch := a.wakeCh
	if ch != nil {
		a.wakeCh = make(chan struct{})
	}
	a.wakeMu.Unlock()
	if ch != nil {
		close(ch)
	}
}

func (a *App) markResumableFail(taskID string) {
	a.resumeMu.Lock()
	if a.resumableFail == nil {
		a.resumableFail = map[string]bool{}
	}
	a.resumableFail[taskID] = true
	a.resumeMu.Unlock()
}

func (a *App) clearResumableFail(taskID string) {
	a.resumeMu.Lock()
	delete(a.resumableFail, taskID)
	a.resumeMu.Unlock()
}

func (a *App) retryResumableFailed() {
	a.resumeMu.Lock()
	if len(a.resumableFail) == 0 {
		a.resumeMu.Unlock()
		return
	}
	ids := make([]string, 0, len(a.resumableFail))
	for id := range a.resumableFail {
		ids = append(ids, id)
	}
	a.resumableFail = map[string]bool{}
	a.resumeMu.Unlock()
	for _, id := range ids {
		a.engine.Retry(id)
	}
}

// bindLifecycleAndNetwork flushes transfer progress when leaving the foreground,
// wakes retry backoff timers, and auto-resumes interrupted transfers when the
// app returns to the foreground or regains connectivity.
func (a *App) bindLifecycleAndNetwork() {
	var prevLifecycle core.Lifecycle
	a.cx.Untrack(func() { prevLifecycle = a.cx.Env().Lifecycle() })
	a.inBackground.Store(prevLifecycle != core.LifecycleActive)
	a.cx.Effect(func() {
		lc := a.cx.Env().Lifecycle()
		if lc == prevLifecycle {
			return
		}
		wasActive := prevLifecycle == core.LifecycleActive
		prevLifecycle = lc
		a.inBackground.Store(lc != core.LifecycleActive)
		a.cx.Untrack(func() {
			if lc != core.LifecycleActive && wasActive {
				a.flushTaskStore()
			}
			if lc == core.LifecycleActive {
				a.wakeTransfers()
				a.retryResumableFailed()
				a.syncTransferProgress(a.tasks.GetUntracked())
			}
		})
	})

	var wasOnline bool
	a.cx.Untrack(func() { wasOnline = a.cx.Env().Network().Online })
	a.cx.OnNetworkChange(func(net core.Network) {
		cameOnline := net.Online && !wasOnline
		wasOnline = net.Online
		if !cameOnline {
			return
		}
		a.wakeTransfers()
		if !a.inBackground.Load() {
			a.retryResumableFailed()
		}
	})
}

func (a *App) flushTaskStore() {
	snap := a.engine.Snapshot()
	var activeDone int64
	for _, t := range snap {
		if t.Status == transfer.Active {
			activeDone += t.Done
		}
	}
	a.lastSavedDone = activeDone
	a.lastTaskSave = time.Now()
	_ = a.taskStore.Set(snap)
}

func absInt64(x int64) int64 {
	if x < 0 {
		return -x
	}
	return x
}

// runner is the work of a transfer task.
func (a *App) runner(transfer.Task) transfer.RunFunc {
	return func(ctx context.Context, t transfer.Task, report func(done, total int64)) error {
		if a.readyCh != nil {
			select {
			case <-a.readyCh:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		c := a.clientByID(t.SiteID)
		if c == nil {
			return errors.New("the site of this task was removed")
		}
		switch t.Direction {
		case transfer.Upload:
			a.clearResumableFail(t.ID)
			policy := downloadRetryPolicy
			maxTries := max(uploadMaxRetries, 0)
			var lastErr error
			for attempt := 0; attempt <= maxTries; attempt++ {
				if err := ctx.Err(); err != nil {
					return err
				}
				r, size, err := a.openLocal(t.LocalPath)
				if err != nil {
					return err
				}
				pr := &transfer.ProgressReader{R: r, Report: func(done int64) { report(done, size) }}
				report(0, size)
				// Uploads can exceed apiHTTP's 30s timeout; bound them by task ctx instead.
				uc := *c
				uc.HTTP = uploadClient
				err = uc.Put(ctx, t.RemotePath, pr, size)
				_ = r.Close()
				if err == nil {
					return nil
				}
				lastErr = err
				var apiErr *openlist.APIError
				if errors.As(err, &apiErr) && apiErr.Code < 500 && apiErr.Code != 408 && apiErr.Code != 429 {
					return err
				}
				if ctx.Err() != nil || !transfer.IsRetryableError(err) || attempt == maxTries {
					break
				}
				delay := policy.InitialDelay
				for i := 0; i < attempt; i++ {
					if delay >= policy.MaxDelay/2 {
						delay = policy.MaxDelay
						break
					}
					delay *= 2
				}
				if delay > policy.MaxDelay {
					delay = policy.MaxDelay
				}
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return ctx.Err()
				case <-a.transferWakeCh():
					timer.Stop()
				case <-timer.C:
				}
			}
			if ctx.Err() == nil && transfer.IsRetryableError(lastErr) {
				a.markResumableFail(t.ID)
			}
			return lastErr
		default:
			return a.runDownload(ctx, c, t, report)
		}
	}
}

func (a *App) runDownload(ctx context.Context, c *openlist.Client, t transfer.Task, report func(done, total int64)) error {
	a.clearResumableFail(t.ID)
	resolve := func(rctx context.Context) (transfer.Target, error) {
		obj, err := c.Get(rctx, t.RemotePath)
		if err != nil {
			var apiErr *openlist.APIError
			if errors.As(err, &apiErr) && apiErr.Code < 500 && apiErr.Code != 408 && apiErr.Code != 429 {
				return transfer.Target{}, transfer.Permanent(err)
			}
			return transfer.Target{}, err
		}
		if obj.RawURL == "" {
			return transfer.Target{}, transfer.Permanent(errors.New("download: server returned no valid URL"))
		}
		header := http.Header{"User-Agent": {transferUserAgent}}
		if sameHost(c.BaseURL, obj.RawURL) && c.Token != "" {
			header.Set("Authorization", c.Token)
		}
		return transfer.Target{URL: obj.RawURL, Header: header}, nil
	}
	switch {
	case isWeb():
		target, err := resolve(ctx)
		if err != nil {
			return err
		}
		return a.saveInBrowser(ctx, t.SiteID, target.URL, target.Header, t.Name, report)
	case savesThroughDialog() && (t.LocalPath == "" || a.probeDir(t.LocalPath) != nil):
		return a.saveThroughDialog(ctx, t.ID, t.Validator, resolve, t.Name, report)
	}
	_, err := transfer.FetchResumable(ctx, downloadClient, resolve, t.LocalPath, t.Name, t.ID, a.downloadPolicy(t.ID, t.Validator), report)
	if err != nil && ctx.Err() == nil && transfer.IsRetryableError(err) {
		a.markResumableFail(t.ID)
	}
	return err
}

// removeTask stops/removes a transfer task and cleans up any unfinished .part
// file and dialog staging directory.
func (a *App) removeTask(t transfer.Task) {
	a.clearResumableFail(t.ID)
	a.dismissNotifyItem("tr:" + t.ID)
	a.engine.Remove(t.ID)
	if t.Direction == transfer.Download && t.Status != transfer.Done {
		if t.LocalPath != "" {
			_ = os.Remove(transfer.PartPath(t.LocalPath, t.Name, t.ID))
		}
		_ = os.RemoveAll(dialogStageDir(t.ID))
	}
}

// saveThroughDialog fetches a file into a deterministic staging directory (so
// interrupted downloads keep their .part file across retries and restarts) and
// hands the finished file to the system's save dialog.
func (a *App) saveThroughDialog(ctx context.Context, taskID, validator string, resolve func(context.Context) (transfer.Target, error), name string, report func(done, total int64)) error {
	dir := dialogStageDir(taskID)
	safe := transfer.SafeName(name)
	path, err := transfer.FetchResumable(ctx, downloadClient, resolve, dir, safe, taskID, a.downloadPolicy(taskID, validator), report)
	if err != nil {
		if ctx.Err() == nil && transfer.IsRetryableError(err) {
			a.markResumableFail(taskID)
		}
		return err
	}
	type answer struct {
		saved bool
		err   error
	}
	got := make(chan answer, 1)
	a.cx.Post(func() {
		a.cx.SaveFile(core.SaveOptions{Name: safe, Path: path}, func(saved bool, _ string, err error) {
			_ = os.RemoveAll(dir)
			select {
			case got <- answer{saved, err}:
			default:
			}
		})
	})
	select {
	case r := <-got:
		if r.err != nil {
			return r.err
		}
		if !r.saved {
			return errors.New(a.say(ctx, locales.SaveCancelled))
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// webMemory is the most a browser download that has no folder to go to reads
// into memory, in the page and again as the script's own copy: a file larger
// than this goes to a folder the user chose, which takes it a piece at a time.
const webMemory = 256 << 20

// say gives a message in the language the app shows, for a transfer's
// goroutine, which may not read the scope: it is read on the engine's. It is
// "" when ctx ends first.
func (a *App) say(ctx context.Context, msg func(*core.Scope) string) string {
	ch := make(chan string, 1)
	a.cx.Post(func() { ch <- msg(a.cx) })
	select {
	case s := <-ch:
		return s
	case <-ctx.Done():
		return ""
	}
}

// saveInBrowser fetches a file into the folder the user chose for the site,
// and where none is chosen hands it to the browser as a download.
func (a *App) saveInBrowser(ctx context.Context, siteID, link string, header http.Header, name string, report func(done, total int64)) error {
	name = transfer.SafeName(name) // a folder handle refuses what a file name may not hold
	if webFolderName(siteID) == "" {
		data, err := transfer.FetchBytes(ctx, downloadClient, link, header, webMemory, report)
		var big *transfer.TooLargeError
		if errors.As(err, &big) {
			return errors.New(a.say(ctx, func(cx *core.Scope) string { return locales.WebTooLarge(cx, browse.Size(big.Size)) }))
		}
		if err != nil {
			return err
		}
		return webDownloadBlob(name, "application/octet-stream", data)
	}
	f, err := webFolderCreate(ctx, siteID, name)
	if err != nil {
		return err
	}
	if err := transfer.FetchTo(ctx, downloadClient, link, header, f, report); err != nil {
		f.Abort()
		return err
	}
	return f.Close()
}

// sameHost reports whether two addresses are on one host, so a token meant for
// the server is not sent to the storage behind it, and not in the clear when
// the server is reached over https.
func sameHost(a, b string) bool {
	ua, err1 := url.Parse(a)
	ub, err2 := url.Parse(b)
	if err1 != nil || err2 != nil || ua.Host == "" || ub.Host == "" {
		return false
	}
	if strings.EqualFold(ua.Scheme, "https") && !strings.EqualFold(ub.Scheme, "https") {
		return false
	}
	return strings.EqualFold(ua.Host, ub.Host)
}

// transfersChanged hears from the engine, on any goroutine.
func (a *App) transfersChanged() { a.cx.Post(a.syncTasks) }

// syncTasks shows the engine's tasks and keeps them for the next run when
// their states changed. A finished upload reads the folder again.
func (a *App) syncTasks() {
	snap := a.engine.Snapshot()
	a.tasks.Set(snap)
	a.releaseUploads(snap)

	first := a.lastDone < 0
	var told []accessIssue
	var sig strings.Builder
	doneUploads := 0
	var activeDone int64
	for _, t := range snap {
		if t.Status == transfer.Failed && t.Err != "" {
			if key := t.ID + "|" + t.Err; !a.notified[key] {
				a.notified[key] = true
				if p := access.ClassifyText(t.Err); !first && p.Kind != access.None {
					told = append(told, accessIssue{Kind: p.Kind, Detail: p.Detail, Target: a.issueTarget(p.Kind), SiteID: t.SiteID})
				}
			}
		}
		sig.WriteString(t.ID)
		sig.WriteString(string(t.Status))
		sig.WriteString(strconv.FormatInt(t.Total, 10))
		sig.WriteByte('|')
		sig.WriteString(t.Validator)
		sig.WriteByte(';')
		if t.Status == transfer.Active {
			activeDone += t.Done
		}
		if t.Direction == transfer.Upload && t.Status == transfer.Done {
			doneUploads++
		}
	}
	now := time.Now()
	s := sig.String()
	progressSave := activeDone != a.lastSavedDone &&
		(a.lastTaskSave.IsZero() || now.Sub(a.lastTaskSave) >= 2*time.Second || absInt64(activeDone-a.lastSavedDone) >= 256<<10)
	if s != a.lastSig || progressSave {
		a.lastSig = s
		a.lastSavedDone = activeDone
		a.lastTaskSave = now
		_ = a.taskStore.Set(snap)
	}
	if a.lastDone >= 0 && doneUploads > a.lastDone {
		a.listing.Refetch()
	}
	a.lastDone = doneUploads
	a.raise(told)
	if !first {
		a.notifyTaskOutcomes(snap)
	} else {
		// Seed statuses without notifying for tasks restored from the last run.
		for _, t := range snap {
			a.taskStatus[t.ID] = t.Status
		}
		a.updateBadgeAndProgress(snap)
	}
}
