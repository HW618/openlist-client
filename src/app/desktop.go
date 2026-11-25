package app

import (
	_ "embed"
	"strconv"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/desktop"

	"github.com/HW618/openlist-client/src/internal/transfer"
	"github.com/HW618/openlist-client/src/locales"
)

//go:embed tray_icon.png
var trayIconPNG []byte

// desktopTransferSummary returns active task counts, average completion
// percentage, and split download/upload counts for the desktop tray.
func desktopTransferSummary(snap []transfer.Task) (count, pct, dl, ul int) {
	var doneBytes, totalBytes int64
	pctSum := 0
	for _, t := range snap {
		if t.Status != transfer.Active {
			continue
		}
		count++
		if t.Direction == transfer.Upload {
			ul++
		} else {
			dl++
		}
		pctSum += t.Percent()
		if t.Total > 0 {
			doneBytes += t.Done
			totalBytes += t.Total
		}
	}
	if count == 0 {
		return 0, 0, 0, 0
	}
	if totalBytes > 0 {
		pct = int(min(100, doneBytes*100/totalBytes))
	} else {
		pct = pctSum / count
	}
	return count, pct, dl, ul
}

// setupDesktopApp registers desktop-only lifecycle handlers (tray icon + menu,
// ShutdownOnExplicit, Dock reopen, quit confirmation for active transfers) and
// returns an attach callback to bind each mounted window's closing behavior.
func setupDesktopApp(appScope *core.Scope, getApp func() *App) func(*App, *core.Scope) {
	desktop.SetShutdownMode(appScope, desktop.ShutdownOnExplicit)

	tray := desktop.NewTray(appScope, desktop.TrayOptions{
		Icon:    trayIconPNG,
		Tooltip: locales.TrayTooltipIdle(appScope),
		Menu: func() []desktop.MenuItem {
			a := getApp()
			if a == nil {
				return []desktop.MenuItem{
					{Label: locales.TrayMenuIdle(appScope), Disabled: true},
					{Separator: true},
					{Label: locales.TrayQuit(appScope), OnSelect: func() { desktop.RequestQuit(appScope) }},
				}
			}
			count, pct, _, _ := desktopTransferSummary(a.tasks.GetUntracked())
			statusLabel := locales.TrayMenuIdle(a.cx)
			if count > 0 {
				statusLabel = locales.TrayMenuActive(a.cx, count, strconv.Itoa(pct))
			}
			return []desktop.MenuItem{
				{Label: statusLabel, Disabled: true},
				{Separator: true},
				{Label: locales.TrayShowWindow(a.cx), OnSelect: func() { a.restoreWindow(false) }},
				{Label: locales.TrayOpenTransfers(a.cx), OnSelect: func() { a.restoreWindow(true) }},
				{Separator: true},
				{Label: locales.TrayQuit(a.cx), OnSelect: func() { desktop.RequestQuit(appScope) }},
			}
		},
	})

	desktop.OnReopen(appScope, func() {
		if a := getApp(); a != nil {
			a.restoreWindow(false)
		}
	})

	desktop.OnQuitRequest(appScope, func(q *desktop.QuitRequest) {
		a := getApp()
		if a != nil {
			a.flushTaskStore()
		}
		if q.Forced() || a == nil || running(a.tasks.GetUntracked()) == 0 {
			if a != nil {
				a.pendingQuit = nil
			}
			return
		}
		if a.pendingQuit != nil {
			q.Cancel()
			a.restoreWindow(false)
			return
		}
		a.pendingQuit = q.Defer()
		a.restoreWindow(false)
		a.openSheet(sheetState{Kind: sheetQuitConfirm})
	})

	return func(a *App, winScope *core.Scope) {
		a.appScope = appScope
		a.tray = tray
		win := desktop.CurrentWindow(winScope)
		if win == nil {
			win = desktop.MainWindow(appScope)
		}
		a.mainWin = win
		if win != nil {
			win.OnClosing(winScope, func(c *desktop.WindowClosing) {
				c.Cancel()
				if a.prefs.GetUntracked().CloseToTray() {
					a.closeSheet()
					win.Hide()
					return
				}
				desktop.RequestQuit(appScope)
			})
		}
		winScope.Cleanup(a.cancelPendingQuit)
		a.syncDesktopTray(a.tasks.GetUntracked())
	}
}

func (a *App) cancelPendingQuit() {
	if a.pendingQuit == nil {
		return
	}
	done := a.pendingQuit
	a.pendingQuit = nil
	done(false)
}

func (a *App) confirmQuitNow() {
	a.flushTaskStore()
	done := a.pendingQuit
	a.pendingQuit = nil
	a.sheet.Set(sheetState{})
	if done != nil {
		done(true)
		return
	}
	if a.appScope != nil {
		a.appScope.Quit()
		return
	}
	a.cx.Quit()
}

func (a *App) restoreWindow(openTransfers bool) {
	if openTransfers {
		a.selectTab(tabTransfers)
	}
	w := a.mainWin
	if w == nil && a.appScope != nil {
		w = desktop.MainWindow(a.appScope)
	}
	if w != nil {
		w.Show()
		w.Focus()
	}
	desktop.OrderFront(a.cx)
}

func (a *App) syncDesktopTray(snap []transfer.Task) {
	if a.tray == nil {
		return
	}
	count, pct, dl, ul := desktopTransferSummary(snap)
	if count > 0 {
		prefix := "↓ "
		switch {
		case dl > 0 && ul > 0:
			prefix = "↕ "
		case ul > 0:
			prefix = "↑ "
		}
		a.tray.SetTitle(prefix + strconv.Itoa(count))
		a.tray.SetTooltip(locales.TrayTooltipActive(a.cx, count, strconv.Itoa(pct)))
		return
	}
	a.tray.SetTitle("")
	a.tray.SetTooltip(locales.TrayTooltipIdle(a.cx))
}
