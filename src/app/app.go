// Command openlist-client is a lightweight cross-platform client for
// OpenList (https://github.com/OpenListTeam/OpenList), written with NSGo.
package app

import (
	"github.com/nsgo-dev/nsgo/base"
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/desktop"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/HW618/openlist-client/src/internal/store"
	"github.com/HW618/openlist-client/src/locales"
	"github.com/HW618/openlist-client/src/theme"
)

// Run starts the app in NSGo's app scope, setting up the desktop tray and
// close-to-tray behavior where desktop windows are supported.
func Run(appScope *core.Scope, setMain func(*core.Scope, func(*core.Scope) core.Node)) {
	runWith(appScope, nil, setMain)
}

func runWith(appScope *core.Scope, secrets store.Secrets, setMain func(*core.Scope, func(*core.Scope) core.Node)) {
	if setMain == nil {
		setMain = (*core.Scope).SetMainView
	}
	var current *App
	var attach func(*App, *core.Scope)
	if desktop.IsAvailable(appScope) {
		attach = setupDesktopApp(appScope, func() *App { return current })
	}
	setMain(appScope, func(cx *core.Scope) core.Node {
		sec := secrets
		if sec == nil {
			sec = newSecrets(cx)
		}
		node, a := buildRoot(cx, sec)
		current = a
		if attach != nil {
			attach(a, cx)
		}
		return node
	})
}

// Root is the app's window: three tabs, the panels over them, the preview and
// the toasts.
func Root(cx *core.Scope) core.Node { return rootWith(cx, newSecrets(cx)) }

// rootWith is Root with the place tokens are kept given, for tests.
func rootWith(cx *core.Scope, secrets store.Secrets) core.Node {
	node, _ := buildRoot(cx, secrets)
	return node
}

func buildRoot(cx *core.Scope, secrets store.Secrets) (core.Node, *App) {
	cx.SetTheme(theme.Load())
	bars := ui.ScrollbarTheme(cx)
	bars.Mode = base.ScrollbarAlways
	base.SetScrollbarTheme(cx, bars)
	app := newApp(cx, secrets)
	if startupChecks {
		cx.Post(func() {
			// Guide notifications before other access checks so the Allow
			// button is a clear first-launch step (and a user gesture on web).
			if notifyGuide {
				app.guideNotifyPermission()
			}
			app.checkAccess(false)
		})
	}
	th := theme.Of(cx)
	return core.View{
		Style:     core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), Position: core.Relative},
		StyleFunc: func() core.Style { return core.Style{Background: th.Background(), Color: th.Text()} },
		Children: []core.Node{
			app.shell(cx),
			app.previewLayer(cx),
			app.sheetHost(cx),
			ui.Toasts{Toaster: app.toaster, Placement: ui.ToastBottom, Inset: 84, CloseLabel: locales.CloseSheetLabel(cx), TestID: "toasts"},
		},
	}, app
}

// tabEntry is one tab of the bar.
type tabEntry struct {
	text   string
	icon   func() *core.SVG
	testID string
	badge  func() int
	body   func(cx *core.Scope) core.Node
}

// shell is the tab pages with the bar below them. The bar is the one
// ui.TabShell draws, with a count over the transfers tab.
func (a *App) shell(cx *core.Scope) core.Node {
	th := theme.Of(cx)
	tabs := []tabEntry{
		{locales.TabFiles(cx), lucide.Files, "tab-files", nil, a.filesPage},
		{locales.TabTransfers(cx), lucide.ArrowLeftRight, "tab-transfers", func() int { return running(a.tasks.Get()) }, a.transfersPage},
		{locales.TabSettings(cx), lucide.Settings, "tab-settings", nil, a.settingsPage},
	}
	var sections, items []core.Node
	for i, t := range tabs {
		i, t := i, t
		seen := false
		sections = append(sections, core.View{TestID: t.testID + "-page", Children: []core.Node{core.Show{
			When: func() bool { seen = seen || a.tab.Get() == i; return seen },
			Then: t.body,
		}}, StyleFunc: func() core.Style {
			s := ui.FillRest
			if a.tab.Get() != i {
				s.Display = core.DisplayNone
			}
			return s
		}})

		var badge core.Node = core.View{}
		if t.badge != nil {
			count := t.badge
			badge = core.Show{When: func() bool { return count() > 0 }, Then: func(cx *core.Scope) core.Node {
				return core.View{
					Style: core.Style{Position: core.Absolute, Top: core.Pt(0), Left: core.Pct(56), MinWidth: core.Pt(16), Height: core.Pt(16), Padding: core.Symmetric(0, 4),
						CornerRadius: 8, AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter},
					StyleFunc: func() core.Style { return core.Style{Background: th.Danger()} },
					Children: []core.Node{core.Text{ContentFunc: func() string { return itoa(count()) }, Style: core.Style{FontSize: 10, LineHeight: 12, FontWeight: ui.Semibold},
						StyleFunc: func() core.Style { return core.Style{Color: th.OnAccent()} }}},
				}
			}}
		}
		items = append(items, base.Tab{
			Label: t.text, Selected: func() bool { return a.tab.Get() == i },
			OnPress: func(core.PressEvent) { a.selectTab(i) }, TestID: t.testID,
			Style: core.Style{FlexGrow: 1, FlexBasis: core.Pt(0), MinWidth: core.Pt(0), AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter, Gap: 2,
				MinHeight: core.Pt(theme.Touch + 6), CornerRadius: theme.RadiusSM, Position: core.Relative},
			StyleFunc: func() core.Style {
				return core.Style{Color: th.Muted(), Selected: &core.Style{Color: th.Accent()}, FocusVisible: ui.FocusRing(cx, -2)}
			},
			Children: []core.Node{ui.Icon(t.icon, 22, ui.Inherited),
				core.Text{Content: t.text, Style: theme.TypeTab, MaxLines: 1}, badge},
		})
	}
	bar := core.View{
		Style: core.Style{Direction: core.Row, Padding: core.Symmetric(4, 8), Borders: core.Edges{Top: 1}, JustifyContent: core.JustifyCenter,
			PaddingEnv: core.EnvEdges{Right: core.EnvSafeArea, Bottom: core.EnvSafeArea, Left: core.EnvSafeArea}},
		StyleFunc: func() core.Style {
			return core.Style{Background: th.Surface(), BorderColors: core.EdgeColors{Top: th.BorderSoft()}}
		},
		Children: []core.Node{base.Tabs{Label: locales.TabLabel(cx), TestID: "tab-bar",
			Style: core.Style{Direction: core.Row, FlexGrow: 1, Gap: 4, MaxWidth: core.Pt(560)}, Children: items}},
	}
	return core.View{Style: ui.FillRest, Children: []core.Node{
		core.View{Style: ui.FillRest, Children: sections},
		bar,
	}}
}
