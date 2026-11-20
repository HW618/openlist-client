package app

import (
	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/HW618/openlist-client/src/internal/browse"
	"github.com/HW618/openlist-client/src/internal/openlist"
	"github.com/HW618/openlist-client/src/internal/store"
	"github.com/HW618/openlist-client/src/locales"
	"github.com/HW618/openlist-client/src/theme"
)

// appVersion is the version of the app, as nsgo.yaml says.
const appVersion = "0.1.0"

// sitesMaxHeight is how tall the expanded site list gets before it scrolls.
const sitesMaxHeight = 320

// savePrefs keeps the app-wide preferences.
func (a *App) savePrefs(change func(*store.Prefs)) {
	p := a.prefs.GetUntracked()
	change(&p)
	p = p.Fix()
	a.prefs.Set(p)
	_ = a.prefStore.Set(p)
}

// settingsPage is the third tab: sites, and what is set for the app and for
// each site.
func (a *App) settingsPage(cx *core.Scope) core.Node {
	group := func(title string, kids ...core.Node) core.Node {
		return col(8,
			core.View{Style: core.Style{Padding: core.Symmetric(0, 4)}, Children: []core.Node{
				txt(cx, title, theme.TypeCaption.Merge(core.Style{FontWeight: ui.Semibold}), cMuted)}},
			card(cx, core.Style{Padding: core.Symmetric(4, 14)}, kids...))
	}
	return page(cx,
		header(cx, core.View{Style: fillStyle, Children: []core.Node{txt(cx, locales.SettingsTitle(cx), theme.TypeTitle, cText)}},
			ui.IconButton{Icon: lucide.Plus, Label: locales.AddSiteAction(cx), Size: theme.Touch, TestID: "settings-add-site",
				OnPress: func() { a.openSheet(sheetState{Kind: sheetAddSite}) }}),
		core.Scroll{Style: fillStyle, Children: []core.Node{
			core.View{Style: core.Style{Gap: 18, Padding: core.Edges{Left: theme.Space4, Right: theme.Space4, Top: 4, Bottom: 32}}, Children: []core.Node{
				group(locales.GroupSites(cx), a.sitesList(cx)),
				group(locales.GroupAppearance(cx), a.appearanceRows(cx)...),
				group(locales.GroupNotifications(cx), a.notificationRows(cx)...),
				group(locales.GroupPaths(cx), a.pathRows(cx)...),
				group(locales.GroupAbout(cx), a.aboutRows(cx)...),
			}}}})
}

// sitesList is the connected site alone, until the list is expanded to show
// every saved site in a scrolling area.
func (a *App) sitesList(cx *core.Scope) core.Node {
	th := theme.Of(cx)
	all := func() []store.Site { return a.sites.Get() }
	connected := func() []store.Site {
		list := a.sites.Get()
		for _, s := range list {
			if s.ID == a.siteID.Get() {
				return []store.Site{s}
			}
		}
		if len(list) > 0 {
			return list[:1]
		}
		return nil
	}
	build := func(cx *core.Scope, visible func() []store.Site) core.Node {
		return core.Each(visible, func(s store.Site) string { return s.ID },
			func(cx *core.Scope, site func() store.Site, _ func() int) core.Node {
				s := site()
				current := func() bool { return a.siteID.Get() == s.ID }
				line := func() string {
					st := a.status.Get()[s.ID]
					lat := browse.Latency(st.Latency)
					switch {
					case st.Checking:
						return locales.SiteStatusLine(cx, s.URL, locales.SiteChecking(cx))
					case st.Failed:
						return locales.SiteStatusLine(cx, s.URL, locales.SiteUnreachable(cx)+": "+st.Err)
					case st.Auth == authOK:
						role := locales.SiteRoleUser(cx)
						switch st.Role {
						case openlist.RoleAdmin:
							role = locales.SiteRoleAdmin(cx)
						case openlist.RoleGuest:
							role = locales.SiteRoleGuest(cx)
						}
						return locales.SiteStatusLine(cx, s.URL, locales.SiteAuthOk(cx, st.User, role)+" · "+lat)
					case st.Auth == authGuest:
						return locales.SiteStatusLine(cx, s.URL, locales.SiteAuthGuest(cx)+" · "+lat)
					case st.Auth == authNeedLogin:
						return locales.SiteStatusLine(cx, s.URL, locales.SiteNeedLogin(cx))
					case st.Auth == authDisabled:
						return locales.SiteStatusLine(cx, s.URL, locales.SiteAccountDisabled(cx, st.User))
					}
					return s.URL
				}
				small := func(text string, run func(), danger bool) core.Node {
					c := cAccent
					if danger {
						c = cDanger
					}
					return press(text, run, core.Style{Padding: core.Symmetric(4, 10), MinHeight: core.Pt(28), CornerRadius: theme.RadiusSM, BorderWidth: 1,
						AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter},
						func() core.Style { return core.Style{BorderColor: th.BorderSoft()} },
						txt(cx, text, theme.TypeCaption.Merge(core.Style{FontWeight: ui.Medium}), c))
				}
				// The buttons wrap under the name and the status, which would
				// else have no room left on a narrow screen.
				return row(10,
					tile(cx, lucide.Server, "", 36, 10),
					core.View{Style: fillStyle.Merge(core.Style{Gap: 6}), Children: []core.Node{
						core.View{Style: core.Style{Gap: 2}, Children: []core.Node{
							row(6, oneLine(txt(cx, s.Name, theme.TypeBody, cText)),
								core.Show{When: current, Then: func(cx *core.Scope) core.Node { return ui.Badge{Text: locales.SiteCurrentTag(cx)} }}),
							oneLine(txtf(cx, line, theme.TypeCaption, cMuted)),
						}},
						core.View{Style: core.Style{Direction: core.Row, Gap: 6, AlignItems: core.AlignCenter, Wrap: core.Wrap}, Children: []core.Node{
							small(locales.TestSite(cx), func() { a.testSite(s) }, false),
							core.Show{When: func() bool { st := a.status.Get()[s.ID]; return st.Auth == authNeedLogin }, Then: func(cx *core.Scope) core.Node {
								return small(locales.SignInAction(cx), func() {
									if !current() {
										a.switchSite(s.ID)
									}
									a.openSheet(sheetState{Kind: sheetSignIn})
								}, false)
							}},
							core.Show{When: func() bool { return a.signedIn(s.ID) }, Then: func(cx *core.Scope) core.Node {
								return small(locales.SignOutAction(cx), func() { a.signOut(s) }, false)
							}},
							core.Show{When: func() bool { return !current() }, Then: func(cx *core.Scope) core.Node {
								return small(locales.SwitchSite(cx), func() { a.switchSite(s.ID); a.toast(locales.ToastSwitchedSite(cx, s.Name)) }, false)
							}},
							small(locales.DeleteSite(cx), func() { a.removeSite(s.ID); a.toast(locales.ToastSiteRemoved(cx)) }, true),
						}}}})
			})
	}
	rows := core.Show{When: a.sitesOpen.Get,
		Then: func(cx *core.Scope) core.Node {
			return ui.ScrollView{Style: core.Style{MaxHeight: core.Pt(sitesMaxHeight)}, ContentStyle: core.Style{Gap: 14, Padding: core.Edges{Right: 6}},
				Children: []core.Node{build(cx, all)}}
		},
		Else: func(cx *core.Scope) core.Node { return build(cx, connected) }}
	toggle := core.Show{When: func() bool { return len(a.sites.Get()) > 1 }, Then: func(cx *core.Scope) core.Node {
		th := theme.Of(cx)
		return core.View{Children: []core.Node{
			ui.IconButton{Variant: ui.IconButtonGhost, Size: 28, IconSize: 18, TestID: "sites-toggle",
				Tint: func() core.Color { return cMuted(th) },
				Icon: func() *core.SVG {
					if a.sitesOpen.Get() {
						return lucide.ChevronUp()
					}
					return lucide.ChevronDown()
				},
				LabelFunc: func() string {
					if a.sitesOpen.Get() {
						return locales.CollapseSites(cx)
					}
					return locales.ExpandSites(cx, len(a.sites.Get()))
				},
				OnPress: func() { a.sitesOpen.Update(func(b bool) bool { return !b }) }}}}
	}}
	none := core.Show{When: func() bool { return len(a.sites.Get()) == 0 }, Then: func(cx *core.Scope) core.Node {
		return core.View{Style: core.Style{Padding: core.Symmetric(14, 0), Gap: 10}, Children: []core.Node{
			txt(cx, locales.NoSitesDesc(cx), theme.TypeSmall, cMuted),
			ui.Button{Text: locales.AddSiteAction(cx), Icon: lucide.Plus, OnPress: func() { a.openSheet(sheetState{Kind: sheetAddSite}) }}}}
	}}
	list := core.View{Style: core.Style{Direction: core.Row, Gap: 6, AlignItems: core.AlignStart}, Children: []core.Node{
		core.View{Style: fillStyle, Children: []core.Node{rows}}, toggle}}
	return core.View{Style: core.Style{Gap: 4, Padding: core.Symmetric(12, 4)}, Children: []core.Node{list, none}}
}

// appearanceRows are the settings of the whole app.
func (a *App) appearanceRows(cx *core.Scope) []core.Node {
	dark := ui.Switch{Checked: cx.Dark, Label: locales.DarkMode(cx), TestID: "dark-switch", OnChange: func(on bool) {
		if on {
			cx.SetAppearance(core.AppearanceDark)
		} else {
			cx.SetAppearance(core.AppearanceLight)
		}
		a.savePrefs(func(p *store.Prefs) { p.Dark = on })
	}}
	views := row(8,
		chip(cx, locales.ViewListFull(cx), func() bool { return a.prefs.Get().View == store.ViewList }, func() { a.setDefaultView(store.ViewList) }),
		chip(cx, locales.ViewGridFull(cx), func() bool { return a.prefs.Get().View == store.ViewGrid }, func() { a.setDefaultView(store.ViewGrid) }))
	sorts := core.View{Style: core.Style{Direction: core.Row, Gap: 8, Wrap: core.Wrap}, Children: []core.Node{
		chip(cx, locales.SortByName(cx), func() bool { return a.prefs.Get().Sort == store.SortName }, func() { a.setDefaultSort(store.SortName) }),
		chip(cx, locales.SortBySize(cx), func() bool { return a.prefs.Get().Sort == store.SortSize }, func() { a.setDefaultSort(store.SortSize) }),
		chip(cx, locales.SortByDate(cx), func() bool { return a.prefs.Get().Sort == store.SortDate }, func() { a.setDefaultSort(store.SortDate) }),
	}}
	return []core.Node{
		optionRow(cx, locales.DarkMode(cx), locales.DarkModeDesc(cx), dark),
		hairline(cx),
		optionRow(cx, locales.DefaultView(cx), locales.DefaultViewDesc(cx), views),
		hairline(cx),
		optionRow(cx, locales.DefaultSort(cx), locales.DefaultSortDesc(cx), sorts),
	}
}

func (a *App) setDefaultView(v store.ViewMode) {
	a.savePrefs(func(p *store.Prefs) { p.View = v })
	a.view.Set(v)
}

func (a *App) setDefaultSort(k store.SortKey) {
	a.savePrefs(func(p *store.Prefs) { p.Sort = k })
	a.sortKey.Set(k)
}

// notificationRows are the task-completion notification toggles.
func (a *App) notificationRows(cx *core.Scope) []core.Node {
	master := ui.Switch{
		Checked: func() bool { return a.prefs.Get().NotifyMaster() },
		Label:   locales.NotifyMaster(cx), TestID: "notify-master",
		OnChange: func(on bool) {
			a.saveNotifyToggle(func(n *store.NotifyPrefs) { n.Master = on })
			if on {
				a.pollOffline()
			}
		},
	}
	success := ui.Switch{
		Checked:  func() bool { return a.prefs.Get().NotifySuccess() },
		Label:    locales.NotifySuccess(cx), TestID: "notify-success",
		Disabled: func() bool { return !a.prefs.Get().NotifyMaster() },
		OnChange: func(on bool) { a.saveNotifyToggle(func(n *store.NotifyPrefs) { n.Success = on }) },
	}
	failure := ui.Switch{
		Checked:  func() bool { return a.prefs.Get().NotifyFailure() },
		Label:    locales.NotifyFailure(cx), TestID: "notify-failure",
		Disabled: func() bool { return !a.prefs.Get().NotifyMaster() },
		OnChange: func(on bool) { a.saveNotifyToggle(func(n *store.NotifyPrefs) { n.Failure = on }) },
	}
	return []core.Node{
		optionRow(cx, locales.NotifyMaster(cx), locales.NotifyMasterDesc(cx), master),
		hairline(cx),
		optionRow(cx, locales.NotifySuccess(cx), locales.NotifySuccessDesc(cx), success),
		hairline(cx),
		optionRow(cx, locales.NotifyFailure(cx), locales.NotifyFailureDesc(cx), failure),
		hairline(cx),
		core.View{Style: core.Style{Gap: 2, Padding: core.Symmetric(12, 0)}, Children: []core.Node{
			txt(cx, locales.NotifyLimitsTitle(cx), theme.TypeBody, cText),
			txt(cx, locales.NotifyLimitsDesc(cx), theme.TypeCaption, cMuted)}},
	}
}

// pathValue shows a folder with a button that changes it, and one that clears
// it where there is a default to fall back to.
func (a *App) pathValue(cx *core.Scope, value func() string, empty string, choose func(), clear func()) core.Node {
	return a.pathValueID(cx, "", value, empty, choose, clear)
}

// pathValueID is pathValue with a test ID for its button.
func (a *App) pathValueID(cx *core.Scope, id string, value func() string, empty string, choose func(), clear func()) core.Node {
	text := func() string {
		if v := value(); v != "" {
			return v
		}
		return empty
	}
	kids := []core.Node{
		core.View{Style: core.Style{MaxWidth: core.Pt(180), FlexShrink: 1, MinWidth: core.Pt(0)}, Children: []core.Node{
			oneLine(txtf(cx, text, theme.TypeCaption.Merge(core.Style{TextAlign: core.TextAlignEnd}), cMuted))}},
		textButtonID(cx, id, func() string { return locales.Change(cx) }, choose),
	}
	if clear != nil {
		kids = append(kids[:1], append([]core.Node{core.Show{When: func() bool { return value() != "" }, Then: func(cx *core.Scope) core.Node {
			return iconButton(lucide.X, locales.ClearSearchLabel(cx), clear)
		}}}, kids[1:]...)...)
	}
	return core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 4, FlexShrink: 1, MinWidth: core.Pt(0)}, Children: kids}
}

// pathRows are the settings of the current site: its folders.
func (a *App) pathRows(cx *core.Scope) []core.Node {
	cur := func() store.Site { s, _ := a.site(); return s }
	filesDir := a.pathValue(cx, func() string { return cur().Settings.FilesPath }, "/",
		func() {
			s, ok := a.site()
			if !ok {
				a.toast(locales.ToastNoSite(cx))
				return
			}
			a.openPicker(&pickerSpec{Title: locales.DirPickerFiles(cx), Start: a.startFor(s.Settings.FilesPath), AllowRoot: true, ConfirmText: locales.DirSelectThis(cx),
				Confirm: func(dir string) {
					a.updateSite(s.ID, func(st *store.Site) { st.Settings.FilesPath = dir })
					a.closeSheet()
					a.toast(locales.ToastFilesDir(cx, dir))
				}})
		},
		func() { a.updateSite(a.siteID.GetUntracked(), func(st *store.Site) { st.Settings.FilesPath = "" }) })
	offDir := a.pathValue(cx, func() string { return cur().Settings.OfflinePath }, locales.DirNeedStorage(cx),
		func() {
			s, ok := a.site()
			if !ok {
				a.toast(locales.ToastNoSite(cx))
				return
			}
			a.openPicker(&pickerSpec{Title: locales.DirPickerDefaultOffline(cx), Start: a.startFor(s.Settings.OfflinePath), ConfirmText: locales.DirSelectThis(cx),
				Confirm: func(dir string) {
					a.updateSite(s.ID, func(st *store.Site) { st.Settings.OfflinePath = dir })
					a.closeSheet()
					a.toast(locales.ToastOfflineDir(cx, dir))
				}})
		},
		func() { a.updateSite(a.siteID.GetUntracked(), func(st *store.Site) { st.Settings.OfflinePath = "" }) })
	var local core.Node
	switch {
	case savesThroughDialog():
		local = a.pathValueID(cx, "local-dir-change", func() string {
			s, ok := a.site()
			switch {
			case !ok:
				return ""
			case asksEachDownload(s):
				return locales.LocalDirAsk(cx)
			}
			return a.downloadDir(s)
		}, defaultDownloadDir(), a.pickLocalDir, nil)
	case isWeb():
		local = a.pathValue(cx, func() string { return a.webFolders.Get()[a.siteID.Get()] }, locales.LocalDirNone(cx), a.pickLocalDir,
			func() {
				id := a.siteID.GetUntracked()
				webFolderForget(id)
				a.webFolders.Update(func(m map[string]string) map[string]string { return withoutKey(m, id) })
				a.toast(locales.ToastBrowserDownloadOn(cx))
			})
	default:
		local = a.pathValueID(cx, "local-dir-change", func() string {
			if s, ok := a.site(); ok {
				return a.downloadDir(s)
			}
			return ""
		}, defaultDownloadDir(), a.pickLocalDir, nil)
	}
	rows := []core.Node{
		optionRow(cx, locales.FilesDirTitle(cx), locales.FilesDirDesc(cx), filesDir),
		hairline(cx),
		optionRow(cx, locales.OfflineDirTitle(cx), locales.OfflineDirDesc(cx), offDir),
		hairline(cx),
		optionRow(cx, locales.LocalDirSettingTitle(cx), locales.LocalDirSettingDesc(cx), local),
	}
	if savesThroughDialog() {
		rows = append(rows, core.Show{
			When: func() bool { s, ok := a.site(); return ok && !asksEachDownload(s) && !a.canWrite.Get() },
			Then: func(cx *core.Scope) core.Node {
				return txt(cx, locales.LocalDirNeedAccess(cx), theme.TypeCaption, cMuted)
			}})
	}
	return rows
}

// aboutRows are the cache and what the app is.
func (a *App) aboutRows(cx *core.Scope) []core.Node {
	usage := func() string {
		a.cacheVersion.Get()
		a.listing.State()
		folders, bytes := a.cacheStats()
		if folders == 0 {
			return locales.CacheEmpty(cx)
		}
		return locales.CacheUsed(cx, browse.Size(bytes))
	}
	clear := ui.Button{Text: locales.ClearCache(cx), Variant: ui.ButtonSecondary, TestID: "clear-cache", OnPress: func() {
		a.clearCache()
		a.toast(locales.ToastCacheCleared(cx))
	}}
	return []core.Node{
		core.View{Style: core.Style{Direction: core.Row, AlignItems: core.AlignCenter, Gap: 12, Padding: core.Symmetric(12, 0)}, Children: []core.Node{
			core.View{Style: fillStyle.Merge(core.Style{Gap: 2}), Children: []core.Node{
				txt(cx, locales.CacheTitle(cx), theme.TypeBody, cText), txtf(cx, usage, theme.TypeCaption, cMuted)}},
			clear}},
		hairline(cx),
		core.View{Style: core.Style{Gap: 2, Padding: core.Symmetric(12, 0)}, Children: []core.Node{
			txt(cx, locales.AboutTitle(cx), theme.TypeBody, cText),
			txt(cx, locales.AboutDesc(cx, appVersion), theme.TypeCaption, cMuted)}},
	}
}
