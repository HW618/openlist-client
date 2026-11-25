package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"github.com/HW618/openlist-client/src/internal/store"
)

func startDesktopApp(t *testing.T, opts ...nsgotest.Option) *nsgotest.App {
	prevGuide := notifyGuide
	notifyGuide = false
	t.Cleanup(func() { notifyGuide = prevGuide })
	return nsgotest.StartApp(t, func(cx *core.Scope) {
		runWith(cx, &store.MemorySecrets{}, nil)
	}, opts...)
}

func waitFileWritten(dir, name string, release func()) {
	release()
	target := filepath.Join(dir, name)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(target); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func heldDownloadServer(t *testing.T) (srv *httptest.Server, half <-chan struct{}, release func()) {
	halfCh := make(chan struct{})
	finish := make(chan struct{})
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, data any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success", "data": data})
	}
	mux.HandleFunc("/api/fs/list", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{
			"content":  []map[string]any{{"name": "movie.bin", "size": 1000, "is_dir": false}},
			"total":    1,
			"provider": "Local",
		})
	})
	mux.HandleFunc("/api/fs/get", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"name": "movie.bin", "size": 1000, "is_dir": false, "raw_url": "http://" + r.Host + "/d/movie.bin"})
	})
	mux.HandleFunc("/d/movie.bin", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write(make([]byte, 500))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-halfCh:
		default:
			close(halfCh)
		}
		<-finish
		_, _ = w.Write(make([]byte, 500))
	})
	srv = httptest.NewServer(cors(mux))
	rel := func() {
		select {
		case <-finish:
		default:
			close(finish)
		}
	}
	t.Cleanup(func() {
		rel()
		srv.Close()
	})
	return srv, halfCh, rel
}

func TestDesktopCloseToTrayHidesWindowAndTrayMenuRestores(t *testing.T) {
	for _, osKind := range []core.OS{core.OSMacOS, core.OSWindows, core.OSLinux} {
		t.Run(string(osKind), func(t *testing.T) {
			srv := newFakeServer(t)
			app := startDesktopApp(t,
				seed(srv.URL),
				nsgotest.Size(960, 720),
				nsgotest.Environment(core.Environment{OS: osKind, Idiom: core.IdiomDesktop}),
			)
			app.WaitFor("id:file-Docs", 2*time.Second)

			app.Press("id:tab-settings")
			app.WaitFor("id:close-to-tray", time.Second)

			if app.CloseWindow() {
				t.Fatal("closing with closeToTray=true should keep the window alive (hidden)")
			}
			if app.Quits != 0 {
				t.Fatalf("Quits = %d, want 0 when minimizing to tray", app.Quits)
			}

			app.ClickTrayIndex(0, core.MouseButtonRight)
			if len(app.PopupMenu) == 0 || !strings.Contains(app.PopupMenu[0].Label, "No active transfers") {
				t.Fatalf("unexpected tray popup menu: %+v", app.PopupMenu)
			}
			app.ChoosePopup("Open transfers")
			app.WaitFor("id:filter-all", time.Second)

			app.ClickTrayIndex(0, core.MouseButtonRight)
			app.ChoosePopup("Quit OpenList")
			if app.Quits != 1 {
				t.Fatalf("Quits = %d, want 1 after choosing tray Quit when idle", app.Quits)
			}
		})
	}
}

func TestDesktopActiveTransferQuitConfirmsBeforeExiting(t *testing.T) {
	srv, half, release := heldDownloadServer(t)
	defer release()
	dir := t.TempDir()
	app := startDesktopApp(t,
		seedIn(srv.URL, dir),
		nsgotest.Size(960, 720),
		nsgotest.Environment(core.Environment{OS: core.OSMacOS, Idiom: core.IdiomDesktop}),
	)
	app.WaitFor("id:file-movie.bin", 2*time.Second)
	app.LongPress("id:file-movie.bin")
	app.Press("id:batch-download")
	<-half

	app.ClickTrayIndex(0, core.MouseButtonRight)
	if len(app.PopupMenu) == 0 || !strings.Contains(app.PopupMenu[0].Label, "1 active transfer") {
		t.Fatalf("tray menu should show 1 active transfer, got %+v", app.PopupMenu)
	}
	app.ChoosePopup("Quit OpenList")
	app.WaitFor("id:quit-confirm", time.Second)
	app.WaitFor("id:quit-cancel", time.Second)
	if app.Quits != 0 {
		t.Fatalf("Quits = %d before confirming, want 0", app.Quits)
	}

	app.Press("id:quit-cancel")
	app.WaitForGone("id:quit-confirm", time.Second)
	if app.Quits != 0 {
		t.Fatalf("Quits = %d after cancel, want 0", app.Quits)
	}

	app.RequestQuit(false)
	app.WaitFor("id:quit-confirm", time.Second)
	app.Press("id:quit-confirm")
	app.WaitUntil(time.Second, func() bool { return app.Quits == 1 })
	waitFileWritten(dir, "movie.bin", release)
}

func TestDesktopForcedQuitSkipsConfirmation(t *testing.T) {
	srv, half, release := heldDownloadServer(t)
	defer release()
	dir := t.TempDir()
	app := startDesktopApp(t,
		seedIn(srv.URL, dir),
		nsgotest.Size(960, 720),
		nsgotest.Environment(core.Environment{OS: core.OSMacOS, Idiom: core.IdiomDesktop}),
	)
	app.WaitFor("id:file-movie.bin", 2*time.Second)
	app.LongPress("id:file-movie.bin")
	app.Press("id:batch-download")
	<-half

	app.RequestQuit(true)
	if app.Quits != 1 {
		t.Fatalf("forced quit Quits = %d, want 1", app.Quits)
	}
	if app.Exists("id:quit-confirm") {
		t.Fatal("forced quit should not open quit confirmation")
	}
	waitFileWritten(dir, "movie.bin", release)
}

func TestDesktopCloseToTrayOffQuitsDirectlyOrConfirms(t *testing.T) {
	for _, osKind := range []core.OS{core.OSMacOS, core.OSWindows, core.OSLinux} {
		t.Run(string(osKind)+"-idle", func(t *testing.T) {
			srv := newFakeServer(t)
			app := startDesktopApp(t,
				seed(srv.URL),
				nsgotest.Size(960, 720),
				nsgotest.Environment(core.Environment{OS: osKind, Idiom: core.IdiomDesktop}),
			)
			app.Press("id:tab-settings")
			app.WaitFor("id:close-to-tray", time.Second)
			app.Press("id:close-to-tray")
			app.WaitFor("text:Closing the window will quit (confirming if transfers are active)", time.Second)
			app.CloseWindow()
			if app.Quits != 1 {
				t.Fatalf("idle close with closeToTray=false: Quits = %d, want 1", app.Quits)
			}
		})

		t.Run(string(osKind)+"-active", func(t *testing.T) {
			srv, half, release := heldDownloadServer(t)
			defer release()
			dir := t.TempDir()
			app := startDesktopApp(t,
				seedIn(srv.URL, dir),
				nsgotest.Size(960, 720),
				nsgotest.Environment(core.Environment{OS: osKind, Idiom: core.IdiomDesktop}),
			)
			app.WaitFor("id:file-movie.bin", 2*time.Second)
			app.LongPress("id:file-movie.bin")
			app.Press("id:batch-download")
			<-half

			app.Press("id:tab-settings")
			app.WaitFor("id:close-to-tray", time.Second)
			app.Press("id:close-to-tray")
			app.CloseWindow()
			app.WaitFor("id:quit-confirm", time.Second)
			if app.Quits != 0 {
				t.Fatalf("active close with closeToTray=false: Quits = %d, want 0", app.Quits)
			}
			app.Press("id:quit-confirm")
			app.WaitUntil(time.Second, func() bool { return app.Quits == 1 })
			waitFileWritten(dir, "movie.bin", release)
		})
	}
}

func TestDesktopSettingsHiddenOnNonDesktop(t *testing.T) {
	cases := []struct {
		name string
		env  core.Environment
	}{
		{"android", core.Environment{OS: core.OSAndroid, Idiom: core.IdiomPhone}},
		{"ios", core.Environment{OS: core.OSIOS, Idiom: core.IdiomPhone}},
		{"web-on-mac", core.Environment{OS: core.OSMacOS, Idiom: core.IdiomDesktop, Simulated: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newFakeServer(t)
			app := startDesktopApp(t, seed(srv.URL), nsgotest.Environment(tc.env))
			app.Press("id:tab-settings")
			app.WaitFor("id:notify-master", time.Second)
			if app.Exists("id:close-to-tray") {
				t.Fatal("desktop close-to-tray setting should be hidden on non-desktop targets")
			}
		})
	}
}
