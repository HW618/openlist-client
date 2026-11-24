package app

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nsgo-dev/nsgo/core"

	"github.com/HW618/openlist-client/src/internal/access"
	"github.com/HW618/openlist-client/src/internal/openlist"
	"github.com/HW618/openlist-client/src/internal/store"
	"github.com/HW618/openlist-client/src/internal/transfer"
	"github.com/HW618/openlist-client/src/locales"
)

const (
	// notifyCollapseLimit is the maximum number of unacknowledged notifications
	// shown individually before collapsing into a single summary alert.
	notifyCollapseLimit = 3
	notifyItemPrefix    = "notify:item:"
	notifySummaryID     = "transfer:summary"
)

type notifyEntry struct {
	title string
	ok    bool
}

// notifyGuide enables the first-launch notification permission sheet. Tests
// that do not mean to exercise it leave it false (see startWith).
var notifyGuide = true

// bindNotifications registers press handling for notification IDs.
func (a *App) bindNotifications() {
	a.cx.OnNotificationPress(a.onNotifyPress)
}

// onNotifyPress clears active notification tracking and opens Transfers.
func (a *App) onNotifyPress(id string) {
	a.clearNotificationBatch()
	a.selectTab(tabTransfers)
	_ = id
}

func (a *App) clearNotificationBatch() {
	for _, id := range a.notifyShown {
		a.cx.CloseNotification(id)
	}
	a.cx.CloseNotification(notifySummaryID)
	a.notifyShown = nil
	a.notifyCount = 0
	a.notifyOK = 0
	a.notifyFail = 0
}

// emitNotifications posts individual alerts while at most notifyCollapseLimit
// are pending, and collapses them into a single summary alert once exceeded.
func (a *App) emitNotifications(entries []notifyEntry) {
	if !a.prefs.GetUntracked().NotifyMaster() || len(entries) == 0 {
		return
	}
	for _, e := range entries {
		a.notifyCount++
		if e.ok {
			a.notifyOK++
		} else {
			a.notifyFail++
		}
	}
	if a.notifyCount <= notifyCollapseLimit {
		for _, e := range entries {
			a.notifySeq++
			id := fmt.Sprintf("%s%d", notifyItemPrefix, a.notifySeq)
			a.notifyShown = append(a.notifyShown, id)
			_ = a.cx.ShowNotification(core.Notification{
				ID: id, Title: e.title, Body: locales.NotifyOpenTransfers(a.cx),
			}, nil)
		}
		return
	}
	for _, id := range a.notifyShown {
		a.cx.CloseNotification(id)
	}
	a.notifyShown = nil
	var title string
	switch {
	case a.notifyOK > 0 && a.notifyFail > 0:
		title = locales.NotifySummaryMixed(a.cx, a.notifyCount, strconv.Itoa(a.notifyOK), strconv.Itoa(a.notifyFail))
	case a.notifyFail > 0:
		title = locales.NotifySummaryFailed(a.cx, a.notifyFail)
	default:
		title = locales.NotifySummaryDone(a.cx, a.notifyOK)
	}
	_ = a.cx.ShowNotification(core.Notification{
		ID: notifySummaryID, Title: title, Body: locales.NotifySummaryCollapsedBody(a.cx),
	}, nil)
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
			a.savePrefs(func(p *store.Prefs) {
				p.EnsureNotifyPrefs()
				n := p.Notifications
				n.Asked = true
				n.Requested = true
				p.Notifications = n
			})
			if done != nil {
				done(perm)
			}
		})
	})
}

// notifyTaskOutcomes shows notifications for tasks that newly reached Done or
// Failed, collapsing when too many accumulate, and updates badge / Dock progress.
func (a *App) notifyTaskOutcomes(snap []transfer.Task) {
	if a.taskStatus == nil {
		a.taskStatus = map[string]transfer.Status{}
	}
	var entries []notifyEntry
	for _, t := range snap {
		prev, seen := a.taskStatus[t.ID]
		a.taskStatus[t.ID] = t.Status
		// Count newly finished tasks, including ones that appear already Done
		// or Failed in the first snapshot after Add (fast transfers).
		becameDone := t.Status == transfer.Done && (!seen || prev != transfer.Done)
		becameFailed := t.Status == transfer.Failed && (!seen || prev != transfer.Failed)
		switch {
		case becameDone && t.Direction == transfer.Download:
			entries = append(entries, notifyEntry{title: locales.NotifyItemDownloadDone(a.cx, t.Name), ok: true})
		case becameDone:
			entries = append(entries, notifyEntry{title: locales.NotifyItemUploadDone(a.cx, t.Name), ok: true})
		case becameFailed:
			entries = append(entries, notifyEntry{title: locales.NotifyItemTransferFailed(a.cx, t.Name), ok: false})
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

	a.emitNotifications(entries)
	a.updateBadgeAndProgress(snap)
}

func offlineSeenKey(siteID string, t openlist.OfflineTask) string {
	kind := "d"
	if t.Transfer {
		kind = "t"
	}
	return siteID + "|" + kind + "|" + t.ID
}

// notifyOfflineOutcomes notifies for offline tasks that newly finished or failed.
func (a *App) notifyOfflineOutcomes(siteID string, list []openlist.OfflineTask, baselineReady bool) {
	if a.offlineSeen == nil {
		a.offlineSeen = map[string]int{}
	}
	var entries []notifyEntry
	alive := make(map[string]bool, len(list))
	for _, t := range list {
		key := offlineSeenKey(siteID, t)
		alive[key] = true
		prev, seen := a.offlineSeen[key]
		a.offlineSeen[key] = t.State
		if !seen {
			if !baselineReady || !t.Finished() {
				continue
			}
		} else if prev == t.State || !t.Finished() {
			continue
		}
		name := offlineTaskName(t.Name)
		if t.Failed() {
			entries = append(entries, notifyEntry{title: locales.NotifyItemOfflineFailed(a.cx, name), ok: false})
		} else if t.State == openlist.TaskSucceeded {
			entries = append(entries, notifyEntry{title: locales.NotifyItemOfflineDone(a.cx, name), ok: true})
		}
	}
	prefix := siteID + "|"
	for id := range a.offlineSeen {
		if strings.HasPrefix(id, prefix) && !alive[id] {
			delete(a.offlineSeen, id)
		}
	}
	a.emitNotifications(entries)
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
	if a.prefs.GetUntracked().NotifyMaster() && !a.prefs.GetUntracked().Notifications.Requested {
		a.requestNotifyPermission(func(perm core.NotificationPermission) {
			if perm == core.NotificationsDenied {
				a.raise([]accessIssue{{Kind: access.Notifications, Detail: "denied"}})
			}
		})
	}
}
