package app

import (
	"context"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/HW618/openlist-client/src/internal/openlist"
	"github.com/HW618/openlist-client/src/locales"
	"github.com/HW618/openlist-client/src/theme"
)

// offlineEvery is how often the server is asked about offline tasks while the
// Transfers tab is open.
const offlineEvery = 3 * time.Second

// offlineState is what the Transfers tab knows of the offline downloads of the
// current site.
type offlineState struct {
	// SiteID is the site the tasks are from.
	SiteID string
	Tasks  []openlist.OfflineTask
	// Err is why they could not be read; Admin says it was for lack of rights.
	Err   string
	Admin bool
	// Loaded is whether the server answered at least once.
	Loaded bool
}

// selectTab shows a tab, and starts reading offline tasks on the Transfers one.
func (a *App) selectTab(i int) {
	a.tab.Set(i)
	if i == tabTransfers {
		a.pollOffline()
	}
}

// pollOffline reads the tasks now and again every few seconds while the
// Transfers tab is shown.
func (a *App) pollOffline() {
	a.refreshOffline()
	if a.polling {
		return
	}
	a.polling = true
	var tick func()
	tick = func() {
		if a.tab.GetUntracked() != tabTransfers {
			a.polling = false
			return
		}
		a.refreshOffline()
		a.cx.AfterFunc(offlineEvery, tick)
	}
	a.cx.AfterFunc(offlineEvery, tick)
}

// refreshOffline reads the offline tasks of the current site.
func (a *App) refreshOffline() {
	s, ok := a.site()
	c := a.client()
	if !ok || c == nil {
		a.offline.Set(offlineState{})
		return
	}
	if a.offlineBusy {
		return
	}
	a.offlineBusy = true
	goPost(a.cx, func(ctx context.Context) ([]openlist.OfflineTask, error) { return c.OfflineTasks(ctx) },
		func(list []openlist.OfflineTask, err error) {
			a.offlineBusy = false
			cur, now := a.site()
			if !now || cur.ID != s.ID {
				return
			}
			st := offlineState{SiteID: s.ID, Tasks: list, Loaded: true}
			if err != nil {
				st.Tasks = nil
				st.Err = errText(err)
				st.Admin = openlist.Unauthorized(err)
				if prev := a.offline.GetUntracked(); prev.SiteID == s.ID && !st.Admin {
					st.Tasks = prev.Tasks
				}
			}
			a.offline.Set(st)
		})
}

// offlineAction cancels, retries or deletes a task on the server.
func (a *App) offlineAction(t openlist.OfflineTask, action string) {
	c := a.client()
	if c == nil {
		return
	}
	goPost(a.cx, func(ctx context.Context) (struct{}, error) { return struct{}{}, c.OfflineTaskAction(ctx, t, action) },
		func(_ struct{}, err error) {
			if err != nil {
				a.fail(err)
			}
			a.refreshOffline()
		})
}

// offlineHasDone reports whether the server holds a task that is finished.
func (a *App) offlineHasDone() bool {
	for _, t := range a.offline.Get().Tasks {
		if t.Finished() {
			return true
		}
	}
	return false
}

// clearOfflineDone has the server forget the finished offline tasks.
func (a *App) clearOfflineDone() {
	c := a.client()
	if c == nil {
		return
	}
	if !a.offlineHasDone() {
		a.toast(locales.ToastNoDone(a.cx))
		return
	}
	goPost(a.cx, func(ctx context.Context) (struct{}, error) { return struct{}{}, c.ClearOfflineDone(ctx) },
		func(_ struct{}, err error) {
			if err != nil {
				a.fail(err)
			} else {
				a.toast(locales.ToastOfflineCleared(a.cx))
			}
			a.refreshOffline()
		})
}

// offlineMatches reports whether a task belongs under a filter.
func offlineMatches(filter string, t openlist.OfflineTask) bool {
	switch filter {
	case "active":
		return !t.Finished()
	case "done":
		return t.State == openlist.TaskSucceeded
	case "failed":
		return t.Failed()
	}
	return true
}

// offlineShown is the tasks the filter lets through.
func (a *App) offlineShown() []openlist.OfflineTask {
	st := a.offline.Get()
	f := a.filter.Get()
	var out []openlist.OfflineTask
	for _, t := range st.Tasks {
		if offlineMatches(f, t) {
			out = append(out, t)
		}
	}
	return out
}

// offlineSection is the offline downloads of the site, under the transfers.
func (a *App) offlineSection(cx *core.Scope) core.Node {
	th := theme.Of(cx)
	notice := core.Show{When: func() bool { return a.offline.Get().Err != "" }, Then: func(cx *core.Scope) core.Node {
		return core.View{Style: core.Style{Padding: core.All(12), BorderWidth: 1, CornerRadius: theme.RadiusMD}, StyleFunc: cardStyle(cx, theme.RadiusMD),
			TestID: "offline-notice",
			Children: []core.Node{core.Text{ContentFunc: func() string {
				st := a.offline.Get()
				if st.Admin {
					return locales.OfflineNeedAdmin(cx)
				}
				return locales.OfflineLoadError(cx, st.Err)
			}, Style: theme.TypeCaption, StyleFunc: func() core.Style { return core.Style{Color: th.Muted()} }}}}
	}}
	list := core.Each(a.offlineShown, func(t openlist.OfflineTask) string {
		if t.Transfer {
			return "t" + t.ID
		}
		return "d" + t.ID
	}, func(cx *core.Scope, task func() openlist.OfflineTask, _ func() int) core.Node {
		return a.offlineCard(cx, task)
	})
	return core.Show{When: func() bool { st := a.offline.Get(); return st.Err != "" || len(st.Tasks) > 0 }, Then: func(cx *core.Scope) core.Node {
		return core.View{Style: core.Style{Gap: 10}, Children: []core.Node{
			row(8,
				core.View{Style: fillStyle, Children: []core.Node{txt(cx, locales.OfflineTasksTitle(cx), theme.TypeSmall.Merge(core.Style{FontWeight: ui.Semibold}), cSecond)}},
				core.Show{When: a.offlineHasDone, Then: func(cx *core.Scope) core.Node {
					return textButtonID(cx, "offline-clear-done", func() string { return locales.OfflineClearDone(cx) }, a.clearOfflineDone)
				}},
				iconButton(lucide.RefreshCw, locales.OfflineRefresh(cx), a.refreshOffline)),
			notice, list}}
	}}
}

// offlineStatus is the words for the state of a task.
func offlineStatus(cx *core.Scope, t openlist.OfflineTask) string {
	var s string
	switch t.State {
	case openlist.TaskPending:
		s = locales.OfflineStateWaiting(cx)
	case openlist.TaskRunning:
		s = locales.OfflineStateRunning(cx)
		if t.Transfer {
			s = locales.OfflineTransferTag(cx)
		}
	case openlist.TaskSucceeded:
		s = locales.OfflineStateDone(cx)
	case openlist.TaskCanceling, openlist.TaskFailing:
		s = locales.OfflineStateCanceling(cx)
	case openlist.TaskCanceled:
		s = locales.OfflineStateCanceled(cx)
	case openlist.TaskWaitingRetry, openlist.TaskBeforeRetry:
		s = locales.OfflineStateRetry(cx)
	default:
		s = locales.OfflineStateFailed(cx)
	}
	if t.State == openlist.TaskRunning || t.State == openlist.TaskPending {
		if t.Progress > 0 {
			s += " · " + itoa(int(t.Progress)) + "%"
		}
	}
	if t.Failed() && t.Error != "" {
		s += ": " + t.Error
	} else if t.Status != "" && !t.Finished() {
		s += " · " + t.Status
	}
	return s
}

// offlineCard is one offline task.
func (a *App) offlineCard(cx *core.Scope, task func() openlist.OfflineTask) core.Node {
	th := theme.Of(cx)
	t0 := task()
	glyph := lucide.CloudDownload
	if t0.Transfer {
		glyph = lucide.HardDriveUpload
	}
	statusColor := func(c theme.Colors) core.Color {
		t := task()
		switch {
		case t.Failed():
			return c.Danger()
		case t.State == openlist.TaskSucceeded:
			return c.Up()
		}
		return c.Muted()
	}
	bar := core.Show{When: func() bool { return !task().Finished() }, Then: func(cx *core.Scope) core.Node {
		return ui.Progress{Value: func() float32 { return float32(task().Progress) / 100 }, Label: t0.Name}
	}}
	act := func(svg func() *core.SVG, label, action string) core.Node {
		return ui.IconButton{Icon: svg, Label: label, OnPress: func() { a.offlineAction(task(), action) }, Size: 36, IconSize: 18}
	}
	actions := core.View{Style: core.Style{Direction: core.Row, Gap: 2, FlexShrink: 0}, Children: []core.Node{
		core.Show{When: func() bool { return task().Failed() }, Then: func(cx *core.Scope) core.Node {
			return act(lucide.RotateCcw, locales.Retry(cx), "retry")
		}},
		core.Show{When: func() bool { return !task().Finished() },
			Then: func(cx *core.Scope) core.Node { return act(lucide.X, locales.CancelTask(cx), "cancel") },
			Else: func(cx *core.Scope) core.Node { return act(lucide.Trash, locales.RemoveRecord(cx), "delete") }},
	}}
	return core.View{Role: core.RoleGroup, Label: t0.Name, TestID: "offline-task",
		Style:     core.Style{Gap: 8, Padding: core.All(12), BorderWidth: 1, CornerRadius: theme.RadiusMD, BorderCurve: core.BorderCurveContinuous},
		StyleFunc: cardStyle(cx, theme.RadiusMD),
		Children: []core.Node{
			row(12,
				tile(cx, glyph, "", 36, 10),
				core.View{Style: fillStyle.Merge(core.Style{Gap: 2}), Children: []core.Node{
					core.Text{ContentFunc: func() string { return task().Name }, MaxLines: 2, EllipsizeMode: core.EllipsizeTail, Style: theme.TypeBody,
						StyleFunc: func() core.Style { return core.Style{Color: th.Text()} }},
					core.Text{ContentFunc: func() string { return offlineStatus(cx, task()) }, MaxLines: 3, EllipsizeMode: core.EllipsizeTail, Style: theme.TypeCaption,
						StyleFunc: func() core.Style { return core.Style{Color: statusColor(th)} }},
				}},
				actions),
			bar,
		}}
}
