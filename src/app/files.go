package app

import (
	"hash/fnv"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/HW618/openlist-client/src/internal/browse"
	"github.com/HW618/openlist-client/src/internal/openlist"
	"github.com/HW618/openlist-client/src/internal/store"
	"github.com/HW618/openlist-client/src/locales"
	"github.com/HW618/openlist-client/src/theme"
)

// kindIcon is the glyph of a kind of file.
func kindIcon(k openlist.Kind) func() *core.SVG {
	switch k {
	case openlist.KindFolder:
		return lucide.Folder
	case openlist.KindVideo:
		return lucide.Film
	case openlist.KindAudio:
		return lucide.Music
	case openlist.KindImage:
		return lucide.Image
	case openlist.KindDoc:
		return lucide.FileText
	case openlist.KindArchive:
		return lucide.Archive
	}
	return lucide.File
}

// filesPage is the first tab: the files of the current site.
func (a *App) filesPage(cx *core.Scope) core.Node {
	body := core.View{Style: fillStyle, Children: []core.Node{a.filesBody(cx)}}
	return core.View{
		Style:    core.Style{FlexGrow: 1, FlexShrink: 1, MinHeight: core.Pt(0), Position: core.Relative},
		OnLayout: func(b core.Bounds) { a.fabBox.Set(b.Size) },
		Children: []core.Node{
			page(cx,
				core.Show{When: a.selecting.Get,
					Then: func(cx *core.Scope) core.Node { return a.selectionBar(cx) },
					Else: func(cx *core.Scope) core.Node { return a.siteHeader(cx) }},
				a.searchRow(cx),
				a.toolbar(cx),
				a.metaLine(cx),
				body,
				core.Show{When: a.selecting.Get, Then: func(cx *core.Scope) core.Node { return a.batchBar(cx) }},
			),
			core.Show{When: func() bool { return !a.selecting.Get() && a.hasSite() }, Then: func(cx *core.Scope) core.Node { return a.fab(cx) }},
		},
	}
}

func (a *App) hasSite() bool { _, ok := a.site(); return ok }

// siteHeader is the site pill and the buttons beside it.
func (a *App) siteHeader(cx *core.Scope) core.Node {
	th := theme.Of(cx)
	name := func() string {
		if s, ok := a.site(); ok {
			return s.Name
		}
		return locales.NoSiteName(cx)
	}
	pill := core.View{
		Role: core.RoleButton, LabelFunc: func() string { return locales.SiteSwitchLabel(cx) + ": " + name() }, TestID: "site-pill",
		OnPress: func(core.PressEvent) {
			if a.hasSite() {
				a.openSheet(sheetState{Kind: sheetSites})
			} else {
				a.openSheet(sheetState{Kind: sheetAddSite})
			}
		},
		Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, Padding: core.Symmetric(6, 12), MinHeight: core.Pt(36),
			CornerRadius: theme.RadiusPill, BorderWidth: 1, MinWidth: core.Pt(0), FlexShrink: 1},
		StyleFunc: func() core.Style { return core.Style{Background: th.Surface(), BorderColor: th.Border()} },
		Children: []core.Node{
			icon(cx, lucide.Server, 16, cAccent),
			core.View{Style: core.Style{MinWidth: core.Pt(0), FlexShrink: 1}, Children: []core.Node{
				oneLine(txtf(cx, name, theme.TypeSmall.Merge(core.Style{FontWeight: ui.Semibold}), cText)),
			}},
			icon(cx, lucide.ChevronDown, 16, cMuted),
		},
	}
	addSiteBtn := core.View{
		Role: core.RoleButton, Label: locales.AddSiteAction(cx), TestID: "header-add-site",
		OnPress:   func(core.PressEvent) { a.openSheet(sheetState{Kind: sheetAddSite}) },
		Style:     core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Padding: core.Symmetric(6, 10), MinHeight: core.Pt(36), CornerRadius: theme.RadiusPill, BorderWidth: 1, FlexShrink: 0},
		StyleFunc: func() core.Style { return core.Style{Background: th.Surface(), BorderColor: th.Border()} },
		Children: []core.Node{
			oneLine(txt(cx, locales.HeaderAddSite(cx), theme.TypeSmall.Merge(core.Style{FontWeight: ui.Semibold}), cAccent)),
		},
	}
	siteGroup := core.View{
		Style:    core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 6, MinWidth: core.Pt(0), FlexShrink: 1},
		Children: []core.Node{pill, addSiteBtn},
	}
	return header(cx,
		siteGroup, spacer(),
		iconButton(lucide.RefreshCw, locales.RefreshDirLabel(cx), func() {
			if a.hasSite() {
				a.refresh(true)
			}
		}),
		iconButton(lucide.Plus, locales.NewOrUploadLabel(cx), func() {
			if a.hasSite() {
				a.openSheet(sheetState{Kind: sheetQuick})
			} else {
				a.openSheet(sheetState{Kind: sheetAddSite})
			}
		}))
}

// selectionBar replaces the header while files are being chosen.
func (a *App) selectionBar(cx *core.Scope) core.Node {
	allSelected := func() bool {
		items := a.items.Get()
		if len(items) == 0 {
			return false
		}
		sel := a.selected.Get()
		for _, it := range items {
			if !sel[it.Path] {
				return false
			}
		}
		return true
	}
	selectedCount := func() int { return len(a.selectedItems()) }
	return header(cx,
		ui.Button{Text: locales.SelectDone(cx), Variant: ui.ButtonSecondary, OnPress: a.endSelect, TestID: "select-done"},
		core.View{Style: fillStyle, Children: []core.Node{
			txtf(cx, func() string { return locales.SelectedCount(cx, selectedCount()) },
				theme.TypeBody.Merge(core.Style{TextAlign: core.TextAlignCenter}), cText)}},
		textButton(cx, func() string {
			if allSelected() {
				return locales.DeselectAll(cx)
			}
			return locales.SelectAll(cx)
		}, func() { a.selectAll(!allSelected()) }))
}

// searchRow is the search field and the choice of where it looks.
func (a *App) searchRow(cx *core.Scope) core.Node {
	input := ui.Input{
		Value: a.query.Get, Placeholder: locales.SearchPlaceholder(cx), Label: locales.SearchLabel(cx), TestID: "search",
		OnChange: func(s string) {
			a.query.Set(s)
			if strings.TrimSpace(s) == "" {
				a.commitGlobal()
			} else {
				a.debounceGlobal()
			}
		},
		OnSubmit:      func(string) { a.commitGlobal() },
		NoAutoCorrect: true,
		Style:         core.Style{FlexGrow: 1, MinWidth: core.Pt(0)},
	}
	clear := core.Show{When: func() bool { return a.query.Get() != "" }, Then: func(cx *core.Scope) core.Node {
		return iconButton(lucide.X, locales.ClearSearchLabel(cx), func() {
			a.query.Set("")
			a.commitGlobal()
		})
	}}
	scope := core.View{Role: core.RoleTab, Label: locales.ScopeLabel(cx), Style: core.Style{Direction: core.Row, Gap: 8}, Children: []core.Node{
		chip(cx, locales.ScopeCurrent(cx), func() bool { return !a.global.Get() }, func() { a.setGlobal(false) }),
		chip(cx, locales.ScopeGlobal(cx), a.global.Get, func() { a.setGlobal(true) }),
	}}
	return core.View{Style: core.Style{Gap: 8, Padding: core.Symmetric(4, theme.Space4)}, Children: []core.Node{
		row(4, icon(cx, lucide.Search, 18, cMuted), input, clear),
		scope,
	}}
}

// setGlobal chooses whether a search looks on the whole server or below the
// folder shown.
func (a *App) setGlobal(on bool) {
	a.cx.Batch(func() {
		a.global.Set(on)
		a.commitGlobal()
	})
}

// debounceGlobal searches below the folder, or the server, once typing
// pauses.
func (a *App) debounceGlobal() {
	if a.stopQuery != nil {
		a.stopQuery()
	}
	a.stopQuery = a.cx.AfterFunc(300*time.Millisecond, a.commitGlobal)
}

func (a *App) commitGlobal() {
	if a.stopQuery != nil {
		a.stopQuery()
		a.stopQuery = nil
	}
	a.globalText.Set(strings.TrimSpace(a.query.GetUntracked()))
}

// toolbar is the path and the buttons that sort and lay out the files.
func (a *App) toolbar(cx *core.Scope) core.Node {
	th := theme.Of(cx)
	crumbs := core.Scroll{Horizontal: true, HideIndicator: true, Style: core.Style{FlexGrow: 1, FlexShrink: 1, MinWidth: core.Pt(0)},
		Children: []core.Node{
			core.View{Role: core.RoleGroup, Label: locales.BreadcrumbLabel(cx), Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 2},
				Children: []core.Node{core.Each(
					func() []browse.Crumb { return browse.Crumbs(a.path.Get()) },
					func(c browse.Crumb) string { return c.Path },
					func(cx *core.Scope, c func() browse.Crumb, index func() int) core.Node {
						cr := c()
						last := func() bool { return cr.Path == a.path.Get() }
						name := cr.Name
						if name == "" {
							name = locales.RootName(cx)
						}
						label := core.Text{Content: name, Style: theme.TypeSmall,
							StyleFunc: func() core.Style {
								if last() {
									return core.Style{Color: th.Text(), FontWeight: ui.Semibold}
								}
								return core.Style{Color: th.Accent()}
							}}
						var sep core.Node = core.View{}
						if cr.Path != "/" {
							sep = icon(cx, lucide.ChevronRight, 14, cFaint)
						}
						return row(2, sep, press(name, func() { a.goTo(cr.Path) },
							core.Style{Padding: core.Symmetric(6, 6), MinHeight: core.Pt(32), AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter},
							nil, label))
					})}}}}
	atRoot := func() bool { return !a.hasSite() || a.path.Get() == "/" }
	upBtn := ui.IconButton{
		Icon:     lucide.CornerLeftUp,
		Label:    locales.ParentDirLabel(cx),
		TestID:   "parent-dir",
		Disabled: atRoot,
		Size:     32,
		IconSize: 16,
		OnPress: func() {
			if !atRoot() {
				a.goUp()
			}
		},
	}
	sortName := func() string {
		switch a.sortKey.Get() {
		case store.SortSize:
			return locales.SortSize(cx)
		case store.SortDate:
			return locales.SortDate(cx)
		}
		return locales.SortName(cx)
	}
	sortBtn := press(locales.SortToggleLabel(cx), func() { a.sortKey.Update(store.SortKey.Next) },
		core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4, Padding: core.Symmetric(6, 8), MinHeight: core.Pt(32),
			CornerRadius: theme.RadiusSM}, nil,
		icon(cx, lucide.ArrowUpDown, 14, cMuted), txtf(cx, sortName, theme.TypeCaption.Merge(core.Style{FontWeight: ui.Medium}), cSecond))
	sortBtn.TestID = "sort"
	dir := ui.IconButton{Icon: func() *core.SVG {
		if a.sortDesc.Get() {
			return lucide.ChevronDown()
		}
		return lucide.ChevronUp()
	}, Label: locales.SortToggleLabel(cx), OnPress: func() { a.sortDesc.Update(func(b bool) bool { return !b }) }, Size: 32, IconSize: 16}
	views := ui.IconButton{Icon: func() *core.SVG {
		if a.view.Get() == store.ViewGrid {
			return lucide.List()
		}
		return lucide.LayoutGrid()
	}, Label: locales.ViewToggleLabel(cx), TestID: "view-toggle", Size: 32, IconSize: 18, OnPress: func() {
		a.view.Update(func(v store.ViewMode) store.ViewMode {
			if v == store.ViewGrid {
				return store.ViewList
			}
			return store.ViewGrid
		})
	}}
	return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4, Padding: core.Symmetric(0, theme.Space4)},
		Children: []core.Node{crumbs, upBtn, sortBtn, dir, views}}
}

// metaLine says how many items there are and which driver serves them.
func (a *App) metaLine(cx *core.Scope) core.Node {
	text := func() string {
		if !a.hasSite() {
			return ""
		}
		s := locales.ItemsCount(cx, len(a.items.Get()))
		if a.searching() {
			if a.found.Loading() {
				return s + " · " + locales.Searching(cx)
			}
			return s
		}
		if r, ok := a.listing.Get(); ok && r.Dir == a.path.Get() && r.Provider != "" {
			return s + " · " + locales.DriverLabel(cx, r.Provider)
		}
		if a.path.Get() == "/" {
			return s + " · " + locales.MultiStorage(cx)
		}
		return s
	}
	return core.View{Style: core.Style{Padding: core.Symmetric(4, theme.Space4)}, Children: []core.Node{
		oneLine(txtf(cx, text, theme.TypeCaption, cMuted)),
	}}
}

// bodyKind is what the middle of the Files page shows.
func (a *App) bodyKind() string {
	if !a.hasSite() {
		return "nosite"
	}
	n := len(a.items.Get())
	if q := strings.TrimSpace(a.query.Get()); q != "" {
		committed := strings.TrimSpace(a.globalText.Get()) == q
		switch {
		case n == 0 && (!committed || a.found.Loading()):
			return "loading"
		case n == 0 && a.found.Err() != nil:
			return "error"
		case n == 0:
			return "nosearch"
		}
	} else {
		switch {
		case n == 0 && a.listing.Loading():
			return "loading"
		case n == 0 && a.listing.Err() != nil:
			return "error"
		case n == 0:
			return "empty"
		}
	}
	if a.view.Get() == store.ViewGrid {
		return "grid"
	}
	return "list"
}

// filesBody shows the files, or why there are none.
func (a *App) filesBody(cx *core.Scope) core.Node {
	kind := core.NewMemo(cx, a.bodyKind)
	is := func(k string) func() bool { return func() bool { return kind.Get() == k } }
	show := func(k string, build func(cx *core.Scope) core.Node) core.Node {
		return core.Show{When: is(k), Then: build}
	}
	return core.View{Style: fillStyle, Children: []core.Node{
		show("nosite", func(cx *core.Scope) core.Node {
			return emptyState(cx, lucide.Cloud, locales.NoSitesTitle(cx), locales.NoSitesDesc(cx),
				ui.Button{Text: locales.AddSiteAction(cx), Icon: lucide.Plus, OnPress: func() { a.openSheet(sheetState{Kind: sheetAddSite}) }, TestID: "add-site"})
		}),
		show("loading", func(cx *core.Scope) core.Node { return loadingView(cx, locales.Loading(cx)) }),
		show("error", func(cx *core.Scope) core.Node { return a.errorView(cx) }),
		show("empty", func(cx *core.Scope) core.Node {
			return emptyState(cx, lucide.FolderOpen, locales.EmptyDirTitle(cx), locales.EmptyDirDesc(cx), nil)
		}),
		show("nosearch", func(cx *core.Scope) core.Node {
			return emptyState(cx, lucide.Search, locales.EmptySearchTitle(cx), locales.EmptySearchDesc(cx), nil)
		}),
		show("list", func(cx *core.Scope) core.Node { return a.fileList(cx) }),
		show("grid", func(cx *core.Scope) core.Node { return a.fileGrid(cx) }),
	}}
}

// errorView says a folder could not be read, with what to do about it.
func (a *App) errorView(cx *core.Scope) core.Node {
	err := func() error {
		if a.searching() {
			return a.found.Err()
		}
		return a.listing.Err()
	}
	desc := func() string {
		e := err()
		if e == nil {
			return ""
		}
		if openlist.Unauthorized(e) {
			return locales.NeedLoginDesc(cx)
		}
		return errText(e)
	}
	retry := ui.Button{Text: locales.Retry(cx), Variant: ui.ButtonSecondary, OnPress: func() {
		if strings.TrimSpace(a.query.GetUntracked()) != "" {
			a.found.Refetch()
		} else {
			a.listing.Refetch()
		}
	}, TestID: "retry"}
	login := core.Show{When: func() bool { e := err(); return e != nil && openlist.Unauthorized(e) }, Then: func(cx *core.Scope) core.Node {
		return ui.Button{Text: locales.SignInAction(cx), OnPress: func() { a.openSheet(sheetState{Kind: sheetSignIn}) }}
	}}
	return emptyStateFunc(cx, lucide.CircleAlert, locales.LoadFailed(cx), desc, buttons(retry, login))
}

// itemKey names an item together with what is shown of it, so a row is built
// again when the item changes.
func itemKey(it browse.Item) string {
	dir := "0"
	if it.IsDir {
		dir = "1"
	}
	return it.Path + "|" + dir + "|" + strconv.Itoa(int(it.Kind)) + "|" +
		strconv.FormatInt(it.Size, 10) + "|" + strconv.FormatInt(it.Modified.Unix(), 10) + "|" +
		it.Thumb + "|" + it.Sign
}

// fileList is the files as rows, built as they come into view. The list is
// built again whenever the items change, which a list does not notice by
// itself when their number stays the same.
func (a *App) fileList(cx *core.Scope) core.Node {
	sum := core.NewMemo(cx, func() uint64 {
		h := fnv.New64a()
		for _, it := range a.items.Get() {
			_, _ = h.Write([]byte(itemKey(it)))
			_, _ = h.Write([]byte{0})
		}
		return h.Sum64()
	})
	return core.View{Style: fillStyle, Children: []core.Node{core.Each(
		func() []uint64 { return []uint64{sum.Get()} },
		func(v uint64) uint64 { return v },
		func(cx *core.Scope, _ func() uint64, _ func() int) core.Node { return a.rows(cx) })}}
}

// rows is the list of rows itself.
func (a *App) rows(cx *core.Scope) core.Node {
	return core.List{
		Style: fillStyle, TestID: "file-list", EstimatedSize: 64, Gap: 8,
		Count: func() int { return len(a.items.Get()) },
		Key: func(i int) any {
			items := a.items.GetUntracked()
			if i < len(items) {
				return items[i].Path
			}
			return i
		},
		Header:     core.View{Style: core.Style{Height: core.Pt(4)}},
		Footer:     core.View{Style: core.Style{Height: core.Pt(110)}},
		OnRefresh:  func() { a.refresh(false) },
		Refreshing: a.listing.Loading,
		Row: func(cx *core.Scope, r core.ListRow) core.Node {
			items := a.items.GetUntracked()
			i := r.Index()
			if i >= len(items) {
				return core.View{}
			}
			return a.fileRow(cx, items[i])
		},
	}
}

// checkMark is the round box of a selected row.
func checkMark(cx *core.Scope, on func() bool) core.Node {
	th := theme.Of(cx)
	return core.View{
		Style: core.Style{Width: core.Pt(22), Height: core.Pt(22), CornerRadius: 11, BorderWidth: 2, AlignItems: core.AlignCenter,
			JustifyContent: core.JustifyCenter, FlexShrink: 0},
		StyleFunc: func() core.Style {
			if on() {
				return core.Style{Background: th.Accent(), BorderColor: th.Accent()}
			}
			return core.Style{BorderColor: th.Border()}
		},
		Children: []core.Node{core.Show{When: on, Then: func(cx *core.Scope) core.Node { return icon(cx, lucide.Check, 14, cOnAccent) }}},
	}
}

// itemTile is the picture of an item: its thumbnail when it has one, else the
// glyph of its kind.
func itemTile(cx *core.Scope, it browse.Item, size core.Points) core.Node {
	glyph := tile(cx, kindIcon(it.Kind), browse.KindName(it.Kind), size, 10)
	if it.Thumb == "" || (it.Kind != openlist.KindImage && it.Kind != openlist.KindVideo) {
		return glyph
	}
	return core.View{Style: core.Style{Width: core.Pt(size), Height: core.Pt(size), CornerRadius: 10, Overflow: core.Hidden, FlexShrink: 0},
		Children: []core.Node{core.Image{Source: core.Src(it.Thumb), ResizeMode: core.Cover, Label: it.Name,
			Style: core.Style{Width: core.Pct(100), Height: core.Pct(100)}}}}
}

// itemPress opens an item, or chooses it while files are being chosen.
func (a *App) itemPress(it browse.Item) {
	switch {
	case a.selecting.GetUntracked():
		a.toggleSelect(it.Path)
	case it.IsDir:
		a.goTo(it.Path)
	default:
		a.openPreview(it)
	}
}

// itemLong starts choosing files with the one held, or chooses another.
func (a *App) itemLong(it browse.Item) {
	if a.selecting.GetUntracked() {
		a.toggleSelect(it.Path)
		return
	}
	a.beginSelect(it.Path)
}

// fileRow is one file in the list.
func (a *App) fileRow(cx *core.Scope, it browse.Item) core.Node {
	th := theme.Of(cx)
	on := func() bool { return a.selected.Get()[it.Path] }
	more := core.Show{When: func() bool { return !a.selecting.Get() }, Then: func(cx *core.Scope) core.Node {
		return iconButton(lucide.Ellipsis, locales.MoreActionsLabel(cx, it.Name), func() { a.openSheet(sheetState{Kind: sheetActions, Item: it}) })
	}}
	pick := core.Show{When: a.selecting.Get, Then: func(cx *core.Scope) core.Node { return checkMark(cx, on) }}
	return core.View{
		Role: core.RoleButton, Label: it.Name, Selected: on, DelayLongPress: 450 * time.Millisecond, TestID: "file-" + it.Name,
		OnPress:     func(core.PressEvent) { a.itemPress(it) },
		OnLongPress: func(core.PressEvent) { a.itemLong(it) },
		Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 12, Padding: core.Edges{Top: 8, Bottom: 8, Left: 12, Right: 4},
			Margin: core.Edges{Left: theme.Space4, Right: theme.Space4}, MinHeight: core.Pt(theme.Touch + 16), BorderWidth: 1,
			CornerRadius: theme.RadiusMD, BorderCurve: core.BorderCurveContinuous},
		StyleFunc: func() core.Style {
			if on() {
				return core.Style{Background: ui.Alpha(th.Accent(), 0.10), BorderColor: th.Accent()}
			}
			return core.Style{Background: th.SurfaceWarm(), BorderColor: th.BorderSoft()}
		},
		Children: []core.Node{
			pick,
			itemTile(cx, it, 40),
			core.View{Style: fillStyle.Merge(core.Style{Gap: 2}), Children: []core.Node{
				oneLine(txt(cx, it.Name, theme.TypeBody, cText)),
				oneLine(txtf(cx, func() string {
					meta := browse.Meta(it, locales.FolderKind(cx))
					if a.searching() {
						return path.Dir(it.Path) + " · " + meta
					}
					return meta
				}, theme.TypeCaption, cMuted)),
			}},
			more,
		},
	}
}

// columnsFor is how many grid columns fit a width.
func columnsFor(w core.Points) int {
	n := int((w - 2*theme.Space4 + 12) / (150 + 12))
	return max(2, n)
}

// fileGrid is the files as tiles.
func (a *App) fileGrid(cx *core.Scope) core.Node {
	th := theme.Of(cx)
	width := func() core.Points {
		w := a.fabBox.Get().Width
		if w > theme.ContentMax {
			w = theme.ContentMax
		}
		if w <= 0 {
			w = 360
		}
		return w
	}
	tileWidth := func() core.Points {
		w := width()
		cols := columnsFor(w)
		return (w - 2*theme.Space4 - 12*core.Points(cols-1)) / core.Points(cols)
	}
	return core.Scroll{Style: fillStyle, Children: []core.Node{
		core.View{Style: core.Style{Direction: core.Row, Wrap: core.Wrap, Gap: 12, Padding: core.Edges{Left: theme.Space4, Right: theme.Space4, Top: 4, Bottom: 110}},
			Children: []core.Node{core.Each(a.items.Get, itemKey,
				func(cx *core.Scope, item func() browse.Item, _ func() int) core.Node {
					it := item()
					on := func() bool { return a.selected.Get()[it.Path] }
					return core.View{
						Role: core.RoleButton, Label: it.Name, Selected: on, DelayLongPress: 450 * time.Millisecond, TestID: "file-" + it.Name,
						OnPress:     func(core.PressEvent) { a.itemPress(it) },
						OnLongPress: func(core.PressEvent) { a.itemLong(it) },
						Style: core.Style{Padding: core.All(10), Gap: 8, AlignItems: core.AlignCenter, BorderWidth: 1, CornerRadius: theme.RadiusMD,
							BorderCurve: core.BorderCurveContinuous},
						StyleFunc: func() core.Style {
							s := core.Style{Width: core.Pt(tileWidth())}
							if on() {
								return s.Merge(core.Style{Background: ui.Alpha(th.Accent(), 0.10), BorderColor: th.Accent()})
							}
							return s.Merge(core.Style{Background: th.SurfaceWarm(), BorderColor: th.BorderSoft()})
						},
						Children: []core.Node{
							core.View{Style: core.Style{AlignSelf: core.AlignStretch, Direction: core.Row, JustifyContent: core.JustifyCenter},
								Children: []core.Node{itemTile(cx, it, 56)}},
							core.View{Style: core.Style{AlignSelf: core.AlignStretch, Gap: 2}, Children: []core.Node{
								core.Text{Content: it.Name, MaxLines: 2, EllipsizeMode: core.EllipsizeTail,
									Style:     theme.TypeSmall.Merge(core.Style{FontWeight: ui.Medium, TextAlign: core.TextAlignCenter}),
									StyleFunc: func() core.Style { return core.Style{Color: th.Text()} }},
								oneLine(txt(cx, browse.ShortMeta(it, locales.FolderKind(cx)), theme.TypeCaption.Merge(core.Style{TextAlign: core.TextAlignCenter}), cMuted)),
							}},
						},
					}
				})}},
	}}
}

// batchBar is the actions on the chosen files, along the bottom.
func (a *App) batchBar(cx *core.Scope) core.Node {
	th := theme.Of(cx)
	none := func() bool { return len(a.selectedItems()) == 0 }
	action := func(id string, svg func() *core.SVG, label string, danger bool, run func([]browse.Item)) core.Node {
		color := cText
		if danger {
			color = cDanger
		}
		return core.View{
			Role: core.RoleButton, Label: label, Disabled: none, TestID: "batch-" + id,
			OnPress: func(core.PressEvent) {
				if items := a.selectedItems(); len(items) > 0 {
					run(items)
				}
			},
			Style: core.Style{FlexGrow: 1, FlexBasis: core.Pt(0), MinWidth: core.Pt(0), AlignItems: core.AlignCenter, Gap: 3, Padding: core.Symmetric(8, 4),
				MinHeight: core.Pt(theme.Touch), CornerRadius: theme.RadiusMD, Disabled: &core.Style{Opacity: core.Alpha(0.4)}},
			Children: []core.Node{icon(cx, svg, 20, color), oneLine(txt(cx, label, theme.TypeTab, color))},
		}
	}
	return core.View{Style: core.Style{Direction: core.Row, Gap: 4, Padding: core.Symmetric(6, 8), BorderWidth: 0, Borders: core.Edges{Top: 1},
		PaddingEnv: core.EnvEdges{Left: core.EnvSafeArea, Right: core.EnvSafeArea}},
		StyleFunc: func() core.Style {
			return core.Style{Background: th.Surface(), BorderColors: core.EdgeColors{Top: th.BorderSoft()}}
		},
		Role: core.RoleToolbar, Label: locales.BatchLabel(cx),
		Children: []core.Node{
			action("download", lucide.Download, locales.BatchDownload(cx), false, a.download),
			action("links", lucide.Link, locales.BatchCopyLink(cx), false, a.copyLinks),
			action("move", lucide.FolderInput, locales.BatchMove(cx), false, a.startMove),
			action("delete", lucide.Trash, locales.BatchDelete(cx), true, func(items []browse.Item) {
				a.openSheet(sheetState{Kind: sheetDelete, Items: items})
			}),
		}}
}

// startMove asks for the folder to move the chosen files to.
func (a *App) startMove(items []browse.Item) {
	groups := byParent(items)
	exclude := ""
	if len(groups) == 1 {
		for dir := range groups {
			exclude = dir
		}
	}
	var trees []string
	for _, it := range items {
		if it.IsDir && !browse.IsMount(it) {
			trees = append(trees, it.Path)
		}
	}
	a.openPicker(&pickerSpec{
		Title: locales.MoveTitle(a.cx, len(items)), Start: a.path.GetUntracked(), ConfirmText: locales.MoveHere(a.cx),
		Exclude: exclude, ExcludeTrees: trees,
		ConfirmWith: func(dir string, done func(error)) { a.move(items, dir, done) },
	})
}

// openPicker opens the folder picker.
func (a *App) openPicker(p *pickerSpec) {
	a.cx.Batch(func() {
		a.pickPath.Set(openlist.Clean(p.Start))
		a.openSheet(sheetState{Kind: sheetPicker, Picker: p})
	})
}
