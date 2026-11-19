package app

import (
	"errors"
	"strings"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/HW618/openlist-client/src/internal/browse"
	"github.com/HW618/openlist-client/src/internal/openlist"
	"github.com/HW618/openlist-client/src/internal/store"
	"github.com/HW618/openlist-client/src/locales"
	"github.com/HW618/openlist-client/src/theme"
)

// sheetHost shows the open panel, if any.
func (a *App) sheetHost(cx *core.Scope) core.Node {
	kind := core.NewMemo(cx, func() sheetKind { return a.sheet.Get().Kind })
	at := func(k sheetKind, build func(cx *core.Scope, st sheetState) core.Node) core.Node {
		return core.Show{
			When: func() bool { return kind.Get() == k },
			Then: func(cx *core.Scope) core.Node { return build(cx, a.sheet.GetUntracked()) },
		}
	}
	return core.View{Children: []core.Node{
		at(sheetActions, a.actionsSheet),
		at(sheetSites, a.sitesSheet),
		at(sheetAddSite, a.addSiteSheet),
		at(sheetSignIn, a.signInSheet),
		at(sheetQuick, a.quickSheet),
		at(sheetMkdir, a.mkdirSheet),
		at(sheetNewFile, a.newFileSheet),
		at(sheetUpload, a.uploadSheet),
		at(sheetOffline, a.offlineSheet),
		at(sheetPicker, a.pickerSheet),
		at(sheetDelete, a.deleteSheet),
		at(sheetRename, a.renameSheet),
		at(sheetPermission, a.permissionSheet),
		at(sheetLocalDir, a.localDirSheet),
	}}
}

// textInput is an input bound to a signal.
func textInput(sig *core.Signal[string], label, placeholder string, change func(*ui.Input)) ui.Input {
	in := ui.Input{Value: sig.Get, OnChange: sig.Set, Label: label, Placeholder: placeholder}
	if change != nil {
		change(&in)
	}
	return in
}

// optionButton is a full-width row of a sheet: a tinted glyph and words.
func optionButton(cx *core.Scope, svg func() *core.SVG, text string, danger bool, run func()) core.Node {
	th := theme.Of(cx)
	color, kind := cText, ""
	if danger {
		color = cDanger
	}
	_ = th
	return press(text, run,
		core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 12, Padding: core.Symmetric(6, 10), MinHeight: core.Pt(theme.Touch + 4),
			CornerRadius: theme.RadiusMD, BorderWidth: 1}, cardStyle(cx, theme.RadiusMD),
		tile(cx, svg, kind, 36, 10), core.View{Style: fillStyle, Children: []core.Node{txt(cx, text, theme.TypeBody, color)}},
		icon(cx, lucide.ChevronRight, 16, cFaint))
}

// ---- file actions ----

func (a *App) actionsSheet(cx *core.Scope, st sheetState) core.Node {
	it := st.Item
	do := func(run func()) func() { return func() { a.closeSheet(); run() } }
	var rows []core.Node
	if it.IsDir {
		rows = append(rows, optionButton(cx, lucide.FolderOpen, locales.ActionOpenDir(cx), false, do(func() { a.goTo(it.Path) })))
	} else {
		rows = append(rows,
			optionButton(cx, lucide.Eye, locales.ActionPreview(cx), false, do(func() { a.openPreview(it) })),
			optionButton(cx, lucide.Download, locales.ActionDownload(cx), false, do(func() { a.download([]browse.Item{it}) })),
			optionButton(cx, lucide.Link, locales.ActionCopyLink(cx), false, do(func() { a.copyLinks([]browse.Item{it}) })))
	}
	if !browse.IsMount(it) {
		rows = append(rows, optionButton(cx, lucide.Pencil, locales.ActionRename(cx), false,
			func() { a.openSheet(sheetState{Kind: sheetRename, Item: it}) }))
	}
	rows = append(rows, optionButton(cx, lucide.Trash, locales.ActionDelete(cx), true,
		func() { a.openSheet(sheetState{Kind: sheetDelete, Item: it, Items: []browse.Item{it}}) }))
	return a.sheetFrame(it.Name, []core.Node{col(8, rows...)}, nil)
}

func (a *App) renameSheet(cx *core.Scope, st sheetState) core.Node {
	it := st.Item
	name := core.NewSignal(cx, it.Name)
	problem := core.NewSignal(cx, "")
	submit := func() {
		if strings.TrimSpace(name.Get()) == "" {
			problem.Set(locales.RenameError(cx))
			return
		}
		a.rename(it, name.Get())
	}
	in := textInput(name, locales.RenameLabel(cx), "", func(in *ui.Input) {
		in.OnSubmit = func(string) { submit() }
		in.AutoFocus = true
		in.TestID = "rename-input"
	})
	return a.sheetFrame(locales.RenameTitle(cx),
		[]core.Node{field(cx, locales.RenameLabel(cx), in, problem.Get)},
		[]core.Node{ui.Button{Text: locales.Cancel(cx), Variant: ui.ButtonSecondary, Flex: 1, OnPress: a.closeSheet},
			ui.Button{Text: locales.ConfirmRename(cx), Flex: 1, OnPress: submit, TestID: "rename-confirm"}})
}

func (a *App) deleteSheet(cx *core.Scope, st sheetState) core.Node {
	items := st.Items
	if len(items) == 0 {
		items = []browse.Item{st.Item}
	}
	title, body := locales.DeleteBatchTitle(cx), locales.DeleteBatchBody(cx, len(items))
	if len(items) == 1 {
		title, body = locales.DeleteTitle(cx), locales.DeleteBody(cx, items[0].Name)
	}
	kids := []core.Node{txt(cx, body, theme.TypeSmall, cSecond)}
	for _, it := range items {
		if browse.IsMount(it) {
			kids = append(kids, txt(cx, locales.ToastSkippedMounts(cx), theme.TypeCaption, cMuted))
			break
		}
	}
	return a.sheetFrame(title, kids,
		[]core.Node{ui.Button{Text: locales.Cancel(cx), Variant: ui.ButtonSecondary, Flex: 1, OnPress: a.closeSheet},
			dangerButton(cx, locales.ConfirmDelete(cx), func() { a.remove(items) })})
}

// ---- sites ----

func (a *App) sitesSheet(cx *core.Scope, _ sheetState) core.Node {
	th := theme.Of(cx)
	rows := core.Each(a.sites.Get, func(s store.Site) string { return s.ID },
		func(cx *core.Scope, site func() store.Site, _ func() int) core.Node {
			s := site()
			current := func() bool { return a.siteID.Get() == s.ID }
			return core.View{
				Role: core.RoleButton, Label: s.Name, Selected: current,
				OnPress: func(core.PressEvent) {
					if !current() {
						a.switchSite(s.ID)
						a.toast(locales.ToastSwitchedSite(cx, s.Name))
					}
					a.closeSheet()
				},
				Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 12, Padding: core.All(12), CornerRadius: theme.RadiusMD, BorderWidth: 1},
				StyleFunc: func() core.Style {
					if current() {
						return core.Style{Background: ui.Alpha(th.Accent(), 0.10), BorderColor: th.Accent()}
					}
					return core.Style{Background: th.SurfaceWarm(), BorderColor: th.BorderSoft()}
				},
				Children: []core.Node{
					tile(cx, lucide.Server, "", 36, 10),
					core.View{Style: fillStyle.Merge(core.Style{Gap: 2}), Children: []core.Node{
						oneLine(txt(cx, s.Name, theme.TypeBody, cText)), oneLine(txt(cx, s.URL, theme.TypeCaption, cMuted))}},
					core.Show{When: current, Then: func(cx *core.Scope) core.Node { return icon(cx, lucide.Check, 18, cAccent) }},
				},
			}
		})
	return a.sheetFrame(locales.SiteSwitcherTitle(cx),
		[]core.Node{core.View{Style: core.Style{Gap: 8}, Children: []core.Node{rows}},
			ui.Button{Text: locales.AddNewSite(cx), Variant: ui.ButtonSecondary, OnPress: func() { a.openSheet(sheetState{Kind: sheetAddSite}) }, TestID: "add-new-site"}},
		nil)
}

func (a *App) addSiteSheet(cx *core.Scope, _ sheetState) core.Node {
	name, url, token := core.NewSignal(cx, ""), core.NewSignal(cx, ""), core.NewSignal(cx, "")
	user, pass := core.NewSignal(cx, ""), core.NewSignal(cx, "")
	nameErr, urlErr, formErr := core.NewSignal(cx, ""), core.NewSignal(cx, ""), core.NewSignal(cx, "")
	busy := core.NewSignal(cx, false)
	submit := func() {
		nameErr.Set("")
		urlErr.Set("")
		formErr.Set("")
		bad := false
		if strings.TrimSpace(name.Get()) == "" {
			nameErr.Set(locales.SiteNameError(cx))
			bad = true
		}
		if _, err := store.NormalizeURL(url.Get()); err != nil {
			urlErr.Set(locales.SiteUrlError(cx))
			bad = true
		}
		if bad || busy.Get() {
			return
		}
		busy.Set(true)
		a.addSite(name.Get(), url.Get(), token.Get(), user.Get(), pass.Get(), func(err error) {
			busy.Set(false)
			if err != nil {
				formErr.Set(locales.SignInFailed(cx, errText(err)))
			}
		})
	}
	urlIn := textInput(url, locales.SiteUrlLabel(cx), locales.SiteUrlPlaceholder(cx), func(in *ui.Input) {
		in.NoAutoCorrect = true
		in.AutoCapitalize = core.AutoCapitalizeNone
		in.KeyboardType = core.KeyboardURL
		in.TestID = "site-url"
	})
	return a.sheetFrame(locales.AddSiteTitle(cx), []core.Node{
		field(cx, locales.SiteNameLabel(cx), textInput(name, locales.SiteNameLabel(cx), locales.SiteNamePlaceholder(cx), func(in *ui.Input) { in.TestID = "site-name" }), nameErr.Get),
		field(cx, locales.SiteUrlLabel(cx), urlIn, urlErr.Get),
		field(cx, locales.SiteTokenLabel(cx), textInput(token, locales.SiteTokenLabel(cx), locales.SiteTokenPlaceholder(cx), func(in *ui.Input) {
			in.Masked = true
			in.NoAutoCorrect = true
			in.TestID = "site-token"
		}), nil),
		field(cx, locales.SiteUserLabel(cx), textInput(user, locales.SiteUserLabel(cx), locales.SiteUserPlaceholder(cx), func(in *ui.Input) {
			in.NoAutoCorrect = true
			in.AutoCapitalize = core.AutoCapitalizeNone
			in.TestID = "site-user"
		}), nil),
		field(cx, locales.SitePassLabel(cx), textInput(pass, locales.SitePassLabel(cx), "", func(in *ui.Input) {
			in.Masked = true
			in.OnSubmit = func(string) { submit() }
			in.TestID = "site-pass"
		}), formErr.Get),
	}, []core.Node{
		ui.Button{Text: locales.Cancel(cx), Variant: ui.ButtonSecondary, Flex: 1, OnPress: a.closeSheet},
		ui.Button{Text: locales.SaveAndConnect(cx), Flex: 1, OnPress: submit, Disabled: busy.Get, TestID: "site-save"},
	})
}

func (a *App) signInSheet(cx *core.Scope, _ sheetState) core.Node {
	user, pass, problem := core.NewSignal(cx, ""), core.NewSignal(cx, ""), core.NewSignal(cx, "")
	busy := core.NewSignal(cx, false)
	submit := func() {
		if busy.Get() {
			return
		}
		busy.Set(true)
		problem.Set("")
		a.signIn(user.Get(), pass.Get(), func(err error) {
			busy.Set(false)
			if err != nil {
				problem.Set(locales.SignInFailed(cx, errText(err)))
			}
		})
	}
	return a.sheetFrame(locales.SignInTitle(cx), []core.Node{
		field(cx, locales.SiteUserLabel(cx), textInput(user, locales.SiteUserLabel(cx), "", func(in *ui.Input) {
			in.NoAutoCorrect = true
			in.AutoCapitalize = core.AutoCapitalizeNone
			in.AutoFocus = true
		}), nil),
		field(cx, locales.SitePassLabel(cx), textInput(pass, locales.SitePassLabel(cx), "", func(in *ui.Input) {
			in.Masked = true
			in.OnSubmit = func(string) { submit() }
		}), problem.Get),
	}, []core.Node{
		ui.Button{Text: locales.Cancel(cx), Variant: ui.ButtonSecondary, Flex: 1, OnPress: a.closeSheet},
		ui.Button{Text: locales.SignInAction(cx), Flex: 1, OnPress: submit, Disabled: busy.Get},
	})
}

// ---- create and upload ----

func (a *App) quickSheet(cx *core.Scope, _ sheetState) core.Node {
	return a.sheetFrame(locales.QuickAddTitle(cx), []core.Node{col(8,
		optionButton(cx, lucide.FolderPlus, locales.FabMkdir(cx), false, func() { a.openSheet(sheetState{Kind: sheetMkdir}) }),
		optionButton(cx, lucide.FilePlus, locales.FabNewFile(cx), false, func() { a.openSheet(sheetState{Kind: sheetNewFile}) }),
		optionButton(cx, lucide.Upload, locales.FabUpload(cx), false, func() { a.openSheet(sheetState{Kind: sheetUpload}) }),
		optionButton(cx, lucide.CloudDownload, locales.FabOffline(cx), false, a.openOffline),
	)}, nil)
}

func (a *App) mkdirSheet(cx *core.Scope, _ sheetState) core.Node {
	name, problem := core.NewSignal(cx, ""), core.NewSignal(cx, "")
	submit := func() {
		if strings.TrimSpace(name.Get()) == "" {
			problem.Set(locales.FolderNameError(cx))
			return
		}
		a.mkdir(name.Get())
	}
	in := textInput(name, locales.FolderNameLabel(cx), locales.FolderNamePlaceholder(cx), func(in *ui.Input) {
		in.OnSubmit = func(string) { submit() }
		in.AutoFocus = true
		in.TestID = "mkdir-input"
	})
	return a.sheetFrame(locales.MkdirTitle(cx), []core.Node{field(cx, locales.FolderNameLabel(cx), in, problem.Get)},
		[]core.Node{ui.Button{Text: locales.Cancel(cx), Variant: ui.ButtonSecondary, Flex: 1, OnPress: a.closeSheet},
			ui.Button{Text: locales.Create(cx), Flex: 1, OnPress: submit, TestID: "mkdir-confirm"}})
}

func (a *App) newFileSheet(cx *core.Scope, _ sheetState) core.Node {
	name, body, problem := core.NewSignal(cx, ""), core.NewSignal(cx, ""), core.NewSignal(cx, "")
	submit := func() {
		if strings.TrimSpace(name.Get()) == "" {
			problem.Set(locales.FileNameError(cx))
			return
		}
		a.newFile(name.Get(), body.Get())
	}
	return a.sheetFrame(locales.NewFileTitle(cx), []core.Node{
		field(cx, locales.FileNameLabel(cx), textInput(name, locales.FileNameLabel(cx), locales.FileNamePlaceholder(cx), func(in *ui.Input) {
			in.AutoFocus = true
			in.TestID = "newfile-name"
		}), problem.Get),
		field(cx, locales.FileBodyLabel(cx), textInput(body, locales.FileBodyLabel(cx), locales.FileBodyPlaceholder(cx), func(in *ui.Input) {
			in.Multiline = true
			in.Style = core.Style{MinHeight: core.Pt(120)}
		}), nil),
	}, []core.Node{ui.Button{Text: locales.Cancel(cx), Variant: ui.ButtonSecondary, Flex: 1, OnPress: a.closeSheet},
		ui.Button{Text: locales.CreateFile(cx), Flex: 1, OnPress: submit, TestID: "newfile-confirm"}})
}

func (a *App) uploadSheet(cx *core.Scope, _ sheetState) core.Node {
	return a.sheetFrame(locales.UploadTitle(cx), []core.Node{
		txt(cx, locales.UploadHint(cx), theme.TypeSmall, cMuted),
		ui.Button{Text: locales.PickFromDevice(cx), Icon: lucide.Upload, OnPress: a.pickAndUpload, TestID: "upload-pick"},
	}, nil)
}

// ---- offline download ----

// openOffline opens the offline download panel with its folder: the one set
// for the site, else the folder shown unless it is the root, else the last
// one chosen.
func (a *App) openOffline() {
	s, ok := a.site()
	if !ok {
		a.toast(locales.ToastNoSite(a.cx))
		return
	}
	dir := s.Settings.OfflinePath
	if dir == "" && a.path.GetUntracked() != "/" {
		dir = a.path.GetUntracked()
	}
	if dir == "" {
		dir = a.offlineDir.GetUntracked()
	}
	a.cx.Batch(func() {
		a.offlineDir.Set(dir)
		a.openSheet(sheetState{Kind: sheetOffline})
	})
}

// parseLinks splits a field of download links, one to a line, and reports
// whether every one is a link a downloader takes.
func parseLinks(text string) (links []string, ok bool) {
	for _, l := range strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' }) {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		low := strings.ToLower(l)
		if !(strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://") || strings.HasPrefix(low, "magnet:") ||
			strings.HasPrefix(low, "ed2k://") || strings.HasPrefix(low, "ftp://")) {
			return nil, false
		}
		links = append(links, l)
	}
	return links, len(links) > 0
}

func (a *App) offlineSheet(cx *core.Scope, _ sheetState) core.Node {
	th := theme.Of(cx)
	problem := core.NewSignal(cx, "")
	tool := func() string {
		list, _ := a.tools.Get()
		cur := a.offlineTool.Get()
		for _, t := range list {
			if string(t) == cur {
				return cur
			}
		}
		if len(list) > 0 {
			return string(list[0])
		}
		return ""
	}
	submit := func() {
		links, ok := parseLinks(a.offlineURLs.Get())
		switch {
		case !ok:
			problem.Set(locales.OfflineUrlError(cx))
		case a.offlineDir.Get() == "" || a.offlineDir.Get() == "/":
			problem.Set(locales.DirNeedStorage(cx))
		case tool() == "":
			problem.Set(locales.OfflineToolsNone(cx))
		default:
			problem.Set("")
			a.addOffline(links, tool(), a.offlineDir.Get())
		}
	}
	urls := textInput(a.offlineURLs, locales.OfflineUrlLabel(cx), locales.OfflineUrlPlaceholder(cx), func(in *ui.Input) {
		in.Multiline = true
		in.NoAutoCorrect = true
		in.AutoCapitalize = core.AutoCapitalizeNone
		in.Style = core.Style{MinHeight: core.Pt(96)}
		in.TestID = "offline-urls"
	})
	tools := core.Each(func() []openlist.OfflineTool { l, _ := a.tools.Get(); return l },
		func(t openlist.OfflineTool) string { return string(t) },
		func(cx *core.Scope, t func() openlist.OfflineTool, _ func() int) core.Node {
			name := string(t())
			return chip(cx, name, func() bool { return tool() == name }, func() { a.offlineTool.Set(name) })
		})
	noTools := core.Show{When: func() bool { l, _ := a.tools.Get(); return len(l) == 0 && !a.tools.Loading() }, Then: func(cx *core.Scope) core.Node {
		return txt(cx, locales.OfflineToolsNone(cx), theme.TypeCaption, cMuted)
	}}
	dirText := func() string {
		if d := a.offlineDir.Get(); d != "" {
			return d
		}
		return locales.DirNeedStorage(cx)
	}
	dirRow := core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 8, Padding: core.Symmetric(8, 12), CornerRadius: theme.RadiusMD, BorderWidth: 1},
		StyleFunc: cardStyle(cx, theme.RadiusMD), Children: []core.Node{
			icon(cx, lucide.Folder, 18, cAccent),
			core.View{Style: fillStyle, Children: []core.Node{oneLine(txtf(cx, dirText, theme.TypeSmall, cText))}},
			textButton(cx, func() string { return locales.Change(cx) }, func() {
				a.openPicker(&pickerSpec{
					Title: locales.DirPickerOffline(cx), Start: a.startFor(a.offlineDir.GetUntracked()), ConfirmText: locales.DirSelectThis(cx),
					Confirm: func(dir string) { a.offlineDir.Set(dir) }, Back: sheetState{Kind: sheetOffline},
				})
			}),
		}}
	recent := core.Show{When: func() bool { s, ok := a.site(); return ok && len(s.Settings.RecentOffline) > 0 }, Then: func(cx *core.Scope) core.Node {
		return col(6,
			txt(cx, locales.RecentDirsLabel(cx), theme.TypeCaption, cMuted),
			core.View{Style: core.Style{Direction: core.Row, Wrap: core.Wrap, Gap: 8}, Children: []core.Node{core.Each(
				func() []string { s, _ := a.site(); return s.Settings.RecentOffline },
				func(d string) string { return d },
				func(cx *core.Scope, d func() string, _ func() int) core.Node {
					dir := d()
					return chip(cx, dir, func() bool { return a.offlineDir.Get() == dir }, func() { a.offlineDir.Set(dir) })
				})}})
	}}
	_ = th
	return a.sheetFrame(locales.OfflineTitle(cx), []core.Node{
		field(cx, locales.OfflineUrlLabel(cx), urls, problem.Get),
		col(6, txt(cx, locales.OfflineToolLabel(cx), theme.TypeCaption.Merge(core.Style{FontWeight: ui.Semibold}), cSecond),
			core.View{Style: core.Style{Direction: core.Row, Wrap: core.Wrap, Gap: 8}, Children: []core.Node{tools, noTools}}),
		col(6, txt(cx, locales.SaveDirLabel(cx), theme.TypeCaption.Merge(core.Style{FontWeight: ui.Semibold}), cSecond), dirRow),
		recent,
	}, []core.Node{ui.Button{Text: locales.Cancel(cx), Variant: ui.ButtonSecondary, Flex: 1, OnPress: a.closeSheet},
		ui.Button{Text: locales.AddTask(cx), Flex: 1, OnPress: submit, TestID: "offline-add"}})
}

// startFor is where a folder picker starts for a folder that may not be set.
func (a *App) startFor(dir string) string {
	if dir == "" {
		return a.path.GetUntracked()
	}
	return dir
}

// ---- folder picker ----

func (a *App) pickerSheet(cx *core.Scope, st sheetState) core.Node {
	spec := st.Picker
	if spec == nil {
		return core.View{}
	}
	th := theme.Of(cx)
	leave := func() {
		if spec.Back.Kind != sheetNone {
			a.openSheet(spec.Back)
		} else {
			a.closeSheet()
		}
	}
	folders := func() []browse.Item {
		res, ok := a.pickList.Get()
		dir := a.pickPath.Get()
		if !ok || res.Dir != dir {
			if c, hit := a.cached(dir); hit {
				res = c
			} else {
				return nil
			}
		}
		var out []browse.Item
		for _, it := range res.Items {
			if it.IsDir {
				out = append(out, it)
			}
		}
		return browse.Sort(out, store.SortName, false)
	}
	blocked := func() string {
		p := a.pickPath.Get()
		switch {
		case p == "/" && !spec.AllowRoot:
			return locales.DirNeedStorage(cx)
		case spec.Exclude != "" && p == spec.Exclude:
			return locales.MoveAlready(cx)
		}
		return ""
	}
	up := core.Show{When: func() bool { return a.pickPath.Get() != "/" }, Then: func(cx *core.Scope) core.Node {
		return optionButton(cx, lucide.ArrowLeft, locales.DirUp(cx), false, func() { a.pickPath.Set(openlist.Parent(a.pickPath.GetUntracked())) })
	}}
	list := core.Each(folders, func(it browse.Item) string { return it.Path },
		func(cx *core.Scope, item func() browse.Item, _ func() int) core.Node {
			it := item()
			return optionButton(cx, lucide.Folder, it.Name, false, func() { a.pickPath.Set(it.Path) })
		})
	empty := core.Show{When: func() bool { return len(folders()) == 0 && !a.pickList.Loading() }, Then: func(cx *core.Scope) core.Node {
		return txt(cx, locales.DirEmpty(cx), theme.TypeCaption.Merge(core.Style{TextAlign: core.TextAlignCenter}), cMuted)
	}}
	loading := core.Show{When: func() bool { return len(folders()) == 0 && a.pickList.Loading() }, Then: func(cx *core.Scope) core.Node {
		return loadingView(cx, locales.Loading(cx))
	}}
	failed := core.Show{When: func() bool { return a.pickList.Err() != nil && !a.pickList.Loading() }, Then: func(cx *core.Scope) core.Node {
		return txtf(cx, func() string {
			if e := a.pickList.Err(); e != nil {
				return errText(e)
			}
			return ""
		}, theme.TypeCaption, cDanger)
	}}
	current := core.View{Style: core.Style{Padding: core.Symmetric(8, 12), CornerRadius: theme.RadiusMD, BorderWidth: 1},
		StyleFunc: func() core.Style {
			return core.Style{Background: ui.Alpha(th.Accent(), 0.08), BorderColor: th.BorderSoft()}
		},
		Children: []core.Node{oneLine(txtf(cx, func() string { return locales.DirChosen(cx, a.pickPath.Get()) }, theme.TypeSmall, cText))}}
	note := core.Show{When: func() bool { return blocked() != "" }, Then: func(cx *core.Scope) core.Node {
		return txtf(cx, blocked, theme.TypeCaption, cMuted)
	}}
	return a.sheetFrame(spec.Title, []core.Node{
		current, note, up, core.View{Style: core.Style{Gap: 8}, TestID: "picker-list", Children: []core.Node{list}}, loading, empty, failed,
	}, []core.Node{
		ui.Button{Text: locales.Back(cx), Variant: ui.ButtonSecondary, Flex: 1, OnPress: leave, TestID: "picker-back"},
		ui.Button{Text: spec.ConfirmText, Flex: 1, Disabled: func() bool { return blocked() != "" }, TestID: "picker-confirm", OnPress: func() {
			dir := a.pickPath.GetUntracked()
			spec.Confirm(dir)
			if spec.Back.Kind != sheetNone {
				a.openSheet(spec.Back)
			}
		}},
	})
}

// ---- folder on this device ----

// pickLocalDir asks the system for the folder downloads of the current site
// are saved in, with the system's own folder dialog: the browser's in a
// browser.
func (a *App) pickLocalDir() {
	s, ok := a.site()
	if !ok {
		a.toast(locales.ToastNoSite(a.cx))
		return
	}
	if isWeb() {
		webFolderPick(s.ID, func(name string, err error) {
			a.cx.Post(func() {
				switch {
				case errors.Is(err, errNoFolderPicker):
					a.toast(locales.ToastNoFolderPicker(a.cx))
				case err != nil:
					a.fail(err)
				case name != "":
					a.webFolders.Update(func(m map[string]string) map[string]string { out := withoutKey(m, s.ID); out[s.ID] = name; return out })
					a.toast(locales.ToastLocalDir(a.cx, name))
				}
			})
		})
		return
	}
	if savesThroughDialog() {
		// Android's folder picker reads a folder and cannot write in it, so the
		// choice is among the shared folders the app can reach.
		a.checkAccess(false)
		a.openSheet(sheetState{Kind: sheetLocalDir})
		return
	}
	a.cx.PickDirectories(core.PickDirectoriesOptions{Title: locales.LocalDirTitle(a.cx), Button: locales.SaveSettings(a.cx), Start: a.downloadDir(s)},
		func(dirs []core.Directory, err error) {
			switch {
			case err != nil:
				a.fail(err)
			case len(dirs) == 0:
			case dirs[0].Path == "":
				a.toast(locales.LocalDirNoPickerToast(a.cx))
			default:
				a.setLocalDir(s.ID, dirs[0].Path)
				a.checkAccess(false)
			}
		})
}

// localDirSheet is the choice of a folder for downloads on Android: one of
// the shared folders of the storage, or a save dialog for each download.
func (a *App) localDirSheet(cx *core.Scope, _ sheetState) core.Node {
	s, ok := a.site()
	if !ok {
		return core.View{}
	}
	pick := func(dir string) func() {
		return func() {
			a.closeSheet()
			a.setLocalDir(s.ID, dir)
			a.checkAccess(false)
		}
	}
	rows := make([]core.Node, 0, len(androidFolders)+1)
	for _, name := range androidFolders {
		dir := androidFolderDir(name)
		rows = append(rows, optionButton(cx, lucide.FolderOpen, name+"/"+appFolder, false, pick(dir)))
	}
	rows = append(rows, optionButton(cx, lucide.Download, locales.LocalDirAsk(cx), false, pick(localDirAsk)))
	return a.sheetFrame(locales.LocalDirTitle(cx), []core.Node{col(8, rows...)},
		[]core.Node{ui.Button{Text: locales.Cancel(cx), Variant: ui.ButtonSecondary, Flex: 1, OnPress: a.closeSheet}})
}

func (a *App) setLocalDir(siteID, dir string) {
	a.updateSite(siteID, func(s *store.Site) { s.Settings.LocalDir = dir })
	if dir == localDirAsk {
		dir = locales.LocalDirAsk(a.cx)
	}
	a.toast(locales.ToastLocalDir(a.cx, dir))
}
