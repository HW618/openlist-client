package app

import (
	"context"
	"errors"
	"net/url"
	"runtime"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/HW618/openlist-client/src/internal/access"
	"github.com/HW618/openlist-client/src/locales"
	"github.com/HW618/openlist-client/src/theme"
)

// startupChecks makes the app look at what it may do as it starts; tests that
// do not mean to look turn it off.
var startupChecks = true

// errNoFolderPicker is what a browser without a folder picker answers.
var errNoFolderPicker = errors.New("this browser can't choose a folder to save in")

// onLinux reports whether the app runs on Linux, where the secure store is a
// Secret Service the user may not have.
var onLinux = func() bool { return runtime.GOOS == "linux" }

// isWeb reports whether the app runs in a browser.
func isWeb() bool { return runtime.GOOS == "js" }

// savesThroughDialog reports whether the platform can hand a download to the
// system's save dialog, where it cannot write the folder it is bound for.
// Android hands out folders it reads but does not write, so it saves in the
// shared Download folders when the app may write there ("all files access"),
// and through the dialog when it may not or the user asked for it. A browser
// writes in a folder through its own handle (webfs_js.go), and downloads
// through the browser's own download where none is chosen.
var savesThroughDialog = func() bool { return runtime.GOOS == "android" }

// accessIssue is one thing the app is not allowed to do.
type accessIssue struct {
	Kind access.Kind
	// Detail is the error as it was reported.
	Detail string
	// Target is what it was about: a folder, a server's host, a file.
	Target string
	// SiteID is the site it was about.
	SiteID string
}

// raise tells the user of what the app may not do, in one panel; a panel
// already open is not replaced, except by another about permissions.
func (a *App) raise(issues []accessIssue) {
	if len(issues) == 0 {
		return
	}
	cur := a.sheet.GetUntracked()
	switch cur.Kind {
	case sheetNone:
	case sheetPermission:
		for _, is := range cur.Issues {
			dup := false
			for _, n := range issues {
				dup = dup || (n.Kind == is.Kind && n.Target == is.Target)
			}
			if !dup {
				issues = append(issues, is)
			}
		}
	default:
		a.toast(locales.ToastError(a.cx, issues[0].Detail))
		return
	}
	a.openSheet(sheetState{Kind: sheetPermission, Issues: issues})
}

// checkAccess looks at what the app needs: the network, to reach the current
// site, and a folder it can save in. What is missing is put to the user in a
// panel; manual says the user asked, who is told when all is well.
func (a *App) checkAccess(manual bool) {
	s, have := a.site()
	dir := defaultDownloadDir()
	var base, host, siteID string
	if have {
		dir, base, siteID = a.downloadDir(s), s.URL, s.ID
		if u, err := url.Parse(s.URL); err == nil {
			host = u.Host
		}
	}
	web, android := isWeb(), savesThroughDialog()
	ask := have && asksEachDownload(s)
	probe := a.probeDir
	var mu struct {
		found []accessIssue
		left  int
	}
	mu.left = 1
	finish := func() {
		mu.left--
		if mu.left > 0 {
			return
		}
		if len(mu.found) > 0 {
			a.raise(mu.found)
		} else if manual {
			a.toast(locales.ToastAccessOk(a.cx))
		}
	}
	add := func(is accessIssue) { mu.found = append(mu.found, is) }

	if web && have {
		mu.left++
		webFolderLoad(siteID, func(name string) {
			if name == "" {
				a.cx.Post(finish)
				return
			}
			webFolderState(siteID, func(state string) {
				a.cx.Post(func() {
					if state != "granted" {
						add(accessIssue{Kind: access.Browser, Detail: "the browser asks again to write in the folder", Target: name, SiteID: siteID})
					}
					finish()
				})
			})
		})
	}
	go func() {
		var found []accessIssue
		switch {
		case web || (android && ask):
		case android:
			// A folder Android will not let the app write is no fault to
			// raise: the downloads go through the save dialog, as settings say.
			writable := probe(dir) == nil
			a.cx.Post(func() { a.canWrite.Set(writable) })
		default:
			if err := probe(dir); err != nil {
				found = append(found, accessIssue{Kind: access.Folder, Detail: err.Error(), Target: dir, SiteID: siteID})
			}
		}
		if a.vault != nil && !web {
			// A secure store the platform lacked may be there now: the tokens
			// held in memory are kept in it from here on.
			if err := a.vault.Recheck(); errors.Is(err, core.ErrSecretsUnavailable) && onLinux() {
				found = append(found, accessIssue{Kind: access.Keychain, Detail: err.Error()})
			}
		}
		if base != "" && !web {
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			err := access.ProbeNetwork(ctx, apiHTTP, base)
			cancel()
			if p := access.Classify(err); p.Kind == access.Network {
				found = append(found, accessIssue{Kind: access.Network, Detail: p.Detail, Target: host, SiteID: siteID})
			}
		}
		a.cx.Post(func() {
			for _, is := range found {
				add(is)
			}
			finish()
		})
	}()
}

// permissionSheet tells what the app may not do and what the user can do.
func (a *App) permissionSheet(cx *core.Scope, st sheetState) core.Node {
	th := theme.Of(cx)
	body := []core.Node{txt(cx, locales.AccessIntro(cx), theme.TypeSmall, cMuted)}
	for _, is := range st.Issues {
		body = append(body, a.issueCard(cx, th, is))
	}
	return a.sheetFrame(locales.AccessTitle(cx), body, []core.Node{
		ui.Button{Text: locales.AccessLater(cx), Variant: ui.ButtonSecondary, Flex: 1, OnPress: a.closeSheet, TestID: "access-later"},
		ui.Button{Text: locales.AccessRecheck(cx), Flex: 1, TestID: "access-recheck", OnPress: func() {
			a.closeSheet()
			a.checkAccess(true)
		}}})
}

// issueCard is one problem, with what can be done about it.
func (a *App) issueCard(cx *core.Scope, th theme.Colors, is accessIssue) core.Node {
	var title, desc string
	var actions []core.Node
	act := func(label string, svg func() *core.SVG, id string, run func()) {
		actions = append(actions, ui.Button{Text: label, Icon: svg, Variant: ui.ButtonSecondary, OnPress: run, TestID: id})
	}
	openSettings := func() {
		a.cx.OpenPermissionSettings(core.PermissionFullDiskAccess, func(opened bool) {
			if !opened {
				a.toast(locales.ToastSettingsManual(a.cx))
			}
		})
	}
	switch is.Kind {
	case access.Folder:
		title, desc = locales.AccessFolderTitle(cx), locales.AccessFolderDesc(cx, is.Target)
		act(locales.AccessChooseFolder(cx), lucide.FolderOpen, "access-choose", func() { a.closeSheet(); a.pickLocalDir() })
		act(locales.AccessOpenSettings(cx), lucide.Settings, "access-settings", openSettings)
	case access.Network:
		title, desc = locales.AccessNetworkTitle(cx), locales.AccessNetworkDesc(cx, is.Target)
		act(locales.AccessOpenSettings(cx), lucide.Settings, "access-settings", openSettings)
	case access.Server:
		title, desc = locales.AccessServerTitle(cx), locales.AccessServerDesc(cx, is.Detail)
		act(locales.AccessSignIn(cx), lucide.LogIn, "access-signin", func() {
			if is.SiteID != "" && is.SiteID != a.siteID.GetUntracked() {
				a.switchSite(is.SiteID)
			}
			a.openSheet(sheetState{Kind: sheetSignIn})
		})
	case access.Keychain:
		title, desc = locales.AccessKeychainTitle(cx), locales.AccessKeychainDesc(cx)
	case access.Notifications:
		title, desc = locales.AccessNotificationsTitle(cx), locales.AccessNotificationsDesc(cx)
		if is.Detail == "guide" || is.Detail == "" {
			act(locales.AccessAllowNotifications(cx), lucide.Bell, "access-notify-allow", func() {
				a.closeSheet()
				a.requestNotifyPermission(func(perm core.NotificationPermission) {
					switch perm {
					case core.NotificationsGranted, core.NotificationsUnavailable:
						a.toast(locales.ToastNotifyReady(a.cx))
					case core.NotificationsDenied:
						a.raise([]accessIssue{{Kind: access.Notifications, Detail: "denied"}})
					}
				})
			})
		} else {
			act(locales.AccessOpenSettings(cx), lucide.Settings, "access-settings", func() {
				// Notification permission has no dedicated OpenPermissionSettings
				// key; guide the user to the system app settings page.
				a.toast(locales.ToastSettingsManual(a.cx))
			})
		}
	default:
		title, desc = locales.AccessBrowserTitle(cx), locales.AccessBrowserDesc(cx, is.Detail)
		siteID := is.SiteID
		if siteID == "" {
			siteID = a.siteID.GetUntracked()
		}
		if webFolderName(siteID) != "" {
			act(locales.AccessAllowFolder(cx), lucide.ShieldCheck, "access-allow", func() {
				webFolderAllow(siteID, func(granted bool, err error) {
					a.cx.Post(func() {
						if err != nil || !granted {
							a.toast(locales.ToastError(a.cx, locales.AccessBrowserTitle(a.cx)))
							return
						}
						a.closeSheet()
						a.toast(locales.ToastFolderAllowed(a.cx))
					})
				})
			})
		}
		act(locales.AccessChooseFolder(cx), lucide.FolderOpen, "access-choose", func() { a.closeSheet(); a.pickLocalDir() })
		act(locales.AccessUseBrowserDownload(cx), lucide.Download, "access-browser", func() {
			webFolderForget(siteID)
			a.webFolders.Update(func(m map[string]string) map[string]string { return withoutKey(m, siteID) })
			a.closeSheet()
			a.toast(locales.ToastBrowserDownloadOn(a.cx))
		})
	}
	return core.View{Role: core.RoleGroup, Label: title, TestID: "access-issue",
		Style:     core.Style{Gap: 10, Padding: core.All(14), BorderWidth: 1, CornerRadius: theme.RadiusMD, BorderCurve: core.BorderCurveContinuous},
		StyleFunc: cardStyle(cx, theme.RadiusMD),
		Children: []core.Node{
			row(10, tile(cx, lucide.ShieldAlert, "", 32, 10), core.View{Style: fillStyle, Children: []core.Node{txt(cx, title, theme.TypeBody.Merge(core.Style{FontWeight: ui.Semibold}), cText)}}),
			txt(cx, desc, theme.TypeCaption, cMuted),
			core.View{Style: core.Style{Direction: core.Row, Wrap: core.Wrap, Gap: 8}, Children: actions},
		}}
}

// withoutKey is m less one key, as a new map.
func withoutKey(m map[string]string, key string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		if k != key {
			out[k] = v
		}
	}
	return out
}
