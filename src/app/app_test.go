package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"github.com/HW618/openlist-client/src/internal/store"
)

func TestMain(m *testing.M) {
	startupChecks = false
	nsgotest.Main(m)
}

// fakeServer is an OpenList server with a tree of folders in memory.
type fakeServer struct {
	*httptest.Server
	mu   sync.Mutex
	tree map[string][]fakeEntry
	logs []string
}

type fakeEntry struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	IsDir bool   `json:"is_dir"`
	Mod   string `json:"modified"`
	Type  int    `json:"type"`
}

func newFakeServer(t *testing.T) *fakeServer {
	f := &fakeServer{tree: map[string][]fakeEntry{
		"/":             {{Name: "Docs", IsDir: true}, {Name: "Media", IsDir: true}},
		"/Docs":         {{Name: "notes.txt", Size: 12}, {Name: "Reports", IsDir: true}},
		"/Docs/Reports": {},
		"/Media":        {{Name: "movie.mp4", Size: 5 << 20, Type: 2}},
	}}
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, data any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success", "data": data})
	}
	mux.HandleFunc("/api/fs/list", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Path string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		defer f.mu.Unlock()
		items := f.tree[req.Path]
		for i := range items {
			items[i].Mod = "2026-01-02T03:04:05Z"
		}
		reply(w, map[string]any{"content": items, "total": len(items), "provider": "Local"})
	})
	mux.HandleFunc("/api/fs/mkdir", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Path string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		defer f.mu.Unlock()
		dir, name := path.Split(req.Path)
		dir = path.Clean(dir)
		f.tree[dir] = append(f.tree[dir], fakeEntry{Name: name, IsDir: true})
		f.tree[req.Path] = nil
		f.logs = append(f.logs, "mkdir "+req.Path)
		reply(w, nil)
	})
	mux.HandleFunc("/api/fs/remove", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Dir   string
			Names []string
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.tree[req.Dir] = slices.DeleteFunc(f.tree[req.Dir], func(e fakeEntry) bool { return slices.Contains(req.Names, e.Name) })
		for _, n := range req.Names {
			f.logs = append(f.logs, "remove "+path.Join(req.Dir, n))
		}
		reply(w, nil)
	})
	mux.HandleFunc("/api/fs/move", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			SrcDir string `json:"src_dir"`
			DstDir string `json:"dst_dir"`
			Names  []string
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, n := range req.Names {
			for _, e := range f.tree[req.SrcDir] {
				if e.Name == n {
					f.tree[req.DstDir] = append(f.tree[req.DstDir], e)
				}
			}
			f.logs = append(f.logs, "move "+path.Join(req.SrcDir, n)+" "+req.DstDir)
		}
		f.tree[req.SrcDir] = slices.DeleteFunc(f.tree[req.SrcDir], func(e fakeEntry) bool { return slices.Contains(req.Names, e.Name) })
		reply(w, nil)
	})
	mux.HandleFunc("/api/public/offline_download_tools", func(w http.ResponseWriter, r *http.Request) {
		reply(w, []string{"aria2", "qBittorrent"})
	})
	mux.HandleFunc("/api/fs/add_offline_download", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Path string
			URLs []string `json:"urls"`
			Tool string
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.logs = append(f.logs, "offline "+req.Tool+" "+req.Path+" "+req.URLs[0])
		reply(w, map[string]any{"tasks": []any{}})
	})
	mux.HandleFunc("/api/admin/task/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var data any = []any{}
		switch r.URL.Path {
		case "/api/admin/task/offline_download/undone":
			data = []map[string]any{{"id": "a", "name": "download a.iso to (/Media)", "state": 1, "status": "[aria2]: downloading", "progress": 42.5}}
		case "/api/admin/task/offline_download/done":
			data = []map[string]any{{"id": "b", "name": "download b.iso to (/Media)", "state": 7, "error": "disk is full", "progress": 10},
				{"id": "c", "name": "download c.iso to (/Media)", "state": 2, "progress": 100}}
		}
		if r.Method == http.MethodPost {
			f.logs = append(f.logs, "task "+path.Base(r.URL.Path)+" "+r.URL.Query().Get("tid"))
		}
		reply(w, data)
	})
	mux.HandleFunc("/api/me", func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "good":
			reply(w, map[string]any{"id": 3, "username": "alice", "role": 0})
		case "admin":
			reply(w, map[string]any{"id": 1, "username": "admin", "role": 2})
		case "off":
			reply(w, map[string]any{"id": 4, "username": "bob", "role": 0, "disabled": true})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 401, "message": "guest user is disabled, login please"})
		}
	})
	mux.HandleFunc("/api/fs/get", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Path string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		reply(w, map[string]any{"name": path.Base(req.Path), "size": 12, "is_dir": false, "raw_url": "http://" + r.Host + "/d" + req.Path, "type": 4})
	})
	mux.HandleFunc("/d/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello, world"))
	})
	f.Server = httptest.NewServer(cors(mux))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeServer) log() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.logs)
}

// seed is saved state with one site on the server.
func seed(url string) nsgotest.Option { return seedIn(url, "") }

// seedIn is seed with downloads saved in localDir.
func seedIn(url, localDir string) nsgotest.Option {
	sites, _ := json.Marshal(map[string]any{"version": 1, "value": []store.Site{{ID: "s1", Name: "Home", URL: url, Settings: store.SiteSettings{LocalDir: localDir}}}})
	return nsgotest.Storage(map[string]string{"sites": string(sites), "currentSite": `{"version":1,"value":"s1"}`})
}

func start(t *testing.T, opts ...nsgotest.Option) *nsgotest.App {
	return startWith(t, &store.MemorySecrets{}, opts...)
}

// startWith is start with the tokens already kept.
func startWith(t *testing.T, secrets store.Secrets, opts ...nsgotest.Option) *nsgotest.App {
	prevGuide := notifyGuide
	notifyGuide = false
	t.Cleanup(func() { notifyGuide = prevGuide })
	return nsgotest.Start(t, func(cx *core.Scope) core.Node { return rootWith(cx, secrets) }, opts...)
}

// readOnlyDir is a folder nothing can be written in.
func readOnlyDir(t *testing.T) string {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	return dir
}

func TestFirstRunAsksForASite(t *testing.T) {
	app := start(t)
	app.WaitFor("text:Connect your first OpenList server", time.Second)
	app.Press("id:add-site")
	app.WaitFor("id:site-name", time.Second)
}

func TestAddSiteThenBrowse(t *testing.T) {
	srv := newFakeServer(t)
	app := start(t)
	app.Press("id:add-site")
	app.Type("id:site-name", "Studio")
	app.Type("id:site-url", srv.URL)
	app.Press("id:site-save")
	app.WaitFor("id:file-Docs", 2*time.Second)
	app.WaitFor("id:site-pill >> text:Studio", time.Second)
}

func TestBrowsesIntoFoldersAndBack(t *testing.T) {
	srv := newFakeServer(t)
	app := start(t, seed(srv.URL))
	app.WaitFor("id:file-Docs", 2*time.Second)
	app.Press("id:file-Docs")
	app.WaitFor("id:file-notes.txt", 2*time.Second)
	if app.Exists("id:file-Media") {
		t.Fatal("Media is not in Docs")
	}
	app.Press("text:Root")
	app.WaitFor("id:file-Media", 2*time.Second)
}

func TestSearchFiltersTheFolder(t *testing.T) {
	srv := newFakeServer(t)
	app := start(t, seed(srv.URL))
	app.WaitFor("id:file-Docs", 2*time.Second)
	app.Type("id:search", "med")
	app.WaitFor("id:file-Media", 3*time.Second)
	if app.Exists("id:file-Docs") {
		t.Fatal("Docs should be filtered out")
	}
	app.Type("id:search", "zzz")
	app.Advance(time.Second) // typing paused: the search below the folder starts
	app.WaitFor("text:No matching files", 5*time.Second)
}

// The folder search reaches the folders below it, though the server has no
// search index (the fake server answers its search with an error).
func TestSearchLooksBelowTheFolder(t *testing.T) {
	srv := newFakeServer(t)
	app := start(t, seed(srv.URL))
	app.WaitFor("id:file-Docs", 2*time.Second)
	app.Type("id:search", "movie")
	app.Advance(time.Second)
	app.WaitFor("id:file-movie.mp4", 5*time.Second)
	if app.Exists("id:file-Docs") {
		t.Fatal("Docs does not match")
	}
}

// A new search never shows what an earlier one found.
func TestNewSearchDropsTheEarlierResults(t *testing.T) {
	srv := newFakeServer(t)
	app := start(t, seed(srv.URL))
	app.WaitFor("id:file-Docs", 2*time.Second)
	app.Press("text:Everywhere")
	app.Type("id:search", "movie")
	app.Advance(time.Second)
	app.WaitFor("id:file-movie.mp4", 5*time.Second)
	app.Type("id:search", "x") // "moviex": the earlier hit is gone at once
	if app.Exists("id:file-movie.mp4") {
		t.Fatal("the earlier results are still shown")
	}
	app.Advance(time.Second)
	app.WaitFor("text:No matching files", 5*time.Second)
}

func TestGlobalSearchLooksThroughTheServer(t *testing.T) {
	srv := newFakeServer(t)
	app := start(t, seed(srv.URL))
	app.WaitFor("id:file-Docs", 2*time.Second)
	app.Press("id:file-Docs")
	app.WaitFor("id:file-notes.txt", 2*time.Second)
	app.Press("text:Everywhere")
	app.Type("id:search", "movie")
	app.Advance(time.Second)
	app.WaitFor("id:file-movie.mp4", 5*time.Second)
}

func TestLongPressSelectsAndDeletes(t *testing.T) {
	srv := newFakeServer(t)
	app := start(t, seed(srv.URL))
	app.WaitFor("id:file-Docs", 2*time.Second)
	app.Press("id:file-Docs")
	app.WaitFor("id:file-notes.txt", 2*time.Second)
	app.LongPress("id:file-notes.txt")
	app.WaitFor("text:1 selected", time.Second)
	app.Press("id:file-Reports")
	app.WaitFor("text:2 selected", time.Second)
	app.Press("text:Delete")
	app.Press("role:button Delete >> nth=1")
	app.WaitUntil(2*time.Second, func() bool { return len(srv.log()) == 2 })
	app.WaitForGone("id:file-notes.txt", 2*time.Second)
}

func TestStorageRootsCannotBeDeleted(t *testing.T) {
	srv := newFakeServer(t)
	app := start(t, seed(srv.URL))
	app.WaitFor("id:file-Docs", 2*time.Second)
	app.LongPress("id:file-Docs")
	app.Press("text:Delete")
	app.Press("role:button Delete >> nth=1")
	app.WaitFor("text:Storage roots can't be deleted", time.Second)
	if got := srv.log(); len(got) != 0 {
		t.Fatalf("server log: %v", got)
	}
}

func TestFloatingMenuCreatesAFolder(t *testing.T) {
	srv := newFakeServer(t)
	app := start(t, seed(srv.URL))
	app.WaitFor("id:file-Docs", 2*time.Second)
	app.Press("id:fab")
	app.WaitFor("id:fab-menu", time.Second)
	app.Press("role:button New folder")
	app.Type("id:mkdir-input", "Archive")
	app.Press("id:mkdir-confirm")
	app.WaitFor("id:file-Archive", 2*time.Second)
	if got := srv.log(); len(got) != 1 || got[0] != "mkdir /Archive" {
		t.Fatalf("server log: %v", got)
	}
}

func TestViewToggleShowsGrid(t *testing.T) {
	srv := newFakeServer(t)
	app := start(t, seed(srv.URL))
	app.WaitFor("id:file-list", 2*time.Second)
	app.Press("id:view-toggle")
	app.WaitFor("id:file-Docs", time.Second)
	if app.Exists("id:file-list") {
		t.Fatal("the list should be gone in grid view")
	}
}

func TestTabsAndSettings(t *testing.T) {
	srv := newFakeServer(t)
	app := start(t, seed(srv.URL))
	app.Press("id:tab-transfers")
	app.WaitFor("id:filter-all", time.Second)
	app.Press("id:tab-settings")
	app.WaitFor("text:Saved OpenList sites", time.Second)
	app.WaitFor("text:Home", time.Second)
	app.Press("id:dark-switch")
	app.WaitFor("text:Dark mode", time.Second)
}

func TestSettingsShowsOnlyTheConnectedSiteUntilExpanded(t *testing.T) {
	srv := newFakeServer(t)
	sites, _ := json.Marshal(map[string]any{"version": 1, "value": []store.Site{
		{ID: "s0", Name: "Office", URL: srv.URL},
		{ID: "s1", Name: "Home", URL: srv.URL},
		{ID: "s2", Name: "Lab", URL: srv.URL}}})
	app := start(t, nsgotest.Storage(map[string]string{"sites": string(sites), "currentSite": `{"version":1,"value":"s1"}`}))
	app.Press("id:tab-settings")
	app.WaitFor("id:sites-toggle", time.Second)
	if app.Exists("text:Office") || app.Exists("text:Lab") {
		t.Fatal("only the connected site should show before expanding")
	}
	app.Press("id:sites-toggle")
	app.WaitFor("text:Office", time.Second)
	app.WaitFor("text:Lab", time.Second)
	app.Press("id:sites-toggle")
	app.WaitFor("text:Home", time.Second)
	if app.Exists("text:Office") {
		t.Fatal("collapsing should hide the other sites")
	}
}

func TestSettingsListsDefaultFolders(t *testing.T) {
	srv := newFakeServer(t)
	app := start(t, seed(srv.URL))
	app.Press("id:tab-settings")
	app.WaitFor("text:Offline download folder", time.Second)
	app.WaitFor("text:Files page folder", time.Second)
	app.WaitFor("text:Download save location", time.Second)
	app.WaitFor("id:download-save-location-row", time.Second)
}

func TestPreviewOfUnsupportedFile(t *testing.T) {
	srv := newFakeServer(t)
	srv.tree["/"] = append(srv.tree["/"], fakeEntry{Name: "data.bin", Size: 5})
	app := start(t, seed(srv.URL))
	app.WaitFor("id:file-data.bin", 2*time.Second)
	app.Press("id:file-data.bin")
	app.WaitFor("id:preview", time.Second)
}

// TestScreenshots saves the main screens in both appearances when the tests
// run in a real window (go tool nsgo test -target macos); headless it does
// nothing.
func TestScreenshots(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		srv := newFakeServer(t)
		srv.tree["/Docs"] = append(srv.tree["/Docs"], fakeEntry{Name: "photo.jpg", Size: 3 << 20, Type: 5}, fakeEntry{Name: "song.mp3", Size: 4 << 20, Type: 3})
		for i := 1; i <= 40; i++ {
			srv.tree["/Docs"] = append(srv.tree["/Docs"], fakeEntry{Name: "archive-" + itoa(i), IsDir: true})
		}
		app := start(t, seed(srv.URL), nsgotest.Environment(core.Environment{Dark: dark}))
		app.WaitFor("id:file-Docs", 2*time.Second)
		app.Press("id:file-Docs")
		app.WaitFor("id:file-archive-1", 2*time.Second)
		app.Screenshot(name + "/files-list")
		app.Press("id:fab")
		app.WaitFor("id:fab-menu", time.Second)
		app.Screenshot(name + "/fab-menu")
		app.Press("id:fab")
		app.LongPress("id:file-archive-1")
		app.WaitFor("id:select-done", time.Second)
		app.Screenshot(name + "/selection")
		app.Press("id:batch-move")
		app.WaitFor("id:picker-confirm", time.Second)
		app.Screenshot(name + "/picker")
		app.Press("id:picker-back")
		app.Press("id:select-done")
		app.Press("id:view-toggle")
		app.Screenshot(name + "/files-grid")
		app.Press("id:tab-settings")
		app.WaitFor("id:dark-switch", time.Second)
		app.Screenshot(name + "/settings")
		app.Press("id:tab-transfers")
		app.Screenshot(name + "/transfers")
	}
}

func TestMoveWithTheFolderPicker(t *testing.T) {
	srv := newFakeServer(t)
	app := start(t, seed(srv.URL))
	app.WaitFor("id:file-Docs", 2*time.Second)
	app.Press("id:file-Docs")
	app.WaitFor("id:file-notes.txt", 2*time.Second)
	app.LongPress("id:file-notes.txt")
	app.Press("id:batch-move")
	app.WaitFor("id:picker-confirm", time.Second)
	app.WaitFor("id:picker-list >> text:Reports", 2*time.Second)
	app.Press("id:picker-list >> text:Reports")
	app.Press("id:picker-confirm")
	app.WaitUntil(2*time.Second, func() bool { return slices.Contains(srv.log(), "move /Docs/notes.txt /Docs/Reports") })
	app.WaitForGone("id:file-notes.txt", 2*time.Second)
}

func TestOfflineDownloadRemembersTheFolder(t *testing.T) {
	srv := newFakeServer(t)
	app := start(t, seed(srv.URL))
	app.WaitFor("id:file-Media", 2*time.Second)
	app.Press("id:file-Media")
	app.WaitFor("id:file-movie.mp4", 2*time.Second)
	app.Press("id:fab")
	app.Press("text:Offline download")
	app.WaitFor("id:offline-urls", time.Second)
	app.Type("id:offline-urls", "https://example.com/a.iso")
	app.WaitFor("text:aria2", 2*time.Second)
	app.Press("id:offline-add")
	app.WaitUntil(2*time.Second, func() bool { return slices.Contains(srv.log(), "offline aria2 /Media https://example.com/a.iso") })
	app.Press("id:fab")
	app.Press("text:Offline download")
	app.WaitFor("id:offline-urls", time.Second)
	app.WaitFor("text:Recently used folders", time.Second)
}

func TestDownloadSavesTheFileAndShowsItDone(t *testing.T) {
	srv := newFakeServer(t)
	dir := t.TempDir()
	app := start(t, seedIn(srv.URL, dir))
	app.WaitFor("id:file-Docs", 2*time.Second)
	app.Press("id:file-Docs")
	app.WaitFor("id:file-notes.txt", 2*time.Second)
	app.LongPress("id:file-notes.txt")
	app.Press("id:batch-download")
	app.WaitUntil(5*time.Second, func() bool {
		b, err := os.ReadFile(filepath.Join(dir, "notes.txt"))
		return err == nil && string(b) == "hello, world"
	})
	app.Press("id:tab-transfers")
	app.WaitFor("text:notes.txt", 2*time.Second)
}

func TestParseLinks(t *testing.T) {
	for in, want := range map[string]bool{
		"https://a/b":                       true,
		"magnet:?xt=urn:btih:abc\nhttp://x": true,
		"ftp://x\n\n  ed2k://y ":            true,
		"not a link":                        false,
		"https://ok\nnope":                  false,
		"":                                  false,
	} {
		if _, ok := parseLinks(in); ok != want {
			t.Errorf("parseLinks(%q) ok = %v", in, ok)
		}
	}
}

func TestTransfersShowOfflineDownloadStatus(t *testing.T) {
	srv := newFakeServer(t)
	app := start(t, seed(srv.URL))
	app.WaitFor("id:file-Docs", 2*time.Second)
	app.Press("id:tab-transfers")
	app.WaitFor("text:Offline downloads", 2*time.Second)
	app.WaitFor("text:Downloading · 42% · [aria2]: downloading", time.Second)
	app.WaitFor("text:Failed: disk is full", time.Second)
	if app.Exists("text:No transfers yet") {
		t.Fatal("offline tasks are transfers too")
	}
	app.Press("id:filter-failed")
	app.WaitForGone("text:Downloading · 42% · [aria2]: downloading", time.Second)
	app.Press("role:button Retry")
	app.WaitUntil(2*time.Second, func() bool { return slices.Contains(srv.log(), "task retry b") })
}

func TestClearFinishedOfflineTasks(t *testing.T) {
	srv := newFakeServer(t)
	app := start(t, seed(srv.URL))
	app.WaitFor("id:file-Docs", 2*time.Second)
	app.Press("id:tab-transfers")
	app.WaitFor("id:offline-clear-done", 2*time.Second)
	app.Press("id:offline-clear-done")
	app.WaitUntil(2*time.Second, func() bool { return slices.Contains(srv.log(), "task clear_done ") })
	app.WaitFor("text:Finished offline tasks cleared", 2*time.Second)
}

// cors lets a browser app on another origin call the server.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.Header().Set("Access-Control-Allow-Methods", "*")
		if r.Method == http.MethodOptions {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// TestServeFake keeps a fake server with many files running for trying the
// app by hand: OPENLIST_FAKE=1 go test -run TestServeFake -timeout 1h
func TestServeFake(t *testing.T) {
	if os.Getenv("OPENLIST_FAKE") == "" {
		t.Skip("set OPENLIST_FAKE to serve")
	}
	srv := newFakeServer(t)
	for i := 1; i <= 120; i++ {
		srv.tree["/Docs"] = append(srv.tree["/Docs"], fakeEntry{Name: "report-" + itoa(i) + ".pdf", Size: int64(i) << 14, Type: 4})
		srv.tree["/Docs"] = append(srv.tree["/Docs"], fakeEntry{Name: "folder-" + itoa(i), IsDir: true})
	}
	_ = os.WriteFile(os.Getenv("OPENLIST_FAKE"), []byte(srv.URL), 0o644)
	time.Sleep(time.Hour)
}

func TestSiteTestSignsInAsTheApp(t *testing.T) {
	for token, want := range map[string]string{
		"good":  "Signed in as alice · User",
		"admin": "Signed in as admin · Admin",
		"off":   "Account bob is disabled",
		"":      "Not signed in",
		"stale": "Not signed in",
	} {
		srv := newFakeServer(t)
		secrets := &store.MemorySecrets{}
		if token != "" {
			_ = secrets.Set("s1", token)
		}
		app := startWith(t, secrets, seed(srv.URL))
		app.WaitFor("id:file-Docs", 2*time.Second)
		app.Press("id:tab-settings")
		app.Press("text:Test")
		app.WaitFor("text:"+srv.URL+" · "+want, 2*time.Second)
		if token == "" || token == "stale" {
			app.WaitFor("role:button Sign in", time.Second)
		}
	}
}

func TestSiteTestSaysWhenTheServerIsDown(t *testing.T) {
	srv := newFakeServer(t)
	url := srv.URL
	app := start(t, seed(url))
	app.WaitFor("id:file-Docs", 2*time.Second)
	srv.Close()
	app.Press("id:tab-settings")
	app.Press("text:Test")
	app.WaitUntil(2*time.Second, func() bool {
		for _, s := range app.Texts() {
			if strings.HasPrefix(s, url+" · Can't connect") {
				return true
			}
		}
		return false
	})
}

func TestStartupAsksForAnUnwritableDownloadFolder(t *testing.T) {
	startupChecks = true
	t.Cleanup(func() { startupChecks = false })
	srv := newFakeServer(t)
	dir := readOnlyDir(t)
	app := start(t, seedIn(srv.URL, dir))
	app.WaitFor("id:access-issue", 2*time.Second)
	app.WaitFor("text:Can't save to the download folder", time.Second)
	app.WaitFor("id:access-choose", time.Second)
	app.WaitFor("id:access-settings", time.Second)
	app.Press("id:access-later")
	app.WaitForGone("id:access-issue", time.Second)
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	app.Press("id:fab")
	app.Press("text:Refresh folder")
	app.Press("id:tab-settings")
}

func TestRecheckTellsWhenAllIsInPlace(t *testing.T) {
	startupChecks = true
	t.Cleanup(func() { startupChecks = false })
	srv := newFakeServer(t)
	dir := readOnlyDir(t)
	app := start(t, seedIn(srv.URL, dir))
	app.WaitFor("id:access-recheck", 2*time.Second)
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	app.Press("id:access-recheck")
	app.WaitFor("text:All permissions are in place", 2*time.Second)
	app.WaitForGone("id:access-issue", time.Second)
}

func TestDownloadRefusedByTheSystemAsksForPermission(t *testing.T) {
	srv := newFakeServer(t)
	dir := readOnlyDir(t)
	app := start(t, seedIn(srv.URL, dir))
	app.WaitFor("id:file-Docs", 2*time.Second)
	app.Press("id:file-Docs")
	app.WaitFor("id:file-notes.txt", 2*time.Second)
	app.LongPress("id:file-notes.txt")
	app.Press("id:batch-download")
	app.WaitFor("id:access-issue", 5*time.Second)
	app.WaitFor("text:Can't save to the download folder", time.Second)
	app.Press("id:access-later")
	app.Press("id:tab-transfers")
	app.WaitFor("text:Download failed", 2*time.Second)
}

// TestPermissionScreenshot saves the permission panel when the tests run in a
// real window.
func TestPermissionScreenshot(t *testing.T) {
	startupChecks = true
	t.Cleanup(func() { startupChecks = false })
	srv := newFakeServer(t)
	app := start(t, seedIn(srv.URL, readOnlyDir(t)))
	app.WaitFor("id:access-issue", 2*time.Second)
	app.Screenshot("access/panel")
}

// asAndroid makes the app behave as on Android for the test.
func asAndroid(t *testing.T) {
	old := savesThroughDialog
	savesThroughDialog = func() bool { return true }
	t.Cleanup(func() { savesThroughDialog = old })
}

func TestAndroidChoosesAmongTheSharedFolders(t *testing.T) {
	asAndroid(t)
	srv := newFakeServer(t)
	app := start(t, seed(srv.URL))
	app.Press("id:tab-settings")
	app.WaitFor("id:download-save-location-row", time.Second)
	app.Press("id:local-dir-change")
	app.WaitFor("text:Documents/OpenList", time.Second)
	app.WaitFor("text:Ask for each download", time.Second)
	app.Press("text:Documents/OpenList")
	want := androidFolderDir("Documents")
	app.WaitUntil(time.Second, func() bool { return slices.Contains(app.Texts(), want) })
	app.Press("id:local-dir-change")
	app.Press("text:Ask for each download")
	app.WaitUntil(time.Second, func() bool {
		return slices.Contains(app.Texts(), "Ask for each download") && !slices.Contains(app.Texts(), "Documents/OpenList")
	})
}

// TestAndroidAuthorizesBeforePickingDownloadFolder: when the download folder
// is not writable, Settings「下载保存位置」shows the authorize sheet first;
// 「去授权」then continues into the shared-folder picker.
func TestAndroidAuthorizesBeforePickingDownloadFolder(t *testing.T) {
	asAndroid(t)
	startupChecks = true
	t.Cleanup(func() { startupChecks = false })
	srv := newFakeServer(t)
	dir := readOnlyDir(t)
	app := start(t, seedIn(srv.URL, dir))
	app.Press("id:tab-settings")
	app.WaitUntil(3*time.Second, func() bool {
		return slices.Contains(app.Texts(), "Authorize file access first, then choose a save folder")
	})
	app.Press("id:local-dir-change")
	app.WaitFor("id:folder-auth-prompt", time.Second)
	app.WaitFor("id:folder-auth-grant", time.Second)
	app.Press("id:folder-auth-grant")
	app.WaitFor("text:Documents/OpenList", 2*time.Second)
	app.WaitFor("text:Ask for each download", time.Second)
	if !slices.Contains(app.PermissionSettings, core.PermissionFullDiskAccess) {
		t.Fatalf("expected OpenPermissionSettings(FullDiskAccess), got %v", app.PermissionSettings)
	}
}

func TestAndroidSavesStraightIntoAFolderItCanWrite(t *testing.T) {
	asAndroid(t)
	srv := newFakeServer(t)
	dir := t.TempDir()
	app := start(t, seedIn(srv.URL, dir))
	app.WaitFor("id:file-Docs", 2*time.Second)
	app.Press("id:file-Docs")
	app.WaitFor("id:file-notes.txt", 2*time.Second)
	app.LongPress("id:file-notes.txt")
	app.Press("id:batch-download")
	app.WaitUntil(5*time.Second, func() bool {
		b, err := os.ReadFile(filepath.Join(dir, "notes.txt"))
		return err == nil && string(b) == "hello, world"
	})
}

func TestSecretsFolderOfEachSystem(t *testing.T) {
	cfg := func() (string, error) { return "/home/u/.config", nil }
	noCfg := func() (string, error) { return "", errors.New("neither $XDG_CONFIG_HOME nor $HOME are defined") }
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	// Android has no config folder to find, and its files folder is passed in.
	if got := secretsDirIn(env(map[string]string{"NSGO_DATA_DIR": "/data/user/0/app/files"}), noCfg); got != filepath.FromSlash("/data/user/0/app/files/openlist-client") {
		t.Errorf("android: %q", got)
	}
	if got := secretsDirIn(env(nil), cfg); got != filepath.FromSlash("/home/u/.config/openlist-client") {
		t.Errorf("desktop: %q", got)
	}
	if got := secretsDirIn(env(nil), noCfg); got != "" {
		t.Errorf("browser: %q", got)
	}
}

func TestSignOutDeletesTheSavedLoginAndNothingElseDoes(t *testing.T) {
	srv := newFakeServer(t)
	secrets := &store.MemorySecrets{}
	_ = secrets.Set("s1", "tok-1")
	app := startWith(t, secrets, seed(srv.URL))
	app.Press("id:tab-settings")
	// Testing the site and switching tabs keep the login.
	app.Press("text:Test")
	app.Press("id:tab-files")
	app.Press("id:tab-settings")
	if _, ok := secrets.Get("s1"); !ok {
		t.Fatal("the login went without the user signing out")
	}
	app.Press("text:Sign out")
	app.WaitFor("text:Signed out of Home; the login saved on this device is deleted", time.Second)
	if _, ok := secrets.Get("s1"); ok {
		t.Fatal("the login is still saved after signing out")
	}
	app.WaitForGone("text:Sign out", time.Second)
}
