package app

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/desktop"
	"github.com/nsgo-dev/nsgo/ui"
	"github.com/nsgo-dev/nsgo/widget"

	"github.com/HW618/openlist-client/src/internal/access"
	"github.com/HW618/openlist-client/src/internal/browse"
	"github.com/HW618/openlist-client/src/internal/openlist"
	"github.com/HW618/openlist-client/src/internal/store"
	"github.com/HW618/openlist-client/src/internal/transfer"
	"github.com/HW618/openlist-client/src/locales"
)

// The tabs, by index.
const (
	tabFiles = iota
	tabTransfers
	tabSettings
)

// sheetKind is which panel is open.
type sheetKind uint8

const (
	sheetNone sheetKind = iota
	sheetActions
	sheetSites
	sheetAddSite
	sheetSignIn
	sheetQuick
	sheetMkdir
	sheetNewFile
	sheetUpload
	sheetOffline
	sheetPicker
	sheetDelete
	sheetRename
	sheetPermission
	sheetLocalDir
	sheetQuitConfirm
)

// pickerSpec is a request to choose a folder on the server.
type pickerSpec struct {
	Title string
	// Start is the folder the picker opens in.
	Start string
	// AllowRoot lets the root itself be chosen; otherwise a storage or a
	// folder inside one must be.
	AllowRoot bool
	// ConfirmText is the label of the button that chooses the folder shown.
	ConfirmText string
	// Exclude is a folder that cannot be chosen, where a move starts.
	Exclude string
	// ExcludeTrees are folder paths that cannot be chosen (nor their descendants).
	ExcludeTrees []string
	// Confirm hears the chosen folder.
	Confirm func(dir string)
	// ConfirmWith hears the chosen folder and a completion callback for busy state.
	ConfirmWith func(dir string, done func(error))
	// Back is the panel to return to when the picker is left.
	Back sheetState
}

// sheetState is the open panel and what it is about.
type sheetState struct {
	Kind   sheetKind
	Item   browse.Item
	Items  []browse.Item
	Picker *pickerSpec
	// Issues are what the app is not allowed to do, for the permission panel.
	Issues []accessIssue
}

// listResult is the content of a folder.
type listResult struct {
	Dir      string
	Items    []browse.Item
	Provider string
}

// listReq says which folder to read, with the client to read it.
type listReq struct {
	Client *openlist.Client
	Dir    string
}

// searchReq says what to look for below Dir: the folder the Files page shows,
// or the root for a search of the whole server.
type searchReq struct {
	Client *openlist.Client
	Dir    string
	Query  string
}

// searchKey names a search: what is looked for, and below which folder.
type searchKey struct {
	Dir   string
	Query string
}

// searchHits are the items a search found, with the search they are of, so
// that the hits of an earlier search are never shown for a later one.
type searchHits struct {
	Key   searchKey
	Items []browse.Item
}

// searchBudget is how long a search without a server index looks.
const searchBudget = 20 * time.Second

// previewData is what the preview shows of a file.
type previewData struct {
	Object openlist.Object
	Text   string
	Large  bool
}

// transferBannerState is the inline notice shown inside the Transfers page.
type transferBannerState struct {
	Visible      bool
	Ongoing      bool
	Heading      string
	Status       string
	Title        string
	Body         string
	// Progress is 0–100 for ui.Progress (not the Live Activity 0–1 fraction).
	Progress     float32
	ShowProgress bool
}

// siteStatus is what the last test of a site found.
type siteStatus struct {
	Checking bool
	Latency  time.Duration
	// Failed is a site that could not be reached; Err says why.
	Failed bool
	Err    string
	// Auth is what the site said of the account the app uses with it.
	Auth authState
	User string
	Role int
}

// authState is how the account of a site is received by the site.
type authState uint8

const (
	authUnknown authState = iota
	// authOK is a signed-in account.
	authOK
	// authGuest is no account: the site lets guests in.
	authGuest
	// authNeedLogin is a site that wants a sign-in the app has not got, or no
	// longer has, as a token expires.
	authNeedLogin
	// authDisabled is an account the site has turned off.
	authDisabled
)

// apiHTTP is the client API calls go through: calls end instead of hanging.
var apiHTTP = &http.Client{Timeout: 30 * time.Second, CheckRedirect: openlist.CheckRedirect}

// App is the state of the whole app.
type App struct {
	cx      *core.Scope
	secrets store.Secrets
	toaster *ui.Toaster

	tab *core.Signal[int]

	// Saved data. Each has a signal the screens read and a store it is
	// written through to.
	sites      *core.Signal[[]store.Site]
	siteID     *core.Signal[string]
	prefs      *core.Signal[store.Prefs]
	sitesStore *core.Persisted[[]store.Site]
	idStore    *core.Persisted[string]
	prefStore  *core.Persisted[store.Prefs]
	taskStore  *core.Persisted[[]transfer.Task]

	// The Files page.
	path       *core.Signal[string]
	view       *core.Signal[store.ViewMode]
	sortKey    *core.Signal[store.SortKey]
	sortDesc   *core.Signal[bool]
	query      *core.Signal[string]
	global     *core.Signal[bool]
	globalText *core.Signal[string]
	stopQuery  func() bool
	selecting  *core.Signal[bool]
	selected   *core.Signal[map[string]bool]
	listing    *core.Resource[listResult]
	found      *core.Resource[searchHits]
	partial    *core.Signal[*searchHits]
	items      *core.Memo[[]browse.Item]

	// Panels.
	sheet      *core.Signal[sheetState]
	preview    *core.Signal[*browse.Item]
	previewRes *core.Resource[previewData]
	pickPath   *core.Signal[string]
	pickList   *core.Resource[listResult]
	tools      *core.Resource[[]openlist.OfflineTool]

	// Offline download form, kept while the folder picker is open.
	offlineURLs *core.Signal[string]
	offlineDir  *core.Signal[string]
	offlineTool *core.Signal[string]

	// The floating button.
	fabPos   *core.Signal[browse.Pos]
	fabSide  *core.Signal[browse.Side]
	fabOpen  *core.Signal[bool]
	fabBox   *core.Signal[core.Size]
	fabDrag  *core.Signal[bool]
	fabStart browse.Pos

	// Transfers.
	engine             *transfer.Engine
	tasks              *core.Signal[[]transfer.Task]
	transferBanner     *core.Signal[transferBannerState]
	bannerDismissedSig string
	filter             *core.Signal[string]
	offline            *core.Signal[offlineState]
	webFolders         *core.Signal[map[string]string]
	probeDir   func(string) error
	// canWrite is whether the folder of the current site takes files, as the
	// last look at it found, where Android tells it in settings.
	canWrite *core.Signal[bool]
	// logins changes whenever a token is kept or removed, for what shows
	// whether a site is signed in.
	logins *core.Signal[int]
	// ready is whether the tokens are read from the secure store, which the
	// folders wait for, so that a site is not asked for before the app has
	// the account to ask as.
	ready *core.Signal[bool]
	// readyCh is closed once preloadTokens finishes (or immediately when no
	// vault is used), so background goroutines can wait for tokens without
	// touching signals.
	readyCh chan struct{}
	// vault is the secure store of the tokens, nil where tests give another.
	vault *vault
	// keychainTold is whether the user was told this run of a secure store
	// the platform lacks.
	keychainTold bool
	notified      map[string]bool
	offlineBusy   bool
	polling       bool
	lastSig       string
	lastDone      int
	lastSavedDone int64
	lastTaskSave  time.Time
	uploadMu      sync.Mutex
	uploadFile    map[string]core.File
	uploadSeen    map[string]bool // keys of uploads the task list has shown; the engine goroutine's
	wakeMu        sync.Mutex
	wakeCh        chan struct{}
	resumeMu      sync.Mutex
	resumableFail map[string]bool
	inBackground  atomic.Bool
	// taskStatus and offlineSeen track prior statuses so Done/Failed notify once.
	taskStatus           map[string]transfer.Status
	offlineSeen          map[string]int
	notifyCount          int
	notifyOK             int
	notifyFail           int
	notifySeq            int
	notifyShown          []string
	notifyItemKeys       map[string]bool // active notification item key -> ok
	notifySummaryShown   bool
	notifyPermChecking   bool
	progressAct          *widget.Activity
	progressUnsupported  bool
	progressPermSyncAt   time.Time
	progressSig          string
	progressDismissedSig string
	progressLastKey      string
	progressLastPct      int
	progressLastAt       time.Time
	progressTimerPending bool
	progressNotifyShown  bool
	progressNotifySig    string
	progressNotifyKey    string
	progressNotifyPct    int
	progressNotifyAt     time.Time
	progressNotifyTimer  bool
	releaseAwake         func()

	// Desktop tray and window lifecycle.
	appScope    *core.Scope
	mainWin     *desktop.Window
	tray        *desktop.Tray
	pendingQuit func(bool)

	// Settings.
	status       *core.Signal[map[string]siteStatus]
	sitesOpen    *core.Signal[bool]
	cacheVersion *core.Signal[int]

	mu         sync.Mutex
	clients    map[string]*openlist.Client
	cache      map[string]listResult
	cacheOrder []string
}

// sigAlways is a signal that tells its readers on every write, for values
// that cannot be compared.
func sigAlways[T any](cx *core.Scope, v T) *core.Signal[T] {
	return core.NewSignalFunc(cx, v, func(a, b T) bool { return false })
}

// newApp builds the state in cx, which is the root scope.
func newApp(cx *core.Scope, secrets store.Secrets) *App {
	a := &App{cx: cx, secrets: secrets, clients: map[string]*openlist.Client{}, cache: map[string]listResult{},
		uploadFile: map[string]core.File{}, uploadSeen: map[string]bool{}, toaster: ui.NewToaster(cx),
		resumableFail: map[string]bool{}, notifyItemKeys: map[string]bool{}}

	a.tab = core.NewSignal(cx, tabFiles)

	a.sitesStore = core.NewPersisted[[]store.Site](cx, "sites", nil, core.PersistOptions{Version: 1})
	a.idStore = core.NewPersisted(cx, "currentSite", "", core.PersistOptions{Version: 1})
	a.prefStore = core.NewPersisted(cx, "prefs", store.DefaultPrefs(), core.PersistOptions{Version: 1})
	a.taskStore = core.NewPersisted[[]transfer.Task](cx, "transfers", nil, core.PersistOptions{Version: 1})

	sites := slices.Clone(a.sitesStore.Get())
	sitesRelocated := false
	for i := range sites {
		if d := sites[i].Settings.LocalDir; d != "" && d != localDirAsk {
			if moved := relocateSavedPath(d); moved != d {
				sites[i].Settings.LocalDir = moved
				sitesRelocated = true
			}
		}
	}
	if sitesRelocated {
		_ = a.sitesStore.Set(sites)
	}
	a.sites = sigAlways(cx, sites)
	a.siteID = core.NewSignal(cx, a.idStore.Get())
	prefs := a.prefStore.Get().Fix()
	a.prefs = core.NewSignal(cx, prefs)
	if _, saved := cx.Storage().GetItem("prefs"); saved {
		if prefs.Dark {
			cx.SetAppearance(core.AppearanceDark)
		} else {
			cx.SetAppearance(core.AppearanceLight)
		}
	}
	if _, ok := a.site(); !ok && len(a.sites.GetUntracked()) > 0 {
		a.siteID.Set(a.sites.GetUntracked()[0].ID)
	}
	a.syncClients()

	start := "/"
	if s, ok := a.site(); ok && s.Settings.FilesPath != "" {
		start = s.Settings.FilesPath
	}
	a.path = core.NewSignal(cx, openlist.Clean(start))
	a.view = core.NewSignal(cx, prefs.View)
	a.sortKey = core.NewSignal(cx, prefs.Sort)
	a.sortDesc = core.NewSignal(cx, prefs.SortDesc)
	a.query = core.NewSignal(cx, "")
	a.global = core.NewSignal(cx, false)
	a.globalText = core.NewSignal(cx, "")
	a.selecting = core.NewSignal(cx, false)
	a.selected = sigAlways(cx, map[string]bool{})

	a.sheet = sigAlways(cx, sheetState{})
	a.preview = core.NewSignalFunc[*browse.Item](cx, nil, func(x, y *browse.Item) bool { return false })
	a.pickPath = core.NewSignal(cx, "/")
	a.offlineURLs = core.NewSignal(cx, "")
	a.offlineDir = core.NewSignal(cx, "")
	a.offlineTool = core.NewSignal(cx, "")

	a.fabPos = core.NewSignalFunc(cx, browse.Pos{X: -1}, func(x, y browse.Pos) bool { return x == y })
	a.fabSide = core.NewSignal(cx, browse.SideNone)
	a.fabOpen = core.NewSignal(cx, false)
	a.fabBox = core.NewSignalFunc(cx, core.Size{}, func(x, y core.Size) bool { return x == y })
	a.fabDrag = core.NewSignal(cx, false)

	a.filter = core.NewSignal(cx, "all")
	a.status = sigAlways(cx, map[string]siteStatus{})
	a.sitesOpen = core.NewSignal(cx, false)
	a.cacheVersion = core.NewSignal(cx, 0)

	a.tasks = sigAlways(cx, []transfer.Task(nil))
	a.transferBanner = core.NewSignal(cx, transferBannerState{})
	a.offline = sigAlways(cx, offlineState{})
	a.webFolders = sigAlways(cx, map[string]string{})
	a.probeDir = access.ProbeDir
	a.canWrite = core.NewSignal(cx, true)
	a.logins = core.NewSignal(cx, 0)
	a.ready = core.NewSignal(cx, true)
	a.readyCh = make(chan struct{})
	if v, ok := secrets.(*vault); ok {
		a.vault = v
		a.preloadTokens()
	} else {
		close(a.readyCh)
	}
	a.notified = map[string]bool{}
	a.taskStatus = map[string]transfer.Status{}
	a.offlineSeen = map[string]int{}
	if isWeb() {
		for _, st := range a.sites.GetUntracked() {
			id := st.ID
			webFolderLoad(id, func(name string) {
				if name != "" {
					cx.Post(func() {
						a.webFolders.Update(func(m map[string]string) map[string]string { out := withoutKey(m, id); out[id] = name; return out })
					})
				}
			})
		}
	}

	a.buildResources()
	initialTasks := slices.Clone(a.taskStore.Get())
	tasksRelocated := false
	var autoResumeDownloads []string
	for i := range initialTasks {
		t := &initialTasks[i]
		if t.LocalPath != "" {
			if moved := relocateSavedPath(t.LocalPath); moved != t.LocalPath {
				t.LocalPath = moved
				tasksRelocated = true
			}
		}
		if t.Direction == transfer.Upload && t.Status != transfer.Done && strings.HasPrefix(t.LocalPath, "mem:") {
			t.Status = transfer.Failed
			t.Err = "upload source is no longer available after restart"
			continue
		}
		if t.Direction == transfer.Download && t.Status != transfer.Done {
			if sz := a.existingPartSize(*t); sz > 0 {
				if t.Total > 0 && sz > t.Total {
					sz = t.Total
				}
				t.Done = sz
			}
			if t.Status == transfer.Active {
				autoResumeDownloads = append(autoResumeDownloads, t.ID)
			}
		}
	}
	if tasksRelocated {
		_ = a.taskStore.Set(initialTasks)
	}
	a.engine = transfer.New(a.runner, a.transfersChanged, initialTasks)
	a.lastDone = -1
	a.syncTasks()
	a.bindNotifications()
	a.bindDeepLinks()
	a.bindLifecycleAndNetwork()
	if len(autoResumeDownloads) > 0 {
		a.cx.Post(func() {
			for _, id := range autoResumeDownloads {
				a.engine.Resume(id)
			}
		})
	}
	if a.prefs.GetUntracked().NotifyMaster() {
		a.cx.Post(a.pollOffline)
	}
	return a
}

// buildResources makes what the screens load: folders, search, previews and
// the offline downloaders.
func (a *App) buildResources() {
	cx := a.cx
	a.listing = core.NewResource(cx,
		func() (listReq, bool) {
			c := a.client()
			if c == nil {
				return listReq{}, false
			}
			return listReq{Client: c, Dir: a.path.Get()}, true
		},
		func(ctx context.Context, r listReq, info core.ResourceInfo) (listResult, error) {
			return a.fetchDir(ctx, r, info.Refetching)
		})
	a.partial = core.NewSignal[*searchHits](cx, nil)
	a.found = core.NewResource(cx,
		func() (searchReq, bool) {
			q := strings.TrimSpace(a.globalText.Get())
			c := a.client()
			if c == nil || q == "" {
				return searchReq{}, false
			}
			dir := "/"
			if !a.global.Get() {
				dir = a.path.Get()
			}
			return searchReq{Client: c, Dir: dir, Query: q}, true
		},
		func(ctx context.Context, r searchReq, _ core.ResourceInfo) (searchHits, error) {
			key := searchKey{Dir: r.Dir, Query: r.Query}
			var last time.Time
			items, err := searchBelow(ctx, r, func(items []browse.Item) {
				// the folders that are read tell what they found as they go,
				// not more often than the screen needs
				if now := time.Now(); now.Sub(last) > 150*time.Millisecond {
					last = now
					a.cx.Post(func() { a.partial.Set(&searchHits{Key: key, Items: items}) })
				}
			})
			return searchHits{Key: key, Items: items}, err
		})
	a.items = core.NewMemoFunc(cx, func() []browse.Item {
		var base []browse.Item
		q := strings.TrimSpace(a.query.Get())
		switch {
		case q == "":
			base = a.currentItems()
		case a.global.Get():
			// only what is found for this search: nothing of an earlier one
			base = a.searchItems(q)
		default:
			// the folder's own items at once, then what the search finds
			// below it
			base = browse.Merge(browse.Filter(a.currentItems(), q), a.searchItems(q))
		}
		return browse.Sort(slices.Clone(base), a.sortKey.Get(), a.sortDesc.Get())
	}, func(x, y []browse.Item) bool { return false })

	a.previewRes = core.NewResource(cx,
		func() (previewReq, bool) {
			it := a.preview.Get()
			c := a.client()
			if it == nil || c == nil {
				return previewReq{}, false
			}
			return previewReq{Client: c, Item: *it}, true
		},
		func(ctx context.Context, r previewReq, _ core.ResourceInfo) (previewData, error) {
			return fetchPreview(ctx, r)
		})
	a.pickList = core.NewResource(cx,
		func() (listReq, bool) {
			c := a.client()
			if c == nil || a.sheet.Get().Kind != sheetPicker {
				return listReq{}, false
			}
			return listReq{Client: c, Dir: a.pickPath.Get()}, true
		},
		func(ctx context.Context, r listReq, _ core.ResourceInfo) (listResult, error) {
			return a.fetchDir(ctx, r, false)
		})
	a.tools = core.NewResource(cx,
		func() (*openlist.Client, bool) {
			c := a.client()
			return c, c != nil && a.sheet.Get().Kind == sheetOffline
		},
		func(ctx context.Context, c *openlist.Client, _ core.ResourceInfo) ([]openlist.OfflineTool, error) {
			return c.OfflineTools(ctx)
		})
}

const maxDirCache = 128

// fetchDir reads a folder, keeping what it read for the next visit.
func (a *App) fetchDir(ctx context.Context, r listReq, refresh bool) (listResult, error) {
	l, err := r.Client.List(ctx, r.Dir, refresh)
	if err != nil {
		return listResult{}, err
	}
	res := listResult{Dir: r.Dir, Provider: l.Provider, Items: make([]browse.Item, len(l.Entries))}
	for i, e := range l.Entries {
		res.Items[i] = browse.ItemFrom(r.Dir, e)
	}
	key := r.Client.BaseURL + "|" + r.Dir
	a.mu.Lock()
	if _, exists := a.cache[key]; !exists {
		if len(a.cacheOrder) >= maxDirCache {
			evict := a.cacheOrder[0]
			a.cacheOrder = a.cacheOrder[1:]
			delete(a.cache, evict)
		}
		a.cacheOrder = append(a.cacheOrder, key)
	}
	a.cache[key] = res
	a.mu.Unlock()
	return res, nil
}

// cached is what the last read of dir on the current site found.
func (a *App) cached(dir string) (listResult, bool) {
	c := a.client()
	if c == nil {
		return listResult{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	r, ok := a.cache[c.BaseURL+"|"+dir]
	return r, ok
}

// cacheStats are how many folders are kept and roughly how many bytes.
func (a *App) cacheStats() (folders int, bytes int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, r := range a.cache {
		folders++
		bytes += int64(len(r.Items)) * 160
	}
	return
}

// clearCache forgets every folder kept.
func (a *App) clearCache() {
	a.mu.Lock()
	a.cache = map[string]listResult{}
	a.cacheOrder = nil
	a.mu.Unlock()
	a.cacheVersion.Update(func(n int) int { return n + 1 })
	a.listing.Refetch()
}

// currentItems are the items of the folder the Files page shows: what was
// read, or what was kept of an earlier visit while it is read again.
func (a *App) currentItems() []browse.Item {
	dir := a.path.Get()
	if r, ok := a.listing.Get(); ok && r.Dir == dir {
		return r.Items
	}
	a.cacheVersion.Get()
	if r, ok := a.cached(dir); ok {
		return r.Items
	}
	return nil
}

// currentKey is the search the search field asks for, once the text is
// committed.
func (a *App) currentKey() (searchKey, bool) {
	q := strings.TrimSpace(a.globalText.Get())
	if q == "" || strings.TrimSpace(a.query.Get()) != q {
		return searchKey{}, false
	}
	dir := "/"
	if !a.global.Get() {
		dir = a.path.Get()
	}
	return searchKey{Dir: dir, Query: q}, true
}

// searchItems are the items found for the search that is asked for now: the
// answer of the search, or what its crawl has found so far; none while
// nothing has been found for it.
func (a *App) searchItems(q string) []browse.Item {
	key, ok := a.currentKey()
	if !ok || key.Query != q {
		return nil
	}
	if res, ok := a.found.Get(); ok && res.Key == key {
		return res.Items
	}
	if p := a.partial.Get(); p != nil && p.Key == key {
		return p.Items
	}
	return nil
}

// searchBelow finds the entries below r.Dir whose names match: the server's
// search index answers when it has one, else the folders are read, as far as
// searchBudget allows, and progress hears what they have shown so far.
func searchBelow(ctx context.Context, r searchReq, progress func([]browse.Item)) ([]browse.Item, error) {
	hits, err := r.Client.Search(ctx, r.Dir, r.Query)
	if err == nil {
		items := make([]browse.Item, len(hits))
		for i, h := range hits {
			items[i] = browse.ItemFromResult(h)
		}
		return items, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	// No index, a search that is switched off, or an index that does not
	// know the entries yet: look through the folders.
	cctx, cancel := context.WithTimeout(ctx, searchBudget)
	defer cancel()
	list := func(ctx context.Context, dir string) ([]browse.Item, error) {
		l, err := r.Client.List(ctx, dir, false)
		if err != nil {
			return nil, err
		}
		items := make([]browse.Item, len(l.Entries))
		for i, e := range l.Entries {
			items[i] = browse.ItemFrom(dir, e)
		}
		return items, nil
	}
	items, cerr := browse.Crawl(cctx, list, r.Dir, r.Query, browse.DefaultCrawl, progress)
	if cerr != nil {
		if err != nil {
			return nil, err
		}
		return nil, cerr
	}
	return items, nil
}

// searching reports whether the search field holds something.
func (a *App) searching() bool { return strings.TrimSpace(a.query.Get()) != "" }

// ---- sites ----

// site is the current site.
func (a *App) site() (store.Site, bool) {
	id := a.siteID.Get()
	for _, s := range a.sites.Get() {
		if s.ID == id {
			return s, true
		}
	}
	return store.Site{}, false
}

func (a *App) siteByID(id string) (store.Site, bool) {
	for _, s := range a.sites.Get() {
		if s.ID == id {
			return s, true
		}
	}
	return store.Site{}, false
}

// client is the client of the current site, nil when there is none.
func (a *App) client() *openlist.Client {
	if !a.ready.Get() {
		return nil
	}
	s, ok := a.site()
	if !ok {
		return nil
	}
	if c := a.clientByID(s.ID); c != nil {
		return c
	}
	return a.clientFor(s)
}

// clientFor makes the client of a site with the token kept for it.
func (a *App) clientFor(s store.Site) *openlist.Client {
	tok, _ := a.secrets.Get(s.ID)
	return &openlist.Client{BaseURL: s.URL, Token: tok, HTTP: apiHTTP}
}

// syncClients remembers a client per site for the transfers, which run off
// the engine's goroutine and so cannot read the signals.
func (a *App) syncClients() {
	m := map[string]*openlist.Client{}
	for _, s := range a.sites.GetUntracked() {
		m[s.ID] = a.clientFor(s)
	}
	a.mu.Lock()
	a.clients = m
	a.mu.Unlock()
}

func (a *App) clientByID(id string) *openlist.Client {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.clients[id]
}

// saveSites replaces the saved sites.
func (a *App) saveSites(v []store.Site) {
	a.sites.Set(v)
	_ = a.sitesStore.Set(v)
	a.syncClients()
}

// updateSite changes the site with id.
func (a *App) updateSite(id string, change func(*store.Site)) {
	sites := slices.Clone(a.sites.GetUntracked())
	for i := range sites {
		if sites[i].ID == id {
			sites[i].Settings.RecentOffline = slices.Clone(sites[i].Settings.RecentOffline)
			change(&sites[i])
		}
	}
	a.saveSites(sites)
}

// switchSite makes id the current site and opens its default folder.
func (a *App) switchSite(id string) {
	s, ok := a.siteByID(id)
	if !ok {
		return
	}
	a.cx.Batch(func() {
		a.siteID.Set(id)
		_ = a.idStore.Set(id)
		a.resetBrowse(s.Settings.FilesPath)
		a.offline.Set(offlineState{})
	})
	if a.tab.GetUntracked() == tabTransfers || a.prefs.GetUntracked().NotifyMaster() {
		a.pollOffline()
	}
}

// resetBrowse goes to dir, or the root, and clears the search and the
// selection.
func (a *App) resetBrowse(dir string) {
	if dir == "" {
		dir = "/"
	}
	a.query.Set("")
	a.globalText.Set("")
	a.endSelect()
	a.path.Set(openlist.Clean(dir))
}

// removeSite forgets a site, its token, cached folders, web handle, and tasks.
func (a *App) removeSite(id string) {
	var removedURL string
	var rest []store.Site
	for _, s := range a.sites.GetUntracked() {
		if s.ID != id {
			rest = append(rest, s)
		} else {
			removedURL = s.URL
		}
	}
	_ = a.secrets.Remove(id)
	if isWeb() {
		webFolderForget(id)
		a.webFolders.Update(func(m map[string]string) map[string]string { return withoutKey(m, id) })
	}
	for _, t := range a.engine.Snapshot() {
		if t.SiteID == id {
			a.removeTask(t)
		}
	}
	a.syncTasks()
	if removedURL != "" {
		prefix := removedURL + "|"
		a.mu.Lock()
		keptOrder := a.cacheOrder[:0]
		for _, k := range a.cacheOrder {
			if strings.HasPrefix(k, prefix) {
				delete(a.cache, k)
			} else {
				keptOrder = append(keptOrder, k)
			}
		}
		a.cacheOrder = keptOrder
		a.mu.Unlock()
	}
	a.logins.Update(func(n int) int { return n + 1 })
	a.cx.Batch(func() {
		stMap := maps_status_clone(a.status.GetUntracked())
		delete(stMap, id)
		a.status.Set(stMap)
		a.saveSites(rest)
		if a.siteID.GetUntracked() == id {
			next := ""
			if len(rest) > 0 {
				next = rest[0].ID
			}
			a.siteID.Set(next)
			_ = a.idStore.Set(next)
			a.offline.Set(offlineState{})
			if next != "" {
				s, _ := a.siteByID(next)
				a.resetBrowse(s.Settings.FilesPath)
			} else {
				a.resetBrowse("/")
			}
		}
	})
}

// ---- navigation ----

// goTo opens dir in the Files page.
func (a *App) goTo(dir string) {
	a.cx.Batch(func() {
		a.query.Set("")
		a.globalText.Set("")
		a.endSelect()
		a.global.Set(false)
		a.path.Set(openlist.Clean(dir))
	})
}

// goUp opens the folder above.
func (a *App) goUp() { a.goTo(openlist.Parent(a.path.GetUntracked())) }

// ---- selection ----

func (a *App) endSelect() {
	a.selecting.Set(false)
	a.selected.Set(map[string]bool{})
}

func (a *App) beginSelect(first string) {
	a.cx.Batch(func() {
		a.fabOpen.Set(false)
		a.selecting.Set(true)
		a.selected.Set(map[string]bool{first: true})
	})
}

func (a *App) toggleSelect(p string) {
	m := maps_clone(a.selected.GetUntracked())
	if m[p] {
		delete(m, p)
	} else {
		m[p] = true
	}
	a.selected.Set(m)
}

func maps_clone(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// selectedItems are the shown items that are selected.
func (a *App) selectedItems() []browse.Item {
	sel := a.selected.Get()
	var out []browse.Item
	for _, it := range a.items.Get() {
		if sel[it.Path] {
			out = append(out, it)
		}
	}
	return out
}

func (a *App) selectAll(all bool) {
	m := map[string]bool{}
	if all {
		for _, it := range a.items.GetUntracked() {
			m[it.Path] = true
		}
	}
	a.selected.Set(m)
}

// ---- panels ----

func (a *App) openSheet(s sheetState) {
	if s.Kind != sheetQuitConfirm {
		a.cancelPendingQuit()
	}
	a.sheet.Set(s)
}

func (a *App) closeSheet() {
	a.cancelPendingQuit()
	a.sheet.Set(sheetState{})
}

// toast shows a short message.
func (a *App) toast(msg string) {
	a.toaster.Show(ui.ToastContent{Title: msg, Duration: 2400 * time.Millisecond})
}

// fail shows why a call failed.
func (a *App) fail(err error) {
	if p := access.Classify(err); p.Kind == access.Network || p.Kind == access.Folder || p.Kind == access.Browser {
		a.raise([]accessIssue{{Kind: p.Kind, Detail: p.Detail, Target: a.issueTarget(p.Kind)}})
		return
	}
	a.toast(locales.ToastError(a.cx, errText(err)))
}

// issueTarget is what a problem of a kind is about, as far as the current
// site tells.
func (a *App) issueTarget(k access.Kind) string {
	s, ok := a.site()
	switch {
	case k == access.Folder && ok:
		return a.downloadDir(s)
	case k == access.Folder:
		return defaultDownloadDir()
	case k == access.Network && ok:
		if u, err := url.Parse(s.URL); err == nil {
			return u.Host
		}
	}
	return ""
}

// errText is an error in the words to show: the server's message when it
// gave one.
func errText(err error) string {
	var api *openlist.APIError
	if errors.As(err, &api) && api.Message != "" {
		return api.Message
	}
	return err.Error()
}

// goPost runs work off the engine's goroutine and hands its result to done
// on it.
func goPost[T any](cx *core.Scope, work func(ctx context.Context) (T, error), done func(T, error)) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		v, err := work(ctx)
		cx.Post(func() { done(v, err) })
	}()
}

// downloadDir is where files for a site are saved on this device: the folder
// the user chose, else the system's Downloads folder. Where the user asked for
// a save dialog for each download it is the folder to try first, as Android
// holds it.
func (a *App) downloadDir(s store.Site) string {
	if d := s.Settings.LocalDir; d != "" && d != localDirAsk {
		return d
	}
	return defaultDownloadDir()
}

// asksEachDownload reports whether a site's downloads go through the system's
// save dialog by the user's choice, which only Android offers.
func asksEachDownload(s store.Site) bool {
	return savesThroughDialog() && s.Settings.LocalDir == localDirAsk
}

// secretsDir is the folder where an earlier version kept the tokens in a file,
// "" where there was none: the app's own folder, found in NSGO_DATA_DIR on
// Android, which gives its process no HOME or XDG_CONFIG_HOME, and in the
// user's config folder elsewhere.
func secretsDir() string {
	return secretsDirIn(os.Getenv, os.UserConfigDir)
}

// secretsDirIn is secretsDir over an environment and a config folder.
func secretsDirIn(env func(string) string, config func() (string, error)) string {
	if dir := env("NSGO_DATA_DIR"); dir != "" && filepath.IsAbs(dir) {
		return filepath.Join(dir, "openlist-client")
	}
	if dir, err := config(); err == nil && dir != "" {
		return filepath.Join(dir, "openlist-client")
	}
	return ""
}

// legacySecretService is the Secrets service name used before the app ID was
// changed to io.github.hw618.openlistclient. Tokens under it are copied into
// the current service on first read (desktop keychains). Android package
// changes are a different app, so the old Keystore is not reachable there.
const legacySecretService = "dev.nsgo.openlistclient"

// newSecrets keeps the tokens in the platform's secure store, and brings in
// those an earlier version kept in a file, and those under the previous app ID.
func newSecrets(cx *core.Scope) store.Secrets {
	if isWeb() {
		// An earlier version kept them in the page's plain storage.
		cx.Storage().RemoveItem("tokens")
	}
	var legacy func() (map[string]string, func())
	if dir := secretsDir(); dir != "" {
		f := &store.FileSecrets{Dir: dir}
		legacy = func() (map[string]string, func()) { return f.All(), f.Wipe }
	}
	cur := cx.Secrets()
	old := cur
	old.Service = legacySecretService
	return newVault(migrateSecrets{cur: cur, old: old}, legacy)
}

// migrateSecrets reads from the current Secrets service, and when an account
// is missing there, from the previous app ID's service, then copies it over.
type migrateSecrets struct {
	cur, old core.Secrets
}

func (m migrateSecrets) Get(account string) (string, bool, error) {
	v, ok, err := m.cur.Get(account)
	if err != nil || ok {
		return v, ok, err
	}
	v, ok, err = m.old.Get(account)
	if err != nil || !ok || v == "" {
		return v, ok, err
	}
	if err := m.cur.Set(account, v); err != nil {
		// Still hand the token to the app; a later Set may succeed.
		return v, true, nil
	}
	_ = m.old.Delete(account)
	return v, true, nil
}

func (m migrateSecrets) Set(account, value string) error {
	return m.cur.Set(account, value)
}

func (m migrateSecrets) Delete(account string) error {
	err := m.cur.Delete(account)
	_ = m.old.Delete(account)
	return err
}
