package app

import (
	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/HW618/openlist-client/src/locales"
	"github.com/HW618/openlist-client/src/theme"
)

// pick chooses a color of the theme where a style function runs.
type pick = func(theme.Colors) core.Color

func cText(c theme.Colors) core.Color       { return c.Text() }
func cSecond(c theme.Colors) core.Color     { return c.TextSecondary() }
func cMuted(c theme.Colors) core.Color      { return c.Muted() }
func cFaint(c theme.Colors) core.Color      { return c.Faint() }
func cAccent(c theme.Colors) core.Color     { return c.Accent() }
func cDanger(c theme.Colors) core.Color     { return c.Danger() }
func cOnAccent(c theme.Colors) core.Color   { return c.OnAccent() }
func cSurface(c theme.Colors) core.Color    { return c.Surface() }
func cWarm(c theme.Colors) core.Color       { return c.SurfaceWarm() }
func cBorder(c theme.Colors) core.Color     { return c.Border() }
func cSoft(c theme.Colors) core.Color       { return c.BorderSoft() }
func cBackground(c theme.Colors) core.Color { return c.Background() }

// txt is a line of text in a style and a color of the theme.
func txt(cx *core.Scope, content string, st core.Style, color pick) core.Text {
	th := theme.Of(cx)
	return core.Text{Content: content, Style: st, StyleFunc: func() core.Style { return core.Style{Color: color(th)} }}
}

// txtf is txt for text that changes.
func txtf(cx *core.Scope, content func() string, st core.Style, color pick) core.Text {
	th := theme.Of(cx)
	return core.Text{ContentFunc: content, Style: st, StyleFunc: func() core.Style { return core.Style{Color: color(th)} }}
}

// oneLine is text that is cut with an ellipsis rather than wrapped.
func oneLine(t core.Text) core.Text {
	t.MaxLines = 1
	t.EllipsizeMode = core.EllipsizeTail
	return t
}

// icon draws a glyph in a color of the theme.
func icon(cx *core.Scope, svg func() *core.SVG, size core.Points, color pick) core.Node {
	th := theme.Of(cx)
	return ui.Icon(svg, size, func() core.Color { return color(th) })
}

// press is a pressable view that screen readers know as a button.
func press(label string, onPress func(), st core.Style, sf func() core.Style, kids ...core.Node) core.View {
	return core.View{Role: core.RoleButton, Label: label, OnPress: func(core.PressEvent) { onPress() },
		Style: st, StyleFunc: sf, Children: kids}
}

// row is a horizontal box with its children centered across.
func row(gap core.Points, kids ...core.Node) core.View {
	return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: gap}, Children: kids}
}

// col is a vertical box.
func col(gap core.Points, kids ...core.Node) core.View {
	return core.View{Style: core.Style{Gap: gap}, Children: kids}
}

// spacer pushes what follows it to the end of a row.
func spacer() core.Node { return core.View{Style: core.Style{FlexGrow: 1}} }

// fillStyle is the style of a box that takes the room left.
var fillStyle = core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0), MinHeight: core.Pt(0)}

// cardStyle is the surface of a card: a warm fill with a soft hairline.
func cardStyle(cx *core.Scope, radius core.Points) func() core.Style {
	th := theme.Of(cx)
	return func() core.Style {
		return core.Style{Background: th.SurfaceWarm(), BorderColor: th.BorderSoft(), BorderWidth: 1, CornerRadius: radius,
			BorderCurve: core.BorderCurveContinuous}
	}
}

// card wraps children in a card.
func card(cx *core.Scope, st core.Style, kids ...core.Node) core.Node {
	return core.View{Style: st.Merge(core.Style{BorderWidth: 1, CornerRadius: theme.RadiusLG, BorderCurve: core.BorderCurveContinuous}),
		StyleFunc: cardStyle(cx, theme.RadiusLG), Children: kids}
}

// tile is a square of a tinted color with a glyph in it: the icon of a file
// kind, or of a setting.
func tile(cx *core.Scope, svg func() *core.SVG, kind string, size, radius core.Points) core.Node {
	th := theme.Of(cx)
	return core.View{
		Style: core.Style{Width: core.Pt(size), Height: core.Pt(size), CornerRadius: radius, AlignItems: core.AlignCenter,
			JustifyContent: core.JustifyCenter, FlexShrink: 0},
		StyleFunc: func() core.Style { return core.Style{Background: ui.Alpha(th.KindColor(kind), 0.13)} },
		Children:  []core.Node{ui.Icon(svg, size*0.5, func() core.Color { return th.KindColor(kind) })},
	}
}

// chip is a small pill that is on or off.
func chip(cx *core.Scope, text string, on func() bool, onPress func()) core.Node {
	return chipID(cx, "", text, on, onPress)
}

// chipID is chip with a test id.
func chipID(cx *core.Scope, id, text string, on func() bool, onPress func()) core.Node {
	th := theme.Of(cx)
	v := press(text, onPress,
		core.Style{Padding: core.Symmetric(6, 14), CornerRadius: theme.RadiusPill, MinHeight: core.Pt(32), AlignItems: core.AlignCenter,
			JustifyContent: core.JustifyCenter, BorderWidth: 1},
		func() core.Style {
			if on() {
				return core.Style{Background: ui.Alpha(th.Accent(), 0.14), BorderColor: th.Accent()}
			}
			return core.Style{Background: th.SurfaceWarm(), BorderColor: th.BorderSoft()}
		},
		core.Text{Content: text, Style: theme.TypeCaption.Merge(core.Style{FontWeight: ui.Medium}),
			StyleFunc: func() core.Style {
				if on() {
					return core.Style{Color: th.Accent()}
				}
				return core.Style{Color: th.TextSecondary()}
			}})
	v.Selected = on
	v.TestID = id
	return v
}

// emptyState is a centered icon, a title and a line of help, with an action.
func emptyState(cx *core.Scope, svg func() *core.SVG, title, desc string, action core.Node) core.Node {
	return emptyStateFunc(cx, svg, title, func() string { return desc }, action)
}

// emptyStateFunc is emptyState with a line of help that changes.
func emptyStateFunc(cx *core.Scope, svg func() *core.SVG, title string, desc func() string, action core.Node) core.Node {
	kids := []core.Node{
		core.View{Style: core.Style{Width: core.Pt(64), Height: core.Pt(64), CornerRadius: theme.RadiusLG, AlignItems: core.AlignCenter,
			JustifyContent: core.JustifyCenter}, StyleFunc: cardStyle(cx, theme.RadiusLG),
			Children: []core.Node{icon(cx, svg, 28, cFaint)}},
		txt(cx, title, theme.TypeSheet.Merge(core.Style{TextAlign: core.TextAlignCenter}), cText),
		txtf(cx, desc, theme.TypeSmall.Merge(core.Style{TextAlign: core.TextAlignCenter}), cMuted),
	}
	if action != nil {
		kids = append(kids, action)
	}
	return core.View{Style: core.Style{FlexGrow: 1, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Gap: 10,
		Padding: core.All(theme.Space6), MaxWidth: core.Pt(420), AlignSelf: core.AlignCenter}, Children: kids}
}

// loadingView is a spinner in the middle of the room left.
func loadingView(cx *core.Scope, label string) core.Node {
	return core.View{Style: core.Style{FlexGrow: 1, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Gap: 10},
		Children: []core.Node{ui.Spinner{Size: 24, Label: label}, txt(cx, label, theme.TypeSmall, cMuted)}}
}

// buttons is a row of buttons that share its width.
func buttons(kids ...core.Node) core.Node {
	return core.View{Style: core.Style{Direction: core.Row, Gap: 8}, Children: kids}
}

// page frames a tab's content: the room left, with the content kept to a
// readable width and centered on a wide window.
func page(cx *core.Scope, kids ...core.Node) core.Node {
	return core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), AlignItems: core.AlignCenter},
		Children: []core.Node{core.View{Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), Width: core.Pct(100),
			MaxWidth: core.Pt(theme.ContentMax)}, Children: kids}}}
}

// header is the strip at the top of a page.
func header(cx *core.Scope, kids ...core.Node) core.Node {
	return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, Padding: core.Symmetric(8, theme.Space4),
		MinHeight: core.Pt(52), PaddingEnv: core.EnvEdges{Top: core.EnvSafeArea}}, Children: kids}
}

// iconButton is a round ghost button with a glyph; label is what screen
// readers say.
func iconButton(svg func() *core.SVG, label string, onPress func()) core.Node {
	return ui.IconButton{Icon: svg, Label: label, OnPress: onPress, Size: theme.Touch}
}

// field is a labeled input with a line for what is wrong with it.
func field(cx *core.Scope, label string, in ui.Input, problem func() string) core.Node {
	th := theme.Of(cx)
	kids := []core.Node{
		core.Text{Content: label, Style: theme.TypeCaption.Merge(core.Style{FontWeight: ui.Semibold}),
			StyleFunc: func() core.Style { return core.Style{Color: th.TextSecondary()} }},
		in,
	}
	if problem != nil {
		kids = append(kids, core.Show{When: func() bool { return problem() != "" }, Then: func(cx *core.Scope) core.Node {
			return txtf(cx, problem, theme.TypeCaption, cDanger)
		}})
	}
	return core.View{Style: core.Style{Gap: 6}, Children: kids}
}

// optionRow is a row of settings: a title and a line of help on the left and a
// control on the right.
func optionRow(cx *core.Scope, title, desc string, control core.Node) core.Node {
	return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 12, Padding: core.Symmetric(12, 0)},
		Children: []core.Node{
			core.View{Style: fillStyle.Merge(core.Style{Gap: 2}), Children: []core.Node{
				txt(cx, title, theme.TypeBody, cText),
				txt(cx, desc, theme.TypeCaption, cMuted),
			}},
			control,
		}}
}

// hairline is a thin rule between rows.
func hairline(cx *core.Scope) core.Node {
	th := theme.Of(cx)
	return core.View{Style: core.Style{Height: core.Pt(1), AlignSelf: core.AlignStretch},
		StyleFunc: func() core.Style { return core.Style{Background: th.BorderSoft()} }}
}

// sheetFrame is a panel with a title, a body and a row of actions: a bottom
// sheet on a narrow window and a centered dialog on a wide one.
func (a *App) sheetFrame(title string, body []core.Node, footer []core.Node) core.Node {
	cx := a.cx
	wide := false
	cx.Untrack(func() { wide = cx.Env().Window().Width >= theme.DialogBreakpoint })
	open := func() bool { return true }
	// The body scrolls inside the room the window and software keyboard leave it.
	scrollMax := func() core.Points {
		env := cx.Env()
		win := env.Window()
		maxH := win.Height * 0.6
		if !wide {
			safe := env.SafeArea()
			bottom := max(env.KeyboardHeight(), safe.Bottom)
			if room := win.Height - safe.Top - bottom - 156; room < maxH {
				maxH = max(room, 96)
			}
		}
		return maxH
	}
	scrollRef := new(core.Ref)
	contentH := core.NewSignal(cx, core.Points(0))
	if !wide {
		// On iOS, keepFocusedClear can scroll the sheet body while the keyboard
		// begins to open, before EnvKeyboard lifts the sheet; return to the top
		// once the content fits in the room above the keyboard.
		cx.Effect(func() {
			kb := cx.Env().KeyboardHeight()
			ch := contentH.Get()
			mh := scrollMax()
			if kb > 0 && ch > 0 && ch <= mh {
				scrollRef.ScrollTo(core.Point{}, false)
			}
		})
	}
	scroll := core.Scroll{
		Ref:       scrollRef,
		Style:     core.Style{FlexShrink: 1, MinHeight: core.Pt(0), MinWidth: core.Pt(0)},
		StyleFunc: func() core.Style { return core.Style{MaxHeight: core.Pt(scrollMax())} },
		BehaviorFunc: func() core.ScrollBehavior {
			ch := contentH.Get()
			return core.ScrollBehavior{
				KeyboardDismissMode:       core.KeyboardDismissInteractive,
				KeyboardShouldPersistTaps: core.KeyboardTapsHandled,
				NoScroll:                  ch > 0 && ch <= scrollMax(),
			}
		},
		OnScroll: func(e core.ScrollEvent) {
			if wide || e.Stale || e.Offset.Y == 0 {
				return
			}
			ch := contentH.GetUntracked()
			var mh core.Points
			cx.Untrack(func() { mh = scrollMax() })
			if ch > 0 && ch <= mh {
				scrollRef.ScrollTo(core.Point{}, false)
			}
		},
		Children: []core.Node{core.View{
			Style:    core.Style{Gap: 14},
			OnLayout: func(b core.Bounds) { contentH.Set(b.Size.Height) },
			Children: body,
		}},
	}
	if wide {
		pad := core.View{Style: core.Style{PaddingEnv: core.EnvEdges{Bottom: core.EnvKeyboard}}, Children: []core.Node{scroll}}
		return ui.Dialog{Open: open, Title: title, OnClose: a.closeSheet,
			OnOpenChange: func(o bool, _ base.Reason) {
				if !o {
					a.closeSheet()
				}
			},
			Children: []core.Node{pad}, Footer: footer, Style: core.Style{MaxWidth: core.Pt(480), Width: core.Pct(90)}}
	}
	dismiss := func(core.PressEvent) { cx.DismissKeyboard() }
	head := core.View{
		OnPress: dismiss, NoFocus: true, ImportantForAccessibility: core.ImportantNo,
		Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8},
		Children: []core.Node{
			core.View{Style: fillStyle, Children: []core.Node{txt(cx, title, theme.TypeSheet, cText)}},
			iconButton(lucide.X, locales.CloseSheetLabel(cx), a.closeSheet),
		},
	}
	kids := []core.Node{head, scroll}
	if len(footer) > 0 {
		kids = append(kids, buttons(footer...))
	}
	wrap := core.View{
		OnPress: dismiss, NoFocus: true, ImportantForAccessibility: core.ImportantNo,
		Style:   core.Style{Gap: 12},
		StyleFunc: func() core.Style {
			safeBottom := cx.Env().SafeArea().Bottom
			return core.Style{
				Padding:    core.Edges{Bottom: -safeBottom},
				PaddingEnv: core.EnvEdges{Bottom: core.EnvKeyboard},
			}
		},
		Children: kids,
	}
	return ui.Sheet{Open: open, OnClose: a.closeSheet, Children: []core.Node{wrap}}
}

// textButton is words in the accent color that press, with no box around.
func textButton(cx *core.Scope, label func() string, onPress func()) core.Node {
	return textButtonID(cx, "", label, onPress)
}

// textButtonID is textButton with a test ID.
func textButtonID(cx *core.Scope, id string, label func() string, onPress func()) core.Node {
	return core.View{Role: core.RoleButton, LabelFunc: label, TestID: id, OnPress: func(core.PressEvent) { onPress() },
		Style:    core.Style{Padding: core.Symmetric(8, 10), MinHeight: core.Pt(theme.Touch), AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter},
		Children: []core.Node{txtf(cx, label, theme.TypeBody, cAccent)}}
}
