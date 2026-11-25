package app

import (
	"errors"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/mobile"
	"github.com/nsgo-dev/nsgo/widget"

	"github.com/HW618/openlist-client/src/internal/access"
	"github.com/HW618/openlist-client/src/internal/browse"
	"github.com/HW618/openlist-client/src/internal/openlist"
	"github.com/HW618/openlist-client/src/internal/store"
	"github.com/HW618/openlist-client/src/internal/transfer"
	"github.com/HW618/openlist-client/src/locales"
)

const (
	// notifyCollapseLimit is the maximum number of unacknowledged notifications
	// shown individually before collapsing into a single summary alert.
	notifyCollapseLimit  = 3
	notifyItemPrefix     = "notify:item:"
	notifyProgressID     = "transfer:progress"
	notifySummaryID      = "transfer:summary"
	transferActivityKind = "transfers"
	transferDeepLink     = "openlist://transfers"
)

type notifyEntry struct {
	key   string
	title string
	ok    bool
}

// notifyGuide enables the first-launch notification permission sheet. Tests
// that do not mean to exercise it leave it false (see startWith).
var notifyGuide = true

// isMobileNotifyRuntime reports whether mobile progress notifications or Live
// Activities run on the current target and rely on runtime notification permission.
var isMobileNotifyRuntime = func() bool {
	return runtime.GOOS == "android" || runtime.GOOS == "ios"
}

// isAndroidProgressNotify reports whether transfer progress uses a single
// silent app notification via cx.ShowNotification instead of LiveUpdates.
var isAndroidProgressNotify = func() bool {
	return runtime.GOOS == "android"
}

var startProgressActivity = widget.StartActivity

// bindNotifications registers press handling for notification IDs and cleans up
// any leftover progress notifications or Live Activities from an earlier run.
func (a *App) bindNotifications() {
	a.cx.OnNotificationPress(a.onNotifyPress)
	a.cx.CloseNotification(notifyProgressID)
	a.cleanupStaleLiveActivities()
	if isMobileNotifyRuntime() {
		// Android reports running LiveUpdates asynchronously at startup; sweep
		// once more so leftover cards from earlier builds are cleared cleanly.
		time.AfterFunc(600*time.Millisecond, func() {
			a.cx.Post(a.cleanupStaleLiveActivities)
		})
	}
}

func (a *App) cleanupStaleLiveActivities() {
	activeID := ""
	if a.progressAct != nil {
		activeID = a.progressAct.ID()
	}
	for _, act := range widget.Activities() {
		if act.Kind() == transferActivityKind && act.ID() != activeID {
			_ = act.End(nil, widget.DismissNow)
		}
	}
}

// bindDeepLinks routes app URL invocations (such as tapping an iOS Live Activity).
func (a *App) bindDeepLinks() {
	handle := func(u string) {
		if strings.HasPrefix(u, transferDeepLink) {
			a.selectTab(tabTransfers)
		}
	}
	if u := mobile.OpenedURL(a.cx); u != "" {
		handle(u)
	}
	mobile.OnOpenLinks(a.cx, func(links []string) {
		for _, u := range links {
			handle(u)
		}
	})
}

// onNotifyPress clears active notification tracking and opens Transfers.
func (a *App) onNotifyPress(id string) {
	if id == notifyProgressID {
		a.progressNotifyShown = false
		a.selectTab(tabTransfers)
		return
	}
	a.clearNotificationBatch()
	a.selectTab(tabTransfers)
}

func (a *App) clearNotificationBatch() {
	for _, id := range a.notifyShown {
		a.cx.CloseNotification(id)
	}
	a.cx.CloseNotification(notifySummaryID)
	a.notifyShown = nil
	a.notifyItemKeys = map[string]bool{}
	a.notifySummaryShown = false
	a.notifyCount = 0
	a.notifyOK = 0
	a.notifyFail = 0
	a.syncTransferBanner(a.tasks.GetUntracked())
}

// emitNotifications posts individual alerts while at most notifyCollapseLimit
// are pending, and collapses them into a single summary alert once exceeded.
func (a *App) emitNotifications(entries []notifyEntry) {
	if !a.prefs.GetUntracked().NotifyMaster() || len(entries) == 0 {
		return
	}
	a.bannerDismissedSig = ""
	if a.notifyItemKeys == nil {
		a.notifyItemKeys = map[string]bool{}
	}
	for i := range entries {
		if entries[i].key == "" {
			a.notifySeq++
			entries[i].key = "seq:" + strconv.Itoa(a.notifySeq)
		}
		e := entries[i]
		if prevOK, exists := a.notifyItemKeys[e.key]; exists {
			if prevOK {
				a.notifyOK--
			} else {
				a.notifyFail--
			}
		} else {
			a.notifyCount++
		}
		a.notifyItemKeys[e.key] = e.ok
		if e.ok {
			a.notifyOK++
		} else {
			a.notifyFail++
		}
	}
	if !a.notifySummaryShown && a.notifyCount <= notifyCollapseLimit {
		for _, e := range entries {
			id := notifyItemPrefix + e.key
			if !slices.Contains(a.notifyShown, id) {
				a.notifyShown = append(a.notifyShown, id)
			}
			_ = a.cx.ShowNotification(core.Notification{
				ID: id, Title: e.title, Body: locales.NotifyOpenTransfers(a.cx),
			}, nil)
		}
		a.syncTransferBanner(a.tasks.GetUntracked())
		return
	}
	a.showSummaryNotification()
	a.syncTransferBanner(a.tasks.GetUntracked())
}

func (a *App) showSummaryNotification() {
	for _, id := range a.notifyShown {
		a.cx.CloseNotification(id)
	}
	a.notifyShown = nil
	a.notifySummaryShown = true
	_ = a.cx.ShowNotification(core.Notification{
		ID: notifySummaryID, Title: a.summaryNotificationTitle(), Body: locales.NotifySummaryCollapsedBody(a.cx),
	}, nil)
}

func (a *App) summaryNotificationTitle() string {
	switch {
	case a.notifyOK > 0 && a.notifyFail > 0:
		return locales.NotifySummaryMixed(a.cx, a.notifyCount, strconv.Itoa(a.notifyOK), strconv.Itoa(a.notifyFail))
	case a.notifyFail > 0:
		return locales.NotifySummaryFailed(a.cx, a.notifyFail)
	default:
		return locales.NotifySummaryDone(a.cx, a.notifyOK)
	}
}

func (a *App) dismissNotifyItem(key string) {
	if key == "" || a.notifyItemKeys == nil {
		return
	}
	id := notifyItemPrefix + key
	a.cx.CloseNotification(id)
	a.notifyShown = slices.DeleteFunc(a.notifyShown, func(s string) bool { return s == id })
	wasOK, exists := a.notifyItemKeys[key]
	if !exists {
		return
	}
	delete(a.notifyItemKeys, key)
	if a.notifyCount > 0 {
		a.notifyCount--
	}
	if wasOK && a.notifyOK > 0 {
		a.notifyOK--
	} else if !wasOK && a.notifyFail > 0 {
		a.notifyFail--
	}
	if a.notifyCount == 0 {
		if a.notifySummaryShown {
			a.cx.CloseNotification(notifySummaryID)
			a.notifySummaryShown = false
		}
		a.syncTransferBanner(a.tasks.GetUntracked())
		return
	}
	if a.notifySummaryShown {
		_ = a.cx.ShowNotification(core.Notification{
			ID: notifySummaryID, Title: a.summaryNotificationTitle(), Body: locales.NotifySummaryCollapsedBody(a.cx),
		}, nil)
	}
	a.syncTransferBanner(a.tasks.GetUntracked())
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

func (a *App) markNotifyRequested() {
	a.savePrefs(func(p *store.Prefs) {
		p.EnsureNotifyPrefs()
		n := p.Notifications
		n.Asked = true
		n.Requested = true
		p.Notifications = n
	})
}

// ensureNotifyPermissionForTransfer asks the OS once when starting a transfer
// while notifications are enabled but were never requested yet (e.g. upgraded
// installs or dismissing the first-launch guide with Later).
func (a *App) ensureNotifyPermissionForTransfer() {
	if isWeb() {
		return
	}
	p := a.prefs.GetUntracked()
	if !p.NotifyMaster() || p.Notifications.Requested || a.notifyPermChecking {
		return
	}
	a.notifyPermChecking = true
	a.cx.NotificationPermission(func(perm core.NotificationPermission) {
		a.cx.Post(func() {
			a.notifyPermChecking = false
			if perm == core.NotificationsNotAsked {
				a.requestNotifyPermission(nil)
				return
			}
			a.markNotifyRequested()
			if perm == core.NotificationsGranted {
				a.syncTransferProgress(a.tasks.GetUntracked())
			}
		})
	})
}

// requestNotifyPermission asks the system, then continues with done on the
// engine goroutine.
func (a *App) requestNotifyPermission(done func(core.NotificationPermission)) {
	a.cx.RequestNotificationPermission(func(perm core.NotificationPermission) {
		a.cx.Post(func() {
			a.markNotifyRequested()
			if perm == core.NotificationsGranted {
				snap := a.tasks.GetUntracked()
				a.syncTransferProgress(snap)
				// On Android, NotificationManagerService may update areNotificationsEnabled
				// slightly after onRequestPermissionsResult; re-sync progress once if needed.
				if hasActiveTransfers(snap) && ((isAndroidProgressNotify() && !a.progressNotifyShown) || (!isAndroidProgressNotify() && isMobileNotifyRuntime() && a.progressAct == nil)) {
					time.AfterFunc(250*time.Millisecond, func() {
						a.cx.Post(func() {
							if isAndroidProgressNotify() && a.progressNotifyShown {
								return
							}
							if !isAndroidProgressNotify() && a.progressAct != nil {
								return
							}
							a.cx.NotificationPermission(func(p core.NotificationPermission) {
								a.cx.Post(func() {
									if p == core.NotificationsGranted {
										a.syncTransferProgress(a.tasks.GetUntracked())
									}
								})
							})
						})
					})
				}
			}
			if done != nil {
				done(perm)
			}
		})
	})
}

func hasActiveTransfers(snap []transfer.Task) bool {
	for _, t := range snap {
		if t.Status == transfer.Active {
			return true
		}
	}
	return false
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
		if seen && prev == transfer.Failed && t.Status != transfer.Failed {
			a.dismissNotifyItem("tr:" + t.ID)
		}
		// Count newly finished tasks, including ones that appear already Done
		// or Failed in the first snapshot after Add (fast transfers).
		becameDone := t.Status == transfer.Done && (!seen || prev != transfer.Done)
		becameFailed := t.Status == transfer.Failed && (!seen || prev != transfer.Failed)
		switch {
		case becameDone && t.Direction == transfer.Download:
			entries = append(entries, notifyEntry{key: "tr:" + t.ID, title: locales.NotifyItemDownloadDone(a.cx, t.Name), ok: true})
		case becameDone:
			entries = append(entries, notifyEntry{key: "tr:" + t.ID, title: locales.NotifyItemUploadDone(a.cx, t.Name), ok: true})
		case becameFailed:
			entries = append(entries, notifyEntry{key: "tr:" + t.ID, title: locales.NotifyItemTransferFailed(a.cx, t.Name), ok: false})
		}
	}
	// Drop statuses for removed tasks.
	alive := map[string]bool{}
	for _, t := range snap {
		alive[t.ID] = true
	}
	for id := range a.taskStatus {
		if !alive[id] {
			a.dismissNotifyItem("tr:" + id)
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
			entries = append(entries, notifyEntry{key: "off:" + key, title: locales.NotifyItemOfflineFailed(a.cx, name), ok: false})
		} else if t.State == openlist.TaskSucceeded {
			entries = append(entries, notifyEntry{key: "off:" + key, title: locales.NotifyItemOfflineDone(a.cx, name), ok: true})
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

// updateBadgeAndProgress sets the icon badge (active+failed), Dock/taskbar progress,
// power assertion while transfers run, and mobile progress notifications.
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
	a.syncKeepAwake(active > 0)
	a.syncTransferProgress(snap)
	a.syncTransferBanner(snap)
	a.syncDesktopTray(snap)
}

func (a *App) syncKeepAwake(active bool) {
	if active && a.releaseAwake == nil {
		a.releaseAwake = a.cx.KeepAwake("Active transfers", core.KeepAwakeOptions{Display: false})
	} else if !active && a.releaseAwake != nil {
		a.releaseAwake()
		a.releaseAwake = nil
	}
}

func (a *App) syncTransferProgress(snap []transfer.Task) {
	if isAndroidProgressNotify() {
		a.syncAndroidProgressNotification(snap)
		return
	}
	a.syncProgressActivity(snap)
}

func (a *App) endAndroidProgressNotification() {
	if a.progressNotifyShown {
		a.cx.CloseNotification(notifyProgressID)
		a.progressNotifyShown = false
	}
	a.progressNotifySig = ""
	a.progressNotifyKey = ""
	a.progressNotifyPct = -1
	a.progressNotifyAt = time.Time{}
}

func (a *App) syncAndroidProgressNotification(snap []transfer.Task) {
	if !a.prefs.GetUntracked().NotifyMaster() {
		a.endAndroidProgressNotification()
		return
	}
	active, sig, title, body, _, _, pct, _, _ := a.buildProgressSummary(snap)
	if !active {
		a.endAndroidProgressNotification()
		return
	}
	key := title + "|" + body
	now := time.Now()
	if a.progressNotifyShown {
		if key == a.progressNotifyKey && sig == a.progressNotifySig {
			return
		}
		elapsed := now.Sub(a.progressNotifyAt)
		pctStep := pct >= 0 && (a.progressNotifyPct < 0 || (a.progressNotifyPct == 0 && pct > 0) || absInt(pct-a.progressNotifyPct) >= 2)
		if sig == a.progressNotifySig && !(a.progressNotifyPct == 0 && pct > 0) && !(pctStep && elapsed >= 800*time.Millisecond) && elapsed < 1500*time.Millisecond {
			if !a.progressNotifyTimer {
				a.progressNotifyTimer = true
				delay := max(800*time.Millisecond-elapsed, 50*time.Millisecond)
				time.AfterFunc(delay, func() {
					a.cx.Post(func() {
						a.progressNotifyTimer = false
						a.syncTransferProgress(a.tasks.GetUntracked())
					})
				})
			}
			return
		}
	}
	a.progressNotifyShown = true
	a.progressNotifySig = sig
	a.progressNotifyKey = key
	a.progressNotifyPct = pct
	a.progressNotifyAt = now
	_ = a.cx.ShowNotification(core.Notification{
		ID: notifyProgressID, Title: title, Body: body, Silent: true,
	}, func(shown bool) {
		if !shown {
			a.cx.Post(func() {
				if a.progressNotifyAt.Equal(now) {
					a.progressNotifyShown = false
				}
			})
		}
	})
}

func (a *App) endProgressActivity() {
	if a.progressAct != nil {
		_ = a.progressAct.End(nil, widget.DismissNow)
		a.progressAct = nil
	}
	a.progressSig = ""
	a.progressLastKey = ""
	a.progressLastPct = -1
	a.progressLastAt = time.Time{}
}

func (a *App) buildProgressSummary(snap []transfer.Task) (activeNow bool, sig, title, body, status, symbol string, pct int, progress float64, showProgress bool) {
	var active []transfer.Task
	var doneBytes, totalBytes int64
	var totalSpeed float64
	indeterminate := false
	var sigB strings.Builder
	for _, t := range snap {
		if t.Status != transfer.Active {
			continue
		}
		active = append(active, t)
		sigB.WriteString(t.ID)
		sigB.WriteByte(';')
		totalSpeed += t.Speed
		if t.Total > 0 {
			doneBytes += t.Done
			totalBytes += t.Total
		} else {
			indeterminate = true
		}
	}
	if len(active) == 0 {
		return false, "", "", "", "", "", -1, 0, false
	}
	sig = sigB.String()
	pct = -1
	showProgress = !indeterminate && totalBytes > 0
	if showProgress {
		pct = int(min(100, doneBytes*100/totalBytes))
		progress = min(1, max(0, float64(doneBytes)/float64(totalBytes)))
	}
	if len(active) == 1 {
		t := active[0]
		if t.Direction == transfer.Upload {
			title = locales.NotifyProgressUploadTitle(a.cx, t.Name)
			symbol = "arrow.up.circle.fill"
		} else {
			title = locales.NotifyProgressDownloadTitle(a.cx, t.Name)
			symbol = "arrow.down.circle.fill"
		}
		switch {
		case t.Done == 0 && t.Total <= 0:
			body = locales.StatusWaiting(a.cx)
		case t.Total > 0:
			body = progressText(t)
		default:
			body = browse.Size(t.Done)
		}
		if t.Speed > 0 && !(t.Done == 0 && t.Total <= 0) {
			body += " · " + browse.Speed(t.Speed)
		}
		if showProgress {
			status = strconv.Itoa(pct) + "%"
		}
	} else {
		title = locales.NotifyProgressMultiTitle(a.cx, len(active))
		symbol = "arrow.up.arrow.down.circle.fill"
		switch {
		case showProgress:
			body = locales.NotifyProgressMultiBody(a.cx, strconv.Itoa(pct), browse.Size(doneBytes), browse.Size(totalBytes))
			status = strconv.Itoa(pct) + "%"
		case doneBytes > 0:
			body = browse.Size(doneBytes)
		default:
			body = locales.StatusWaiting(a.cx)
		}
		if totalSpeed > 0 && (showProgress || doneBytes > 0) {
			body += " · " + browse.Speed(totalSpeed)
		}
	}
	return true, sig, title, body, status, symbol, pct, progress, showProgress
}

// syncProgressActivity shows and updates a mobile Live Activity (iOS) while
// transfers are active, when notifications are enabled.
func (a *App) syncProgressActivity(snap []transfer.Task) {
	if a.progressUnsupported {
		return
	}
	if !a.prefs.GetUntracked().NotifyMaster() {
		a.progressDismissedSig = ""
		a.endProgressActivity()
		return
	}
	active, sig, title, body, status, symbol, pct, progress, showProgress := a.buildProgressSummary(snap)
	if !active {
		a.progressDismissedSig = ""
		a.endProgressActivity()
		return
	}
	if sig != a.progressDismissedSig {
		a.progressDismissedSig = ""
	}
	if a.progressAct == nil && a.progressDismissedSig == sig {
		return
	}

	content := widget.ActivityContent{
		Title:        title,
		Body:         body,
		Status:       status,
		Symbol:       symbol,
		Progress:     progress,
		ShowProgress: showProgress,
		URL:          transferDeepLink,
	}
	contentKey := title + "|" + body + "|" + status

	now := time.Now()
	if a.progressAct == nil {
		act, err := startProgressActivity(transferActivityKind, content)
		if err != nil {
			switch {
			case errors.Is(err, widget.ErrActivitiesUnsupported), errors.Is(err, widget.ErrNoActivities):
				a.progressUnsupported = true
			case errors.Is(err, widget.ErrActivitiesDisabled):
				a.refreshLiveActivitiesIfGranted()
			}
			return
		}
		a.progressAct = act
		a.progressSig = sig
		a.progressLastKey = contentKey
		a.progressLastPct = pct
		a.progressLastAt = now
		return
	}

	if contentKey == a.progressLastKey && sig == a.progressSig {
		return
	}
	elapsed := now.Sub(a.progressLastAt)
	pctStep := pct >= 0 && (a.progressLastPct < 0 || (a.progressLastPct == 0 && pct > 0) || absInt(pct-a.progressLastPct) >= 2)
	if sig == a.progressSig && !(a.progressLastPct == 0 && pct > 0) && !(pctStep && elapsed >= 800*time.Millisecond) && elapsed < 1500*time.Millisecond {
		if !a.progressTimerPending {
			a.progressTimerPending = true
			delay := max(800*time.Millisecond-elapsed, 50*time.Millisecond)
			time.AfterFunc(delay, func() {
				a.cx.Post(func() {
					a.progressTimerPending = false
					a.syncTransferProgress(a.tasks.GetUntracked())
				})
			})
		}
		return
	}
	if err := a.progressAct.Update(content); err != nil {
		if errors.Is(err, widget.ErrActivityEnded) {
			a.progressAct = nil
			a.progressDismissedSig = sig
		}
		return
	}
	a.progressSig = sig
	a.progressLastKey = contentKey
	a.progressLastPct = pct
	a.progressLastAt = now
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// refreshLiveActivitiesIfGranted asks the platform for current notification
// permission (which refreshes NSGo's cached Live Activity availability on mobile)
// so granting permission via Settings or a just-dismissed dialog takes effect
// without restarting the app.
func (a *App) refreshLiveActivitiesIfGranted() {
	if !isMobileNotifyRuntime() {
		return
	}
	now := time.Now()
	if !a.progressPermSyncAt.IsZero() && now.Sub(a.progressPermSyncAt) < 2*time.Second {
		return
	}
	a.progressPermSyncAt = now
	a.cx.NotificationPermission(func(perm core.NotificationPermission) {
		a.cx.Post(func() {
			if perm == core.NotificationsGranted {
				a.syncTransferProgress(a.tasks.GetUntracked())
			}
		})
	})
}

// saveNotifyToggle updates one notification preference and may request permission.
func (a *App) saveNotifyToggle(change func(*store.NotifyPrefs)) {
	a.savePrefs(func(p *store.Prefs) {
		p.EnsureNotifyPrefs()
		n := p.Notifications
		change(&n)
		p.Notifications = n
	})
	a.syncTransferProgress(a.tasks.GetUntracked())
	a.syncTransferBanner(a.tasks.GetUntracked())
	if a.prefs.GetUntracked().NotifyMaster() && !a.prefs.GetUntracked().Notifications.Requested {
		a.requestNotifyPermission(func(perm core.NotificationPermission) {
			if perm == core.NotificationsDenied {
				a.raise([]accessIssue{{Kind: access.Notifications, Detail: "denied"}})
			}
		})
	}
}

func (a *App) syncTransferBanner(snap []transfer.Task) {
	if !a.prefs.GetUntracked().NotifyMaster() {
		a.transferBanner.Set(transferBannerState{})
		return
	}
	activeNow, sig, title, body, status, _, _, progress, showProgress := a.buildProgressSummary(snap)
	if activeNow {
		progSig := "prog:" + sig
		if a.bannerDismissedSig == progSig {
			a.transferBanner.Set(transferBannerState{})
			return
		}
		a.transferBanner.Set(transferBannerState{
			Visible:      true,
			Ongoing:      true,
			Heading:      locales.TransferBannerProgressHeading(a.cx),
			Status:       status,
			Title:        title,
			Body:         body,
			Progress:     float32(progress),
			ShowProgress: showProgress,
		})
		return
	}
	if a.notifyCount > 0 {
		sumSig := "sum:" + strconv.Itoa(a.notifyCount) + ":" + strconv.Itoa(a.notifyOK) + ":" + strconv.Itoa(a.notifyFail)
		if a.bannerDismissedSig == sumSig {
			a.transferBanner.Set(transferBannerState{})
			return
		}
		var dlBytes, ulBytes int64
		for _, t := range snap {
			if t.Status != transfer.Done {
				continue
			}
			sz := t.Total
			if sz <= 0 {
				sz = t.Done
			}
			if t.Direction == transfer.Upload {
				ulBytes += sz
			} else {
				dlBytes += sz
			}
		}
		a.transferBanner.Set(transferBannerState{
			Visible: true,
			Ongoing: false,
			Heading: locales.TransferBannerSummaryHeading(a.cx),
			Title:   a.summaryNotificationTitle(),
			Body:    locales.TransferBannerSummaryBody(a.cx, browse.Size(dlBytes), browse.Size(ulBytes)),
		})
		return
	}
	a.transferBanner.Set(transferBannerState{})
}

func (a *App) dismissTransferBanner() {
	b := a.transferBanner.GetUntracked()
	snap := a.tasks.GetUntracked()
	if b.Ongoing {
		if activeNow, sig, _, _, _, _, _, _, _ := a.buildProgressSummary(snap); activeNow {
			a.bannerDismissedSig = "prog:" + sig
		}
		a.transferBanner.Set(transferBannerState{})
		return
	}
	if a.notifyCount > 0 {
		a.bannerDismissedSig = "sum:" + strconv.Itoa(a.notifyCount) + ":" + strconv.Itoa(a.notifyOK) + ":" + strconv.Itoa(a.notifyFail)
	}
	a.clearNotificationBatch()
}
