package app

import (
	"context"
	"io"
	"os"
	"strings"
	"time"

	"github.com/nsgo-dev/nsgo/core"

	"github.com/HW618/openlist-client/src/internal/browse"
	"github.com/HW618/openlist-client/src/internal/openlist"
	"github.com/HW618/openlist-client/src/internal/store"
	"github.com/HW618/openlist-client/src/internal/transfer"
	"github.com/HW618/openlist-client/src/locales"
)

// byParent groups the names of items by the folder they are in.
func byParent(items []browse.Item) map[string][]string {
	m := map[string][]string{}
	for _, it := range items {
		dir := openlist.Parent(it.Path)
		m[dir] = append(m[dir], it.Name)
	}
	return m
}

// refresh reads the folder again.
func (a *App) refresh(announce bool) {
	a.listing.Refetch()
	if announce {
		a.toast(locales.ToastRefreshed(a.cx))
	}
}

// changed runs after a call that changes files: it closes the panel, reads
// the folder again and says what happened.
func (a *App) changed(msg string) func(struct{}, error) {
	return func(_ struct{}, err error) {
		if err != nil {
			a.fail(err)
			return
		}
		a.cx.Batch(func() {
			a.closeSheet()
			a.endSelect()
		})
		a.listing.Refetch()
		if msg != "" {
			a.toast(msg)
		}
	}
}

func (a *App) mkdir(name string) {
	c := a.client()
	if c == nil {
		return
	}
	name = strings.TrimSpace(name)
	full := openlist.Join(a.path.GetUntracked(), name)
	goPost(a.cx, func(ctx context.Context) (struct{}, error) { return struct{}{}, c.Mkdir(ctx, full) },
		a.changed(locales.ToastFolderCreated(a.cx, name)))
}

// newFile creates a text file with body.
func (a *App) newFile(name, body string) {
	c := a.client()
	if c == nil {
		return
	}
	name = strings.TrimSpace(name)
	full := openlist.Join(a.path.GetUntracked(), name)
	goPost(a.cx, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, c.Put(ctx, full, strings.NewReader(body), int64(len(body)))
	}, a.changed(locales.ToastFileCreated(a.cx, name)))
}

func (a *App) rename(it browse.Item, name string) {
	c := a.client()
	if c == nil {
		return
	}
	name = strings.TrimSpace(name)
	goPost(a.cx, func(ctx context.Context) (struct{}, error) { return struct{}{}, c.Rename(ctx, it.Path, name) },
		a.changed(locales.ToastRenamed(a.cx)))
}

// remove deletes items, skipping storage roots.
func (a *App) remove(items []browse.Item) {
	c := a.client()
	if c == nil {
		return
	}
	var ok []browse.Item
	for _, it := range items {
		if !browse.IsMount(it) {
			ok = append(ok, it)
		}
	}
	if len(ok) == 0 {
		a.closeSheet()
		a.toast(locales.ToastMountNoDelete(a.cx))
		return
	}
	groups := byParent(ok)
	goPost(a.cx, func(ctx context.Context) (struct{}, error) {
		for dir, names := range groups {
			if err := c.Remove(ctx, dir, names); err != nil {
				return struct{}{}, err
			}
		}
		return struct{}{}, nil
	}, a.changed(locales.ToastDeleted(a.cx, len(ok))))
}

// move puts items into dst, skipping storage roots and what is there.
func (a *App) move(items []browse.Item, dst string) {
	c := a.client()
	if c == nil {
		return
	}
	var ok []browse.Item
	for _, it := range items {
		if !browse.IsMount(it) && openlist.Parent(it.Path) != dst {
			ok = append(ok, it)
		}
	}
	if len(ok) == 0 {
		a.closeSheet()
		a.toast(locales.ToastMountNoMove(a.cx))
		return
	}
	groups := byParent(ok)
	goPost(a.cx, func(ctx context.Context) (struct{}, error) {
		for dir, names := range groups {
			if err := c.Move(ctx, dir, dst, names); err != nil {
				return struct{}{}, err
			}
		}
		return struct{}{}, nil
	}, a.changed(locales.ToastMoved(a.cx, len(ok), openlist.Base(dst))))
}

// copyLinks puts the direct links of the files among items on the clipboard.
func (a *App) copyLinks(items []browse.Item) {
	c := a.client()
	if c == nil {
		return
	}
	var links []string
	for _, it := range items {
		if !it.IsDir {
			links = append(links, c.DirectLink(it.Path, it.Sign))
		}
	}
	if len(links) == 0 {
		a.toast(locales.ToastFoldersNoDownload(a.cx))
		return
	}
	a.cx.SetClipboard(strings.Join(links, "\n"))
	if len(links) == 1 && len(items) == 1 {
		a.toast(locales.ToastLinkCopied(a.cx, items[0].Name))
		return
	}
	a.toast(locales.ToastLinksCopied(a.cx, len(links)))
}

// download queues the files among items for this device.
func (a *App) download(items []browse.Item) {
	s, ok := a.site()
	if !ok {
		return
	}
	dir := a.downloadDir(s)
	if asksEachDownload(s) {
		dir = ""
	}
	n := 0
	for _, it := range items {
		if it.IsDir {
			continue
		}
		a.engine.Add(transfer.Task{Name: it.Name, Direction: transfer.Download, SiteID: s.ID, SiteName: s.Name,
			RemotePath: it.Path, LocalPath: dir, Total: it.Size})
		n++
	}
	if n == 0 {
		a.toast(locales.ToastFoldersNoDownload(a.cx))
		return
	}
	a.endSelect()
	a.toast(locales.ToastDownloadQueued(a.cx, n))
}

// pickAndUpload asks for files on this device and queues them for the folder
// the Files page shows.
func (a *App) pickAndUpload() {
	s, ok := a.site()
	if !ok {
		a.toast(locales.ToastNoSite(a.cx))
		return
	}
	dest := a.path.GetUntracked()
	a.cx.PickFiles(core.PickFilesOptions{Multiple: true, Title: locales.UploadTitle(a.cx)}, func(files []core.File, err error) {
		if err != nil {
			a.fail(err)
			return
		}
		if len(files) == 0 {
			return
		}
		for _, f := range files {
			key := f.Path
			if key == "" {
				key = "mem:" + store.NewID()
			}
			a.uploadMu.Lock()
			a.uploadFile[key] = f
			a.uploadMu.Unlock()
			a.engine.Add(transfer.Task{Name: f.Name, Direction: transfer.Upload, SiteID: s.ID, SiteName: s.Name,
				RemotePath: openlist.Join(dest, f.Name), LocalPath: key, Total: f.Size})
		}
		a.closeSheet()
		a.toast(locales.ToastUploadQueued(a.cx, len(files)))
	})
}

// releaseUploads lets go of the files picked for uploads that are over: done,
// or taken off the list. A file the platform copied into the app for the
// upload is deleted with it. Those that are to run, run again or be retried
// are kept. It is for the engine's goroutine.
func (a *App) releaseUploads(snap []transfer.Task) {
	live := map[string]bool{}
	var done []string
	for _, t := range snap {
		if t.Direction != transfer.Upload || t.LocalPath == "" {
			continue
		}
		if t.Status == transfer.Done {
			done = append(done, t.LocalPath)
			continue
		}
		live[t.LocalPath] = true
		a.uploadSeen[t.LocalPath] = true
	}
	// A file queued twice may have one of them still to go.
	over := map[string]bool{}
	for _, k := range done {
		if !live[k] {
			over[k] = true
		}
	}
	for k := range a.uploadSeen {
		if !live[k] {
			over[k] = true
			delete(a.uploadSeen, k)
		}
	}
	if len(over) == 0 {
		return
	}
	a.uploadMu.Lock()
	var temp []string
	for k := range over {
		if f, ok := a.uploadFile[k]; ok {
			delete(a.uploadFile, k)
			if f.Temporary && f.Path != "" {
				temp = append(temp, f.Path)
			}
		}
	}
	a.uploadMu.Unlock()
	if len(temp) > 0 {
		go func() {
			for _, p := range temp {
				_ = os.Remove(p)
			}
		}()
	}
}

// openLocal opens the source of an upload.
func (a *App) openLocal(key string) (io.ReadCloser, int64, error) {
	a.uploadMu.Lock()
	f, ok := a.uploadFile[key]
	a.uploadMu.Unlock()
	if ok {
		r, err := f.Open()
		return r, f.Size, err
	}
	file, err := os.Open(key)
	if err != nil {
		return nil, 0, err
	}
	st, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, err
	}
	return file, st.Size(), nil
}

// addOffline queues links for the server to download into dir.
func (a *App) addOffline(urls []string, tool, dir string) {
	c := a.client()
	s, ok := a.site()
	if c == nil || !ok {
		return
	}
	goPost(a.cx, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, c.AddOfflineDownload(ctx, dir, urls, openlist.OfflineTool(tool))
	}, func(_ struct{}, err error) {
		if err != nil {
			a.fail(err)
			return
		}
		a.updateSite(s.ID, func(st *store.Site) { st.Settings.PushRecent(dir) })
		a.cx.Batch(func() {
			a.offlineURLs.Set("")
			a.closeSheet()
		})
		a.toast(locales.ToastOfflineAdded(a.cx, dir))
		a.refreshOffline()
	})
}

// testSite signs in to a site as the app does, with the token it has, and
// finds out who the site takes the app for: that is what makes a site usable,
// not that its address answers.
func (a *App) testSite(s store.Site) {
	set := func(st siteStatus) {
		m := maps_status_clone(a.status.GetUntracked())
		m[s.ID] = st
		a.status.Set(m)
	}
	set(siteStatus{Checking: true})
	c := a.clientFor(s)
	type result struct {
		user openlist.User
		took time.Duration
	}
	goPost(a.cx, func(ctx context.Context) (result, error) {
		ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		t0 := time.Now()
		u, err := c.Me(ctx)
		return result{u, time.Since(t0)}, err
	}, func(r result, err error) {
		st := siteStatus{Latency: r.took, User: r.user.Username, Role: r.user.Role}
		name := s.Name
		lat := browse.Latency(r.took)
		switch {
		case err == nil && r.user.Disabled:
			st.Auth = authDisabled
			a.toast(locales.ToastSiteDisabled(a.cx, name, r.user.Username))
		case err == nil && (r.user.Role == openlist.RoleGuest || c.Token == ""):
			st.Auth = authGuest
			a.toast(locales.ToastSiteGuest(a.cx, name, lat))
		case err == nil:
			st.Auth = authOK
			a.toast(locales.ToastSiteAuthOk(a.cx, name, r.user.Username, lat))
		case openlist.Unauthorized(err):
			st.Auth = authNeedLogin
			a.toast(locales.ToastSiteNeedLogin(a.cx, name))
		default:
			st = siteStatus{Failed: true, Err: errText(err)}
			a.toast(locales.ToastSiteFailReason(a.cx, name, st.Err))
		}
		set(st)
	})
}

func maps_status_clone(m map[string]siteStatus) map[string]siteStatus {
	out := make(map[string]siteStatus, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	return out
}

// addSite saves a site, signing in when an account is given, and makes it the
// current one.
func (a *App) addSite(name, rawURL, token, user, pass string, done func(error)) {
	u, err := store.NormalizeURL(rawURL)
	if err != nil {
		done(err)
		return
	}
	finish := func(tok string) {
		id := store.NewID()
		if tok != "" {
			_ = a.secrets.Set(id, tok)
			a.logins.Update(func(n int) int { return n + 1 })
		}
		site := store.Site{ID: id, Name: strings.TrimSpace(name), URL: u}
		a.cx.Batch(func() {
			a.saveSites(append(append([]store.Site(nil), a.sites.GetUntracked()...), site))
			a.switchSite(id)
			a.closeSheet()
		})
		a.toast(locales.ToastSiteConnected(a.cx, site.Name))
		done(nil)
	}
	token, user = strings.TrimSpace(token), strings.TrimSpace(user)
	if token != "" || user == "" {
		finish(token)
		return
	}
	c := &openlist.Client{BaseURL: u, HTTP: apiHTTP}
	goPost(a.cx, func(ctx context.Context) (string, error) { return c.Login(ctx, user, pass) },
		func(tok string, err error) {
			if err != nil {
				done(err)
				return
			}
			finish(tok)
		})
}

// signedIn reports whether the app keeps a login for a site.
func (a *App) signedIn(id string) bool {
	a.logins.Get()
	tok, ok := a.secrets.Get(id)
	return ok && tok != ""
}

// signOut deletes the login kept for a site. It is the only thing that does,
// short of removing the site: a token that stops working, a restart and a
// closed app leave it where it is.
func (a *App) signOut(s store.Site) {
	_ = a.secrets.Remove(s.ID)
	a.cx.Batch(func() {
		a.logins.Update(func(n int) int { return n + 1 })
		m := maps_status_clone(a.status.GetUntracked())
		delete(m, s.ID)
		a.status.Set(m)
	})
	a.syncClients()
	if a.siteID.GetUntracked() == s.ID {
		a.listing.Refetch()
	}
	a.toast(locales.ToastSignedOut(a.cx, s.Name))
}

// signIn gets a token for the current site with an account.
func (a *App) signIn(user, pass string, done func(error)) {
	s, ok := a.site()
	if !ok {
		return
	}
	c := &openlist.Client{BaseURL: s.URL, HTTP: apiHTTP}
	user = strings.TrimSpace(user)
	goPost(a.cx, func(ctx context.Context) (string, error) { return c.Login(ctx, user, pass) },
		func(tok string, err error) {
			if err != nil {
				done(err)
				return
			}
			_ = a.secrets.Set(s.ID, tok)
			a.logins.Update(func(n int) int { return n + 1 })
			a.syncClients()
			a.closeSheet()
			a.listing.Refetch()
			a.toast(locales.ToastSignedIn(a.cx))
			done(nil)
		})
}
