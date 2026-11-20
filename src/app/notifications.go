package app

import (
	"github.com/nsgo-dev/nsgo/core"

	"github.com/HW618/openlist-client/src/internal/access"
	"github.com/HW618/openlist-client/src/internal/openlist"
	"github.com/HW618/openlist-client/src/internal/store"
	"github.com/HW618/openlist-client/src/internal/transfer"
	"github.com/HW618/openlist-client/src/locales"
)

// Fixed notification IDs: showing again replaces the previous one (merge).
const (
	notifyTransferDone   = "transfer:done"
	notifyTransferFailed = "transfer:failed"
	notifyOfflineDone    = "offline:done"
	notifyOfflineFailed  = "offline:failed"
)

// notifyGuide enables the first-launch notification permission sheet. Tests
// that do not mean to exercise it leave it false (see startWith).
var notifyGuide = true

// bindNotifications registers press handling for notification IDs.
func (a *App) bindNotifications() {
	a.cx.OnNotificationPress(a.onNotifyPress)
}

// onNotifyPress opens Transfers for transfer:* and offline:* notification IDs.
func (a *App) onNotifyPress(id string) {
	a.selectTab(tabTransfers)
	_ = id
}

// guideNotifyPermission asks for notification permission once after install /
// first launch when the master switch is on (defaults on). Uses a permission
// sheet with an Allow button so browsers get a user gesture.
func (a *App) guideNotifyPermission() {
	p := a.prefs.GetUntracked()
	if !p.NotifyMaster() || p.Notifications.Asked {
		return
	}
	a.markNotifyAsked()
	a.raise([]accessIssue{{Kind: access.Notifications, Detail: "guide"}})
}

// markNotifyAsked records that the first-launch guide has been shown.
func (a *App) markNotifyAsked() {
	a.savePrefs(func(p *store.Prefs) {
		p.EnsureNotifyPrefs()
		n := p.Notifications
		n.Asked = true
		p.Notifications = n
	})
}

// requestNotifyPermission asks the system, then continues with done on the
// engine goroutine.
func (a *App) requestNotifyPermission(done func(core.NotificationPermission)) {
	a.cx.RequestNotificationPermission(func(perm core.NotificationPermission) {
		a.cx.Post(func() {
			a.markNotifyAsked()
			if done != nil {
				done(perm)
			}
		})
	})
}

// notifyTaskOutcomes shows merged system notifications for tasks that newly
// reached Done or Failed, and updates badge / Dock progress.
func (a *App) notifyTaskOutcomes(snap []transfer.Task) {
	prefs := a.prefs.GetUntracked()
	if a.taskStatus == nil {
		a.taskStatus = map[string]transfer.Status{}
	}
	var doneDL, doneUL, failed int
	for _, t := range snap {
		prev, seen := a.taskStatus[t.ID]
		a.taskStatus[t.ID] = t.Status
		// Count newly finished tasks, including ones that appear already Done
		// or Failed in the first snapshot after Add (fast transfers).
		becameDone := t.Status == transfer.Done && (!seen || prev != transfer.Done)
		becameFailed := t.Status == transfer.Failed && (!seen || prev != transfer.Failed)
		switch {
		case becameDone && t.Direction == transfer.Download:
			doneDL++
		case becameDone:
			doneUL++
		case becameFailed:
			failed++
		}
	}
	// Drop statuses for removed tasks.
	alive := map[string]bool{}
	for _, t := range snap {
		alive[t.ID] = true
	}
	for id := range a.taskStatus {
		if !alive[id] {
			delete(a.taskStatus, id)
		}
	}

	if prefs.NotifyMaster() {
		if prefs.NotifySuccess() && (doneDL > 0 || doneUL > 0) {
			a.showMergedTransferDone(doneDL, doneUL)
		}
		if prefs.NotifyFailure() && failed > 0 {
			a.showMergedTransferFailed(failed)
		}
	}
	a.updateBadgeAndProgress(snap)
}

// showMergedTransferDone replaces the transfer:done notification.
func (a *App) showMergedTransferDone(downloads, uploads int) {
	var title string
	switch {
	case downloads > 0 && uploads > 0:
		title = locales.NotifyTransferBoth(a.cx, downloads+uploads)
	case downloads > 0:
		title = locales.NotifyDownloadDone(a.cx, downloads)
	default:
		title = locales.NotifyUploadDone(a.cx, uploads)
	}
	_ = a.cx.ShowNotification(core.Notification{
		ID: notifyTransferDone, Title: title, Body: locales.NotifyOpenTransfers(a.cx),
	}, nil)
}

// showMergedTransferFailed replaces the transfer:failed notification.
func (a *App) showMergedTransferFailed(n int) {
	_ = a.cx.ShowNotification(core.Notification{
		ID: notifyTransferFailed, Title: locales.NotifyTransferFailed(a.cx, n), Body: locales.NotifyOpenTransfers(a.cx),
	}, nil)
}

// notifyOfflineOutcomes notifies for offline tasks that newly finished.
func (a *App) notifyOfflineOutcomes(list []openlist.OfflineTask) {
	prefs := a.prefs.GetUntracked()
	if a.offlineSeen == nil {
		a.offlineSeen = map[string]int{}
	}
	var done, failed int
	for _, t := range list {
		prev, seen := a.offlineSeen[t.ID]
		a.offlineSeen[t.ID] = t.State
		if !seen {
			// First sight of an already-finished task: do not notify (stale).
			continue
		}
		if prev == t.State || !t.Finished() {
			continue
		}
		if t.Failed() {
			failed++
		} else if t.State == openlist.TaskSucceeded {
			done++
		}
	}
	alive := map[string]bool{}
	for _, t := range list {
		alive[t.ID] = true
	}
	for id := range a.offlineSeen {
		if !alive[id] {
			delete(a.offlineSeen, id)
		}
	}
	if !prefs.NotifyMaster() {
		return
	}
	if prefs.NotifySuccess() && done > 0 {
		_ = a.cx.ShowNotification(core.Notification{
			ID: notifyOfflineDone, Title: locales.NotifyOfflineDone(a.cx, done), Body: locales.NotifyOpenTransfers(a.cx),
		}, nil)
	}
	if prefs.NotifyFailure() && failed > 0 {
		_ = a.cx.ShowNotification(core.Notification{
			ID: notifyOfflineFailed, Title: locales.NotifyOfflineFailed(a.cx, failed), Body: locales.NotifyOpenTransfers(a.cx),
		}, nil)
	}
}

// updateBadgeAndProgress sets the icon badge (active+failed) and Dock progress.
func (a *App) updateBadgeAndProgress(snap []transfer.Task) {
	active, failed := 0, 0
	var doneBytes, totalBytes int64
	indeterminate := false
	for _, t := range snap {
		switch t.Status {
		case transfer.Active:
			active++
			if t.Total > 0 {
				doneBytes += t.Done
				totalBytes += t.Total
			} else {
				indeterminate = true
			}
		case transfer.Failed:
			failed++
		}
	}
	a.cx.SetBadge(active + failed)
	switch {
	case active == 0:
		a.cx.SetTaskProgress(core.TaskProgress{})
	case indeterminate || totalBytes <= 0:
		a.cx.SetTaskProgress(core.TaskProgress{State: core.TaskProgressIndeterminate})
	default:
		a.cx.SetTaskProgress(core.TaskProgress{
			State: core.TaskProgressNormal,
			Value: float64(doneBytes) / float64(totalBytes),
		})
	}
}

// saveNotifyToggle updates one notification preference and may request permission.
func (a *App) saveNotifyToggle(change func(*store.NotifyPrefs)) {
	a.savePrefs(func(p *store.Prefs) {
		p.EnsureNotifyPrefs()
		n := p.Notifications
		change(&n)
		p.Notifications = n
	})
	if a.prefs.GetUntracked().NotifyMaster() && !a.prefs.GetUntracked().Notifications.Asked {
		a.requestNotifyPermission(func(perm core.NotificationPermission) {
			if perm == core.NotificationsDenied {
				a.raise([]accessIssue{{Kind: access.Notifications, Detail: "denied"}})
			}
		})
	}
}
