package app

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/nsgo-dev/nsgo/core"

	"github.com/HW618/openlist-client/src/internal/access"
	"github.com/HW618/openlist-client/src/internal/browse"
	"github.com/HW618/openlist-client/src/internal/openlist"
	"github.com/HW618/openlist-client/src/internal/transfer"
	"github.com/HW618/openlist-client/src/locales"
)

// downloadClient has no overall timeout: a big file takes as long as it takes,
// and the task's context stops it.
var downloadClient = &http.Client{CheckRedirect: openlist.CheckRedirect}

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
			r, size, err := a.openLocal(t.LocalPath)
			if err != nil {
				return err
			}
			defer r.Close()
			pr := &transfer.ProgressReader{R: r, Report: func(done int64) { report(done, size) }}
			report(0, size)
			return c.Put(ctx, t.RemotePath, pr, size)
		default:
			obj, err := c.Get(ctx, t.RemotePath)
			if err != nil {
				return err
			}
			if obj.RawURL == "" {
				return errors.New("download: server returned no valid URL")
			}
			header := http.Header{}
			if sameHost(c.BaseURL, obj.RawURL) && c.Token != "" {
				header.Set("Authorization", c.Token)
			}
			switch {
			case isWeb():
				return a.saveInBrowser(ctx, t.SiteID, obj.RawURL, header, t.Name, report)
			case savesThroughDialog() && (t.LocalPath == "" || a.probeDir(t.LocalPath) != nil):
				return a.saveThroughDialog(ctx, obj.RawURL, header, t.Name, report)
			}
			_, err = transfer.FetchWithPart(ctx, downloadClient, obj.RawURL, header, t.LocalPath, t.Name, t.ID, report)
			return err
		}
	}
}

// removeTask stops/removes a transfer task and cleans up any unfinished .part file.
func (a *App) removeTask(t transfer.Task) {
	a.engine.Remove(t.ID)
	if t.Direction == transfer.Download && t.Status != transfer.Done && t.LocalPath != "" {
		part := filepath.Join(t.LocalPath, transfer.SafeName(t.ID)+transfer.PartSuffix)
		_ = os.Remove(part)
	}
}

// saveThroughDialog fetches a file into a temporary file and hands it to the
// system's save dialog, for platforms that cannot write in a folder the user
// chose.
func (a *App) saveThroughDialog(ctx context.Context, link string, header http.Header, name string, report func(done, total int64)) error {
	dir, err := os.MkdirTemp("", "openlist-save")
	if err != nil {
		return err
	}
	var handedOff atomic.Bool
	defer func() {
		if !handedOff.Load() {
			_ = os.RemoveAll(dir)
		}
	}()
	safe := transfer.SafeName(name)
	path, err := transfer.Fetch(ctx, downloadClient, link, header, dir, safe, report)
	if err != nil {
		return err
	}
	type answer struct {
		saved bool
		err   error
	}
	got := make(chan answer, 1)
	handedOff.Store(true)
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
		sig.WriteByte(';')
		if t.Direction == transfer.Upload && t.Status == transfer.Done {
			doneUploads++
		}
	}
	if s := sig.String(); s != a.lastSig {
		a.lastSig = s
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
