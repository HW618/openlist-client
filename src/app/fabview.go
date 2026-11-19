package app

import (
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/HW618/openlist-client/src/internal/browse"
	"github.com/HW618/openlist-client/src/locales"
	"github.com/HW618/openlist-client/src/theme"
)

// fabBounds is the size of the Files page, which the button moves in.
func (a *App) fabBounds() (w, h float32) {
	b := a.fabBox.Get()
	return float32(b.Width), float32(b.Height)
}

// fabPosition is where the button rests: where it was left, or its home
// before it was moved, kept inside the page.
func (a *App) fabPosition() browse.Pos {
	w, h := a.fabBounds()
	p := a.fabPos.Get()
	if p.X < 0 {
		return browse.Home(w, h)
	}
	return browse.Clamp(p, w, h)
}

// fabDrag moves the button with the finger, and lets it dock when it is let
// go near a side.
func (a *App) fabPan(e core.PanEvent) {
	w, h := a.fabBounds()
	switch e.Phase {
	case core.GestureBegan:
		a.cx.Batch(func() {
			a.fabStart = a.fabPosition()
			a.fabOpen.Set(false)
			a.fabSide.Set(browse.SideNone)
			a.fabDrag.Set(true)
		})
	case core.GestureChanged:
		a.fabPos.Set(browse.Clamp(browse.Pos{X: a.fabStart.X + float32(e.Translation.X), Y: a.fabStart.Y + float32(e.Translation.Y)}, w, h))
	default:
		p, side := browse.Release(a.fabPosition(), w)
		a.cx.Batch(func() {
			a.fabPos.Set(p)
			a.fabSide.Set(side)
			a.fabDrag.Set(false)
		})
	}
}

// fabEntry is one button of the menu.
type fabEntry struct {
	icon  func() *core.SVG
	label string
	on    func() bool
	run   func()
}

// fab is the round button that opens the quick actions. It can be dragged
// anywhere on the page, and docks into a side it is let go near, leaving a
// fifth of itself in view.
func (a *App) fab(cx *core.Scope) core.Node {
	th := theme.Of(cx)
	entries := []fabEntry{
		{lucide.SquareCheck, locales.FabToggleSelect(cx), a.selecting.Get, func() {
			if a.selecting.GetUntracked() {
				a.endSelect()
			} else {
				a.selecting.Set(true)
			}
		}},
		{lucide.CloudDownload, locales.FabOffline(cx), nil, a.openOffline},
		{lucide.FilePlus, locales.FabNewFile(cx), nil, func() { a.openSheet(sheetState{Kind: sheetNewFile}) }},
		{lucide.FolderPlus, locales.FabMkdir(cx), nil, func() { a.openSheet(sheetState{Kind: sheetMkdir}) }},
		{lucide.Upload, locales.FabUpload(cx), nil, func() { a.openSheet(sheetState{Kind: sheetUpload}) }},
		{lucide.RefreshCw, locales.FabRefresh(cx), nil, func() { a.refresh(true) }},
	}
	labels := make([]string, len(entries))
	for i, e := range entries {
		labels[i] = e.label
	}
	itemW := browse.ItemWidth(labels...)

	main := core.View{
		Role: core.RoleButton, TestID: "fab",
		LabelFunc: func() string {
			if a.fabOpen.Get() {
				return locales.FabCloseLabel(cx)
			}
			return locales.FabLabel(cx)
		},
		OnPress:       func(core.PressEvent) { a.fabOpen.Update(func(b bool) bool { return !b }) },
		OnPan:         a.fabPan,
		PanActivation: core.PanActivation{ActiveOffsetX: 6, ActiveOffsetY: 6},
		Style: core.Style{Position: core.Absolute, Width: core.Pt(core.Points(browse.FabSize)), Height: core.Pt(core.Points(browse.FabSize)),
			CornerRadius: core.Points(browse.FabSize) / 2, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, ZIndex: 20},
		StyleFunc: func() core.Style {
			p := a.fabPosition()
			w, _ := a.fabBounds()
			side := a.fabSide.Get()
			open, drag := a.fabOpen.Get(), a.fabDrag.Get()
			x := p.X
			opacity := core.Alpha(1)
			if side != browse.SideNone && !open && !drag {
				x += browse.Docked(p, side, w)
				opacity = core.Alpha(0.72)
			}
			s := core.Style{Left: core.Pt(core.Points(x)), Top: core.Pt(core.Points(p.Y)), Background: th.Accent(), Opacity: opacity,
				Shadows: []core.BoxShadow{{Color: ui.Alpha(th.Accent(), 0.38), Offset: core.Point{Y: 8}, Blur: 24}}}
			if !drag {
				s.Transition = core.Transition{Duration: 200 * time.Millisecond, Easing: core.EaseOut, Properties: core.TransitionLayout}
			}
			return s
		},
		Children: []core.Node{core.View{
			StyleFunc: func() core.Style {
				if a.fabOpen.Get() {
					return core.Style{Transform: core.Transform{Rotate: 45}}
				}
				return core.Style{}
			},
			Children: []core.Node{icon(cx, lucide.Plus, 26, cOnAccent)}}},
	}

	menu := core.Show{When: a.fabOpen.Get, Then: func(cx *core.Scope) core.Node {
		var m browse.Menu
		var left bool
		cx.Untrack(func() {
			w, h := a.fabBounds()
			p := a.fabPosition()
			m = browse.LayoutMenu(p, w, h, len(entries), itemW)
			left = m.Left <= p.X+browse.FabSize/2 && p.X+browse.FabSize/2 < w/2
		})
		items := make([]core.Node, len(entries))
		for i, e := range entries {
			items[i] = a.fabItem(cx, e, itemW, left)
		}
		backdrop := core.View{NoFocus: true, OnPress: func(core.PressEvent) { a.fabOpen.Set(false) },
			Style: core.Style{Position: core.Absolute, Left: core.Pt(0), Top: core.Pt(0), Right: core.Pt(0), Bottom: core.Pt(0), ZIndex: 18}}
		list := core.View{
			Role: core.RoleMenu, Label: locales.FabLabel(cx), TestID: "fab-menu",
			Style: core.Style{Position: core.Absolute, Left: core.Pt(core.Points(m.Left)), Top: core.Pt(core.Points(m.Top)),
				Width: core.Pt(core.Points(m.Width)), Height: core.Pt(core.Points(m.Height)), Direction: core.Column, Wrap: core.Wrap,
				RowGap: 10, ColumnGap: 12, AlignContent: core.AlignStart, ZIndex: 19},
			Children: items,
		}
		return core.View{Style: core.Style{Position: core.Absolute, Left: core.Pt(0), Top: core.Pt(0), Right: core.Pt(0), Bottom: core.Pt(0), ZIndex: 17},
			PointerEvents: core.PointerEventsBoxNone, Children: []core.Node{backdrop, list}}
	}}

	return core.View{
		Style:         core.Style{Position: core.Absolute, Left: core.Pt(0), Top: core.Pt(0), Right: core.Pt(0), Bottom: core.Pt(0)},
		PointerEvents: core.PointerEventsBoxNone,
		Children:      []core.Node{menu, main},
	}
}

// fabItem is a labeled round button of the menu. A menu on the left of the
// page puts the round button first.
func (a *App) fabItem(cx *core.Scope, e fabEntry, width float32, left bool) core.Node {
	th := theme.Of(cx)
	on := func() bool { return e.on != nil && e.on() }
	label := core.View{
		Style: core.Style{Padding: core.Symmetric(6, 12), CornerRadius: theme.RadiusSM, BorderWidth: 1},
		StyleFunc: func() core.Style {
			return core.Style{Background: th.Background(), BorderColor: th.BorderSoft()}
		},
		Children: []core.Node{core.Text{Content: e.label, Style: theme.TypeCaption.Merge(core.Style{FontWeight: ui.Medium}),
			StyleFunc: func() core.Style { return core.Style{Color: th.Text()} }}},
	}
	mini := core.View{
		Style: core.Style{Width: core.Pt(core.Points(browse.MiniSize)), Height: core.Pt(core.Points(browse.MiniSize)),
			CornerRadius: core.Points(browse.MiniSize) / 2, BorderWidth: 1, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter},
		StyleFunc: func() core.Style {
			if on() {
				return core.Style{Background: th.Accent(), BorderColor: th.Accent()}
			}
			return core.Style{Background: th.Background(), BorderColor: th.Border()}
		},
		Children: []core.Node{ui.Icon(e.icon, 20, func() core.Color {
			if on() {
				return th.OnAccent()
			}
			return th.Text()
		})},
	}
	kids := []core.Node{label, mini}
	just := core.JustifyEnd
	if left {
		kids = []core.Node{mini, label}
		just = core.JustifyStart
	}
	return press(e.label, func() {
		a.fabOpen.Set(false)
		e.run()
	}, core.Style{Direction: core.Row, AlignItems: core.AlignCenter, JustifyContent: just, Gap: 10, Width: core.Pt(core.Points(width)),
		Height: core.Pt(core.Points(browse.MiniSize))}, nil, kids...)
}
