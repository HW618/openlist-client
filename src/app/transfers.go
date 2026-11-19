package app

import (
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/HW618/openlist-client/src/internal/browse"
	"github.com/HW618/openlist-client/src/internal/transfer"
	"github.com/HW618/openlist-client/src/locales"
	"github.com/HW618/openlist-client/src/theme"
)

// matches reports whether a task belongs under a filter.
func matches(filter string, t transfer.Task) bool {
	switch filter {
	case "active":
		return t.Status == transfer.Active || t.Status == transfer.Paused
	case "done":
		return t.Status == transfer.Done
	case "failed":
		return t.Status == transfer.Failed
	}
	return true
}

// running counts the tasks that are moving or waiting to.
func running(tasks []transfer.Task) int {
	n := 0
	for _, t := range tasks {
		if t.Status == transfer.Active {
			n++
		}
	}
	return n
}

// transfersPage is the second tab: what is downloading and uploading.
func (a *App) transfersPage(cx *core.Scope) core.Node {
	shown := core.NewMemoFunc(cx, func() []transfer.Task {
		f := a.filter.Get()
		var out []transfer.Task
		for _, t := range a.tasks.Get() {
			if matches(f, t) {
				out = append(out, t)
			}
		}
		return out
	}, func(x, y []transfer.Task) bool { return false })

	filters := core.View{Role: core.RoleTabList, Label: locales.FilterLabel(cx), Style: core.Style{Direction: core.Row, Gap: 8, Padding: core.Symmetric(4, theme.Space4)},
		Children: []core.Node{
			a.filterChip(cx, "all", locales.FilterAll(cx)),
			a.filterChip(cx, "active", locales.FilterActive(cx)),
			a.filterChip(cx, "done", locales.FilterDone(cx)),
			a.filterChip(cx, "failed", locales.FilterFailed(cx)),
		}}
	list := core.Scroll{Style: fillStyle, Children: []core.Node{
		core.View{Style: core.Style{Gap: 10, Padding: core.Edges{Left: theme.Space4, Right: theme.Space4, Top: 8, Bottom: 24}}, Children: []core.Node{
			a.offlineSection(cx),
			core.Each(shown.Get, func(t transfer.Task) string { return t.ID },
				func(cx *core.Scope, task func() transfer.Task, _ func() int) core.Node { return a.taskCard(cx, task) })}}}}
	empty := core.Show{When: func() bool { return len(shown.Get()) == 0 && len(a.offlineShown()) == 0 && a.offline.Get().Err == "" }, Then: func(cx *core.Scope) core.Node {
		return emptyState(cx, lucide.ArrowLeftRight, locales.TransfersEmptyTitle(cx), locales.TransfersEmptyDesc(cx), nil)
	}, Else: func(cx *core.Scope) core.Node { return list }}
	return page(cx,
		header(cx, core.View{Style: fillStyle, Children: []core.Node{txt(cx, locales.TransfersTitle(cx), theme.TypeTitle, cText)}},
			textButton(cx, func() string { return locales.ClearDone(cx) }, func() {
				if a.engine.ClearDone() == 0 {
					a.toast(locales.ToastNoDone(cx))
				} else {
					a.toast(locales.ToastClearedDone(cx))
				}
				a.syncTasks()
			})),
		filters,
		core.View{Style: fillStyle, Children: []core.Node{empty}})
}

func (a *App) filterChip(cx *core.Scope, id, text string) core.Node {
	return chipID(cx, "filter-"+id, text, func() bool { return a.filter.Get() == id }, func() { a.filter.Set(id) })
}

// taskCard is one transfer.
func (a *App) taskCard(cx *core.Scope, task func() transfer.Task) core.Node {
	th := theme.Of(cx)
	t0 := task()
	id := t0.ID
	glyph := lucide.Download
	if t0.Direction == transfer.Upload {
		glyph = lucide.Upload
	}
	status := func() string {
		t := task()
		switch t.Status {
		case transfer.Done:
			return locales.StatusDone(cx) + " · " + browse.Size(t.Total)
		case transfer.Paused:
			return locales.StatusPaused(cx) + " · " + progressText(t)
		case transfer.Failed:
			tag := locales.DownloadFailedTag(cx)
			if t.Direction == transfer.Upload {
				tag = locales.UploadFailedTag(cx)
			}
			if t.Err != "" {
				return tag + ": " + t.Err
			}
			return tag
		}
		if t.Done == 0 && t.Total == 0 {
			return locales.StatusWaiting(cx)
		}
		s := progressText(t)
		if t.Speed > 0 {
			s += " · " + browse.Speed(t.Speed)
		}
		return s
	}
	statusColor := func(c theme.Colors) core.Color {
		switch task().Status {
		case transfer.Failed:
			return c.Danger()
		case transfer.Done:
			return c.Up()
		}
		return c.Muted()
	}
	bar := core.Show{When: func() bool { s := task().Status; return s == transfer.Active || s == transfer.Paused }, Then: func(cx *core.Scope) core.Node {
		return ui.Progress{Value: func() float32 { return float32(task().Percent()) / 100 }, Label: t0.Name}
	}}
	act := func(svg func() *core.SVG, label string, run func()) core.Node {
		return ui.IconButton{Icon: svg, Label: label, OnPress: run, Size: 36, IconSize: 18}
	}
	actions := core.View{Style: core.Style{Direction: core.Row, Gap: 2, FlexShrink: 0}, Children: []core.Node{
		core.Show{When: func() bool { return task().Status == transfer.Active }, Then: func(cx *core.Scope) core.Node {
			return act(lucide.Pause, locales.Pause(cx), func() { a.engine.Pause(id); a.syncTasks() })
		}},
		core.Show{When: func() bool { return task().Status == transfer.Paused }, Then: func(cx *core.Scope) core.Node {
			return act(lucide.Play, locales.Resume(cx), func() { a.engine.Resume(id); a.syncTasks() })
		}},
		core.Show{When: func() bool { return task().Status == transfer.Failed }, Then: func(cx *core.Scope) core.Node {
			return act(lucide.RotateCcw, locales.Retry(cx), func() { a.toast(locales.ToastRetrying(cx, t0.Name)); a.engine.Retry(id); a.syncTasks() })
		}},
		core.Show{When: func() bool { s := task().Status; return s == transfer.Active || s == transfer.Paused },
			Then: func(cx *core.Scope) core.Node {
				return act(lucide.X, locales.CancelTask(cx), func() { a.engine.Cancel(id); a.syncTasks() })
			},
			Else: func(cx *core.Scope) core.Node {
				return act(lucide.Trash, locales.RemoveRecord(cx), func() { a.engine.Remove(id); a.syncTasks() })
			}},
	}}
	return core.View{Role: core.RoleGroup, Label: t0.Name,
		Style:     core.Style{Gap: 8, Padding: core.All(12), BorderWidth: 1, CornerRadius: theme.RadiusMD, BorderCurve: core.BorderCurveContinuous},
		StyleFunc: cardStyle(cx, theme.RadiusMD),
		Children: []core.Node{
			row(12,
				tile(cx, glyph, "", 36, 10),
				core.View{Style: fillStyle.Merge(core.Style{Gap: 2}), Children: []core.Node{
					oneLine(txt(cx, t0.Name, theme.TypeBody, cText)),
					core.Text{ContentFunc: status, MaxLines: 2, EllipsizeMode: core.EllipsizeTail, Style: theme.TypeCaption,
						StyleFunc: func() core.Style { return core.Style{Color: statusColor(th)} }},
				}},
				actions),
			bar,
		}}
}

// progressText is how much of a task is done: "45% · 4.5 MB / 10 MB".
func progressText(t transfer.Task) string {
	if t.Total <= 0 {
		return browse.Size(t.Done)
	}
	return itoa(t.Percent()) + "% · " + browse.Size(t.Done) + " / " + browse.Size(t.Total)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
