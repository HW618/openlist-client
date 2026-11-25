package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"
	"github.com/nsgo-dev/nsgo/widget"

	"github.com/HW618/openlist-client/src/internal/store"
	"github.com/HW618/openlist-client/src/internal/transfer"
)

func TestSettingsShowsNotificationToggles(t *testing.T) {
	srv := newFakeServer(t)
	prefs, _ := json.Marshal(map[string]any{"version": 1, "value": store.Prefs{
		View: store.ViewList, Sort: store.SortName,
		Notifications: store.NotifyPrefs{Set: true, Master: true, Asked: true},
	}})
	sites, _ := json.Marshal(map[string]any{"version": 1, "value": []store.Site{{ID: "s1", Name: "Home", URL: srv.URL}}})
	app := start(t, nsgotest.Storage(map[string]string{
		"sites": string(sites), "currentSite": `{"version":1,"value":"s1"}`, "prefs": string(prefs),
	}))
	app.Press("id:tab-settings")
	app.WaitFor("text:Notifications", time.Second)
	app.WaitFor("id:notify-master", time.Second)
	if app.Exists("id:notify-success") || app.Exists("id:notify-failure") || app.Exists("text:Platform limits") {
		t.Fatal("settings should show only the single notification toggle")
	}
}

func TestFirstLaunchGuidesNotificationPermission(t *testing.T) {
	startupChecks = true
	notifyGuide = true
	t.Cleanup(func() {
		startupChecks = false
		notifyGuide = false
	})
	srv := newFakeServer(t)
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		return rootWith(cx, &store.MemorySecrets{})
	}, seed(srv.URL))
	app.WaitFor("id:access-issue", 2*time.Second)
	app.WaitFor("id:access-notify-allow", time.Second)
	app.Press("id:access-notify-allow")
	app.WaitFor("text:Notifications are ready", 2*time.Second)
	if app.NotificationPermission != core.NotificationsGranted {
		t.Fatalf("permission = %v", app.NotificationPermission)
	}
}

func TestTransferDoneShowsMergedNotification(t *testing.T) {
	srv := newFakeServer(t)
	dir := t.TempDir()
	prefs, _ := json.Marshal(map[string]any{"version": 1, "value": store.Prefs{
		View: store.ViewList, Sort: store.SortName,
		Notifications: store.NotifyPrefs{Set: true, Master: true, Success: true, Failure: true, Asked: true},
	}})
	sites, _ := json.Marshal(map[string]any{"version": 1, "value": []store.Site{
		{ID: "s1", Name: "Home", URL: srv.URL, Settings: store.SiteSettings{LocalDir: dir}},
	}})
	app := start(t, nsgotest.Storage(map[string]string{
		"sites": string(sites), "currentSite": `{"version":1,"value":"s1"}`, "prefs": string(prefs),
	}))
	app.NotificationPermission = core.NotificationsGranted
	app.WaitFor("id:file-Docs", 2*time.Second)
	app.Press("id:file-Docs")
	app.WaitFor("id:file-notes.txt", 2*time.Second)
	app.LongPress("id:file-notes.txt")
	app.Press("id:batch-download")
	app.WaitUntil(5*time.Second, func() bool {
		b, err := os.ReadFile(filepath.Join(dir, "notes.txt"))
		return err == nil && string(b) == "hello, world"
	})
	var gotID string
	app.WaitUntil(3*time.Second, func() bool {
		for _, n := range app.Notifications {
			if strings.HasPrefix(n.ID, notifyItemPrefix) && strings.Contains(n.Title, "notes.txt") {
				gotID = n.ID
				return true
			}
		}
		return false
	})
	app.PressNotification(gotID)
	app.WaitFor("id:filter-all", time.Second)
}

func TestMigrateSecretsCopiesFromLegacyService(t *testing.T) {
	cur := newFakeSecrets(nil)
	old := newFakeSecrets(map[string]string{"s1": "tok-legacy"})
	v := newVault(&dualFake{cur: cur, old: old}, nil)
	preload(t, v, "s1")
	if tok, ok := v.Get("s1"); !ok || tok != "tok-legacy" {
		t.Fatalf("Get = %q, %v", tok, ok)
	}
	if _, ok := old.item("s1"); ok {
		t.Fatal("legacy token should be deleted after copy")
	}
	if got, ok := cur.item("s1"); !ok || got != "tok-legacy" {
		t.Fatalf("current = %q, %v", got, ok)
	}
}

func TestMigrateSecretsKeepsCurrentOverLegacy(t *testing.T) {
	cur := newFakeSecrets(map[string]string{"s1": "tok-new"})
	old := newFakeSecrets(map[string]string{"s1": "tok-legacy"})
	v := newVault(&dualFake{cur: cur, old: old}, nil)
	preload(t, v, "s1")
	if tok, ok := v.Get("s1"); !ok || tok != "tok-new" {
		t.Fatalf("Get = %q, %v; current must win", tok, ok)
	}
	if got, ok := old.item("s1"); !ok || got != "tok-legacy" {
		t.Fatal("legacy should be left alone when current already has the account")
	}
}

func TestLegacySecretServiceAndAppID(t *testing.T) {
	if legacySecretService != "dev.nsgo.openlistclient" {
		t.Fatalf("legacySecretService = %q", legacySecretService)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "nsgo.yaml"))
	if err != nil {
		// nsgo test may run with module root as cwd
		data, err = os.ReadFile("nsgo.yaml")
	}
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "id: io.github.hw618.openlistclient") {
		t.Fatal("nsgo.yaml must use the confirmed app id")
	}
	if !strings.Contains(text, "POST_NOTIFICATIONS") {
		t.Fatal("nsgo.yaml must declare POST_NOTIFICATIONS")
	}
	if strings.Contains(text, "background_downloads: true") {
		t.Fatal("nsgo.yaml must not enable android.background_downloads")
	}
	if !strings.Contains(text, "live_activities:") || !strings.Contains(text, "enabled: true") {
		t.Fatal("nsgo.yaml must enable live_activities")
	}
	if !strings.Contains(text, "url_schemes: [openlist]") {
		t.Fatal("nsgo.yaml must declare openlist url_scheme")
	}
}

func TestNotifyMasterOffSkipsNotification(t *testing.T) {
	srv := newFakeServer(t)
	dir := t.TempDir()
	prefs, _ := json.Marshal(map[string]any{"version": 1, "value": store.Prefs{
		View: store.ViewList, Sort: store.SortName,
		Notifications: store.NotifyPrefs{Set: true, Master: false, Success: true, Failure: true, Asked: true},
	}})
	sites, _ := json.Marshal(map[string]any{"version": 1, "value": []store.Site{
		{ID: "s1", Name: "Home", URL: srv.URL, Settings: store.SiteSettings{LocalDir: dir}},
	}})
	app := start(t, nsgotest.Storage(map[string]string{
		"sites": string(sites), "currentSite": `{"version":1,"value":"s1"}`, "prefs": string(prefs),
	}))
	app.NotificationPermission = core.NotificationsGranted
	app.WaitFor("id:file-Docs", 2*time.Second)
	app.Press("id:file-Docs")
	app.WaitFor("id:file-notes.txt", 2*time.Second)
	app.LongPress("id:file-notes.txt")
	app.Press("id:batch-download")
	app.WaitUntil(5*time.Second, func() bool {
		b, err := os.ReadFile(filepath.Join(dir, "notes.txt"))
		return err == nil && string(b) == "hello, world"
	})
	// Give syncTasks a moment; master off must not emit a completion notification.
	deadline := time.Now().Add(800 * time.Millisecond)
	for time.Now().Before(deadline) {
		for _, n := range app.Notifications {
			if strings.HasPrefix(n.ID, notifyItemPrefix) || n.ID == notifySummaryID {
				t.Fatalf("master off should not notify, got %+v", n)
			}
		}
		time.Sleep(40 * time.Millisecond)
	}
}

func TestNotifyPermissionDeniedShowsSettingsGuide(t *testing.T) {
	startupChecks = true
	notifyGuide = true
	t.Cleanup(func() {
		startupChecks = false
		notifyGuide = false
	})
	srv := newFakeServer(t)
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		return rootWith(cx, &store.MemorySecrets{})
	}, seed(srv.URL))
	app.NotificationAnswer = core.NotificationsDenied
	app.WaitFor("id:access-notify-allow", 2*time.Second)
	app.Press("id:access-notify-allow")
	app.WaitFor("id:access-settings", 2*time.Second)
	if app.NotificationPermission != core.NotificationsDenied {
		t.Fatalf("permission = %v", app.NotificationPermission)
	}
}

// dualFake migrates like migrateSecrets for unit tests without core.Secrets.
type dualFake struct {
	cur, old *fakeSecrets
}

func (d *dualFake) Get(account string) (string, bool, error) {
	v, ok, err := d.cur.Get(account)
	if err != nil || ok {
		return v, ok, err
	}
	v, ok, err = d.old.Get(account)
	if err != nil || !ok || v == "" {
		return v, ok, err
	}
	if err := d.cur.Set(account, v); err != nil {
		return v, true, nil
	}
	_ = d.old.Delete(account)
	return v, true, nil
}

func (d *dualFake) Set(account, value string) error { return d.cur.Set(account, value) }
func (d *dualFake) Delete(account string) error {
	err := d.cur.Delete(account)
	_ = d.old.Delete(account)
	return err
}

func TestDismissingNotifyGuideLaterStillPromptsFromSettings(t *testing.T) {
	startupChecks = true
	notifyGuide = true
	t.Cleanup(func() {
		startupChecks = false
		notifyGuide = false
	})
	srv := newFakeServer(t)
	app := nsgotest.Start(t, func(cx *core.Scope) core.Node {
		return rootWith(cx, &store.MemorySecrets{})
	}, seed(srv.URL))
	app.WaitFor("id:access-later", 2*time.Second)
	app.Press("id:access-later")
	app.WaitForGone("id:access-issue", time.Second)

	app.Press("id:tab-settings")
	app.WaitFor("id:notify-master", time.Second)
	app.Press("id:notify-master")
	app.Press("id:notify-master")
	app.WaitUntil(2*time.Second, func() bool {
		return app.NotificationPermission == core.NotificationsGranted
	})
}

func TestNotifyOfflineOutcomesNotifiesFastFinishAfterBaseline(t *testing.T) {
	var phase atomic.Int32
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, data any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success", "data": data})
	}
	mux.HandleFunc("/api/fs/list", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"content": []any{}, "total": 0, "provider": "Local"})
	})
	mux.HandleFunc("/api/admin/task/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/admin/task/offline_download/done" {
			if phase.Load() == 0 {
				reply(w, []map[string]any{{"id": "old", "name": "old.iso", "state": 2, "progress": 100}})
				return
			}
			reply(w, []map[string]any{
				{"id": "old", "name": "old.iso", "state": 2, "progress": 100},
				{"id": "fast", "name": "fast.iso", "state": 2, "progress": 100},
			})
			return
		}
		reply(w, []any{})
	})
	srv := httptest.NewServer(cors(mux))
	t.Cleanup(srv.Close)

	prefs, _ := json.Marshal(map[string]any{"version": 1, "value": store.Prefs{
		View: store.ViewList, Sort: store.SortName,
		Notifications: store.NotifyPrefs{Set: true, Master: true, Success: true, Failure: true, Asked: true},
	}})
	sites, _ := json.Marshal(map[string]any{"version": 1, "value": []store.Site{{ID: "s1", Name: "Home", URL: srv.URL}}})
	app := start(t, nsgotest.Storage(map[string]string{
		"sites": string(sites), "currentSite": `{"version":1,"value":"s1"}`, "prefs": string(prefs),
	}))
	app.NotificationPermission = core.NotificationsGranted
	app.Press("id:tab-transfers")
	app.WaitFor("text:old.iso", 2*time.Second)
	for _, n := range app.Notifications {
		if strings.HasPrefix(n.ID, notifyItemPrefix) || n.ID == notifySummaryID {
			t.Fatalf("baseline finished task should not notify, got %+v", n)
		}
	}
	phase.Store(1)
	app.Press("role:button Refresh offline tasks")
	app.WaitFor("text:fast.iso", 2*time.Second)
	app.WaitUntil(2*time.Second, func() bool {
		for _, n := range app.Notifications {
			if strings.HasPrefix(n.ID, notifyItemPrefix) && strings.Contains(n.Title, "fast.iso") {
				return true
			}
		}
		return false
	})
}

func TestNotificationsAlwaysNotifyAndCollapseWhenTooMany(t *testing.T) {
	var phase atomic.Int32
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, data any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success", "data": data})
	}
	mux.HandleFunc("/api/fs/list", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"content": []any{}, "total": 0, "provider": "Local"})
	})
	mux.HandleFunc("/api/admin/task/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/admin/task/offline_download/done" {
			base := map[string]any{"id": "old", "name": "old.iso", "state": 2, "progress": 100}
			switch phase.Load() {
			case 0:
				reply(w, []map[string]any{base})
				return
			case 1:
				reply(w, []map[string]any{
					base,
					{"id": "t1", "name": "a.zip", "state": 2, "progress": 100},
					{"id": "t2", "name": "b.zip", "state": 2, "progress": 100},
					{"id": "t3", "name": "bad.iso", "state": 7, "progress": 50},
				})
				return
			case 2:
				reply(w, []map[string]any{
					base,
					{"id": "t1", "name": "a.zip", "state": 2, "progress": 100},
					{"id": "t2", "name": "b.zip", "state": 2, "progress": 100},
					{"id": "t3", "name": "bad.iso", "state": 7, "progress": 50},
					{"id": "t4", "name": "c.tar", "state": 2, "progress": 100},
				})
				return
			case 3:
				reply(w, []map[string]any{
					base,
					{"id": "t1", "name": "a.zip", "state": 2, "progress": 100},
					{"id": "t2", "name": "b.zip", "state": 2, "progress": 100},
					{"id": "t3", "name": "bad.iso", "state": 7, "progress": 50},
					{"id": "t4", "name": "c.tar", "state": 2, "progress": 100},
					{"id": "t5", "name": "d.txt", "state": 2, "progress": 100},
				})
				return
			}
		}
		reply(w, []any{})
	})
	srv := httptest.NewServer(cors(mux))
	t.Cleanup(srv.Close)

	prefs, _ := json.Marshal(map[string]any{"version": 1, "value": store.Prefs{
		View: store.ViewList, Sort: store.SortName,
		// Legacy Success/Failure false values must not suppress notifications when Master is on.
		Notifications: store.NotifyPrefs{Set: true, Master: true, Success: false, Failure: false, Asked: true},
	}})
	sites, _ := json.Marshal(map[string]any{"version": 1, "value": []store.Site{{ID: "s1", Name: "Home", URL: srv.URL}}})
	app := start(t, nsgotest.Storage(map[string]string{
		"sites": string(sites), "currentSite": `{"version":1,"value":"s1"}`, "prefs": string(prefs),
	}))
	app.NotificationPermission = core.NotificationsGranted
	app.Press("id:tab-transfers")
	app.WaitFor("text:old.iso", 2*time.Second)

	// Phase 1: 3 items (2 done, 1 failed) -> 3 individual notifications, not collapsed.
	phase.Store(1)
	app.Press("role:button Refresh offline tasks")
	app.WaitFor("text:bad.iso", 2*time.Second)
	app.WaitUntil(2*time.Second, func() bool {
		var seenDoneA, seenDoneB, seenFailBad bool
		for _, n := range app.Notifications {
			if !strings.HasPrefix(n.ID, notifyItemPrefix) {
				continue
			}
			if strings.Contains(n.Title, "a.zip") {
				seenDoneA = true
			}
			if strings.Contains(n.Title, "b.zip") {
				seenDoneB = true
			}
			if strings.Contains(n.Title, "bad.iso") {
				seenFailBad = true
			}
		}
		return seenDoneA && seenDoneB && seenFailBad
	})

	// Phase 2: 4th item arrives -> individual notifications close and collapse into summary.
	phase.Store(2)
	app.Press("role:button Refresh offline tasks")
	app.WaitFor("text:c.tar", 2*time.Second)
	app.WaitUntil(2*time.Second, func() bool {
		hasItem := false
		var summary *core.Notification
		for i := range app.Notifications {
			n := app.Notifications[i]
			if strings.HasPrefix(n.ID, notifyItemPrefix) {
				hasItem = true
			}
			if n.ID == notifySummaryID {
				summary = &n
			}
		}
		return !hasItem && summary != nil && strings.Contains(summary.Title, "4 tasks finished (3 done, 1 failed)")
	})

	// Pressing the summary clears the batch so the next item notifies individually again.
	app.PressNotification(notifySummaryID)
	phase.Store(3)
	app.Press("role:button Refresh offline tasks")
	app.WaitFor("text:d.txt", 2*time.Second)
	app.WaitUntil(2*time.Second, func() bool {
		for _, n := range app.Notifications {
			if strings.HasPrefix(n.ID, notifyItemPrefix) && strings.Contains(n.Title, "d.txt") {
				return true
			}
		}
		return false
	})
}

func TestLiveActivityShowsAndUpdatesDownloadProgress(t *testing.T) {
	half := make(chan struct{})
	finish := make(chan struct{})
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, data any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success", "data": data})
	}
	mux.HandleFunc("/api/fs/list", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{
			"content":  []map[string]any{{"name": "movie.bin", "size": 1000, "is_dir": false, "type": 0}},
			"total":    1,
			"provider": "Local",
		})
	})
	mux.HandleFunc("/api/fs/get", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"name": "movie.bin", "size": 1000, "is_dir": false, "raw_url": "http://" + r.Host + "/d/movie.bin"})
	})
	mux.HandleFunc("/d/movie.bin", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "bytes=0-0" {
			w.Header().Set("Content-Range", "bytes 0-0/1000")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte{0})
			return
		}
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write(make([]byte, 500))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-half:
		default:
			close(half)
		}
		<-finish
		_, _ = w.Write(make([]byte, 500))
	})
	srv := httptest.NewServer(cors(mux))
	t.Cleanup(func() {
		select {
		case <-finish:
		default:
			close(finish)
		}
		srv.Close()
	})

	dir := t.TempDir()
	prefs, _ := json.Marshal(map[string]any{"version": 1, "value": store.Prefs{
		View: store.ViewList, Sort: store.SortName,
		Notifications: store.NotifyPrefs{Set: true, Master: true, Asked: true},
	}})
	sites, _ := json.Marshal(map[string]any{"version": 1, "value": []store.Site{
		{ID: "s1", Name: "Home", URL: srv.URL, Settings: store.SiteSettings{LocalDir: dir}},
	}})
	app := start(t,
		nsgotest.LiveActivities(true),
		nsgotest.Storage(map[string]string{
			"sites": string(sites), "currentSite": `{"version":1,"value":"s1"}`, "prefs": string(prefs),
		}),
	)
	app.NotificationPermission = core.NotificationsGranted
	app.WaitFor("id:file-movie.bin", 2*time.Second)
	app.LongPress("id:file-movie.bin")
	app.Press("id:batch-download")

	<-half
	app.WaitUntil(3*time.Second, func() bool {
		acts := app.LiveActivities()
		if len(acts) != 1 || len(acts[0].Changes) == 0 {
			return false
		}
		last := acts[0].Changes[len(acts[0].Changes)-1].Content
		return strings.Contains(last.Title, "movie.bin") && last.ShowProgress && last.Progress >= 0.45 && last.URL == transferDeepLink && len(last.Actions) == 0
	})

	close(finish)
	app.WaitUntil(3*time.Second, func() bool {
		acts := app.LiveActivities()
		return len(acts) == 1 && acts[0].End != nil
	})
}

func TestLiveActivityDeepLinkOpensTransfers(t *testing.T) {
	srv := newFakeServer(t)
	app := start(t, seed(srv.URL))
	app.WaitFor("id:file-Docs", 2*time.Second)
	app.OpenLinks(transferDeepLink)
	app.WaitFor("id:filter-all", 2*time.Second)
}

func TestLiveActivityOffWhenMasterDisabled(t *testing.T) {
	hold := make(chan struct{})
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, data any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success", "data": data})
	}
	mux.HandleFunc("/api/fs/list", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{
			"content":  []map[string]any{{"name": "slow.bin", "size": 100, "is_dir": false}},
			"total":    1,
			"provider": "Local",
		})
	})
	mux.HandleFunc("/api/fs/get", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"name": "slow.bin", "size": 100, "is_dir": false, "raw_url": "http://" + r.Host + "/d/slow.bin"})
	})
	mux.HandleFunc("/d/slow.bin", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write(make([]byte, 50))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-hold
		_, _ = w.Write(make([]byte, 50))
	})
	srv := httptest.NewServer(cors(mux))
	t.Cleanup(func() {
		select {
		case <-hold:
		default:
			close(hold)
		}
		srv.Close()
	})

	dir := t.TempDir()
	prefs, _ := json.Marshal(map[string]any{"version": 1, "value": store.Prefs{
		View: store.ViewList, Sort: store.SortName,
		Notifications: store.NotifyPrefs{Set: true, Master: false, Asked: true},
	}})
	sites, _ := json.Marshal(map[string]any{"version": 1, "value": []store.Site{
		{ID: "s1", Name: "Home", URL: srv.URL, Settings: store.SiteSettings{LocalDir: dir}},
	}})
	app := start(t,
		nsgotest.LiveActivities(true),
		nsgotest.Storage(map[string]string{
			"sites": string(sites), "currentSite": `{"version":1,"value":"s1"}`, "prefs": string(prefs),
		}),
	)
	app.WaitFor("id:file-slow.bin", 2*time.Second)
	app.LongPress("id:file-slow.bin")
	app.Press("id:batch-download")
	time.Sleep(250 * time.Millisecond)
	if acts := app.LiveActivities(); len(acts) != 0 {
		t.Fatalf("master off should not start live activity, got %+v", acts)
	}
	close(hold)
}

func TestTransferPromptsNotificationPermissionWhenAskedButNotRequested(t *testing.T) {
	srv := newFakeServer(t)
	dir := t.TempDir()
	prefs, _ := json.Marshal(map[string]any{"version": 1, "value": store.Prefs{
		View: store.ViewList, Sort: store.SortName,
		Notifications: store.NotifyPrefs{Set: true, Master: true, Asked: true, Requested: false},
	}})
	sites, _ := json.Marshal(map[string]any{"version": 1, "value": []store.Site{
		{ID: "s1", Name: "Home", URL: srv.URL, Settings: store.SiteSettings{LocalDir: dir}},
	}})
	app := start(t, nsgotest.Storage(map[string]string{
		"sites": string(sites), "currentSite": `{"version":1,"value":"s1"}`, "prefs": string(prefs),
	}))
	app.WaitFor("id:file-Docs", 2*time.Second)
	app.Press("id:file-Docs")
	app.WaitFor("id:file-notes.txt", 2*time.Second)
	app.LongPress("id:file-notes.txt")
	app.Press("id:batch-download")
	app.WaitUntil(5*time.Second, func() bool {
		b, err := os.ReadFile(filepath.Join(dir, "notes.txt"))
		return err == nil && string(b) == "hello, world"
	})
	app.WaitUntil(2*time.Second, func() bool {
		return app.NotificationPermission == core.NotificationsGranted
	})
	rawPrefs, ok := app.Storage().GetItem("prefs")
	if !ok || !strings.Contains(rawPrefs, `"requested":true`) {
		t.Fatalf("prefs should record requested=true, got %q", rawPrefs)
	}
	// A subsequent download after granting permission emits its completion alert.
	app.LongPress("id:file-notes.txt")
	app.Press("id:batch-download")
	app.WaitUntil(2*time.Second, func() bool {
		for _, n := range app.Notifications {
			if strings.HasPrefix(n.ID, notifyItemPrefix) && strings.Contains(n.Title, "notes.txt") {
				return true
			}
		}
		return false
	})
}

func TestLiveActivityRecoversAfterDelayedAndroidPermission(t *testing.T) {
	prevMobile := isMobileNotifyRuntime
	prevStart := startProgressActivity
	isMobileNotifyRuntime = func() bool { return true }
	var calls atomic.Int32
	startProgressActivity = func(kind string, c widget.ActivityContent, opts ...widget.ActivityOptions) (*widget.Activity, error) {
		// Simulate Android LiveUpdates.state lagging on the initial grant callback.
		if calls.Add(1) <= 2 {
			return nil, widget.ErrActivitiesDisabled
		}
		return prevStart(kind, c, opts...)
	}
	t.Cleanup(func() {
		isMobileNotifyRuntime = prevMobile
		startProgressActivity = prevStart
	})

	half := make(chan struct{})
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
		case <-half:
		default:
			close(half)
		}
		<-finish
		_, _ = w.Write(make([]byte, 500))
	})
	srv := httptest.NewServer(cors(mux))
	t.Cleanup(func() {
		select {
		case <-finish:
		default:
			close(finish)
		}
		srv.Close()
	})

	dir := t.TempDir()
	prefs, _ := json.Marshal(map[string]any{"version": 1, "value": store.Prefs{
		View: store.ViewList, Sort: store.SortName,
		Notifications: store.NotifyPrefs{Set: true, Master: true, Asked: true, Requested: false},
	}})
	sites, _ := json.Marshal(map[string]any{"version": 1, "value": []store.Site{
		{ID: "s1", Name: "Home", URL: srv.URL, Settings: store.SiteSettings{LocalDir: dir}},
	}})
	app := start(t,
		nsgotest.LiveActivities(true),
		nsgotest.Storage(map[string]string{
			"sites": string(sites), "currentSite": `{"version":1,"value":"s1"}`, "prefs": string(prefs),
		}),
	)
	app.WaitFor("id:file-movie.bin", 2*time.Second)
	app.LongPress("id:file-movie.bin")
	app.Press("id:batch-download")

	<-half
	app.WaitUntil(3*time.Second, func() bool {
		acts := app.LiveActivities()
		return len(acts) == 1 && len(acts[0].Changes) > 0
	})

	close(finish)
	app.WaitUntil(3*time.Second, func() bool {
		acts := app.LiveActivities()
		return len(acts) == 1 && acts[0].End != nil
	})
}

func TestAndroidUsesSingleSilentAppProgressNotification(t *testing.T) {
	prevAndroidNotify := isAndroidProgressNotify
	isAndroidProgressNotify = func() bool { return true }
	t.Cleanup(func() { isAndroidProgressNotify = prevAndroidNotify })

	half := make(chan struct{})
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
		case <-half:
		default:
			close(half)
		}
		<-finish
		_, _ = w.Write(make([]byte, 500))
	})
	srv := httptest.NewServer(cors(mux))
	t.Cleanup(func() {
		select {
		case <-finish:
		default:
			close(finish)
		}
		srv.Close()
	})

	dir := t.TempDir()
	prefs, _ := json.Marshal(map[string]any{"version": 1, "value": store.Prefs{
		View: store.ViewList, Sort: store.SortName,
		Notifications: store.NotifyPrefs{Set: true, Master: true, Asked: true, Requested: true},
	}})
	sites, _ := json.Marshal(map[string]any{"version": 1, "value": []store.Site{
		{ID: "s1", Name: "Home", URL: srv.URL, Settings: store.SiteSettings{LocalDir: dir}},
	}})
	app := start(t,
		nsgotest.LiveActivities(true),
		nsgotest.Storage(map[string]string{
			"sites": string(sites), "currentSite": `{"version":1,"value":"s1"}`, "prefs": string(prefs),
		}),
	)
	app.NotificationPermission = core.NotificationsGranted
	app.WaitFor("id:file-movie.bin", 2*time.Second)
	app.LongPress("id:file-movie.bin")
	app.Press("id:batch-download")

	<-half
	app.WaitUntil(3*time.Second, func() bool {
		count := 0
		var prog *core.Notification
		for i := range app.Notifications {
			if app.Notifications[i].ID == notifyProgressID {
				count++
				prog = &app.Notifications[i]
			}
		}
		return count == 1 && prog.Silent && strings.Contains(prog.Title, "movie.bin")
	})
	if acts := app.LiveActivities(); len(acts) != 0 {
		t.Fatalf("Android should not start LiveActivities, got %+v", acts)
	}

	// Tapping the app progress notification navigates to Transfers.
	app.PressNotification(notifyProgressID)
	app.WaitFor("id:filter-all", time.Second)

	close(finish)
	app.WaitUntil(5*time.Second, func() bool {
		hasProg := false
		hasDone := false
		for _, n := range app.Notifications {
			if n.ID == notifyProgressID {
				hasProg = true
			}
			if strings.HasPrefix(n.ID, notifyItemPrefix) && strings.Contains(n.Title, "movie.bin") {
				hasDone = true
			}
		}
		return !hasProg && hasDone
	})
	if acts := app.LiveActivities(); len(acts) != 0 {
		t.Fatalf("Android should never create LiveActivities, got %+v", acts)
	}
}

func TestRetryDismissesOldFailureNotificationAndDeduplicates(t *testing.T) {
	var okPhase atomic.Bool
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, data any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success", "data": data})
	}
	mux.HandleFunc("/api/fs/list", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{
			"content":  []map[string]any{{"name": "fail.txt", "size": 9, "is_dir": false}},
			"total":    1,
			"provider": "Local",
		})
	})
	mux.HandleFunc("/api/fs/get", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"name": "fail.txt", "size": 9, "is_dir": false, "raw_url": "http://" + r.Host + "/d/fail.txt"})
	})
	mux.HandleFunc("/d/fail.txt", func(w http.ResponseWriter, r *http.Request) {
		if !okPhase.Load() {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", "9")
		_, _ = w.Write([]byte("recovered"))
	})
	srv := httptest.NewServer(cors(mux))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	prefs, _ := json.Marshal(map[string]any{"version": 1, "value": store.Prefs{
		View: store.ViewList, Sort: store.SortName,
		Notifications: store.NotifyPrefs{Set: true, Master: true, Asked: true, Requested: true},
	}})
	sites, _ := json.Marshal(map[string]any{"version": 1, "value": []store.Site{
		{ID: "s1", Name: "Home", URL: srv.URL, Settings: store.SiteSettings{LocalDir: dir}},
	}})
	app := start(t, nsgotest.Storage(map[string]string{
		"sites": string(sites), "currentSite": `{"version":1,"value":"s1"}`, "prefs": string(prefs),
	}))
	app.NotificationPermission = core.NotificationsGranted
	app.WaitFor("id:file-fail.txt", 2*time.Second)
	app.LongPress("id:file-fail.txt")
	app.Press("id:batch-download")

	app.WaitUntil(3*time.Second, func() bool {
		count := 0
		for _, n := range app.Notifications {
			if strings.HasPrefix(n.ID, notifyItemPrefix) && strings.Contains(n.Title, "Transfer failed: fail.txt") {
				count++
			}
		}
		return count == 1
	})

	okPhase.Store(true)
	app.Press("id:tab-transfers")
	app.WaitFor("role:button Retry", 2*time.Second)
	app.Press("role:button Retry")
	app.WaitFor("text:Done · 9 B", 3*time.Second)

	itemCount := 0
	hasDone := false
	for _, n := range app.Notifications {
		if strings.HasPrefix(n.ID, notifyItemPrefix) {
			itemCount++
			if strings.Contains(n.Title, "Downloaded: fail.txt") {
				hasDone = true
			}
			if strings.Contains(n.Title, "Transfer failed") {
				t.Fatalf("stale failure notification lingered after retry: %+v", app.Notifications)
			}
		}
	}
	if itemCount != 1 || !hasDone {
		t.Fatalf("expected 1 deduplicated completion notification, got %+v", app.Notifications)
	}
}

func TestDownloadResumesFromPartAfterFailureAndAppRestart(t *testing.T) {
	prevPolicy := downloadRetryPolicy
	downloadRetryPolicy = transfer.RetryPolicy{
		MaxRetries:   3,
		InitialDelay: 5 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
	}
	t.Cleanup(func() { downloadRetryPolicy = prevPolicy })

	payload := bytes.Repeat([]byte("R"), 1024)
	var dropHits atomic.Int32
	var restartRange atomic.Value
	restartRange.Store("")

	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, data any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success", "data": data})
	}
	mux.HandleFunc("/api/fs/list", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{
			"content":  []map[string]any{{"name": "auto.bin", "size": len(payload), "is_dir": false}},
			"total":    1,
			"provider": "Local",
		})
	})
	mux.HandleFunc("/api/fs/get", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Path string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		reply(w, map[string]any{"name": filepath.Base(req.Path), "size": len(payload), "raw_url": "http://" + r.Host + "/d" + req.Path})
	})
	mux.HandleFunc("/d/auto.bin", func(w http.ResponseWriter, r *http.Request) {
		if dropHits.Add(1) == 1 {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = w.Write(payload[:512])
			return
		}
		if r.Header.Get("Range") != "bytes=512-" {
			http.Error(w, "bad range", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Range", "bytes 512-1023/1024")
		w.Header().Set("Content-Length", "512")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[512:])
	})
	mux.HandleFunc("/d/killed.bin", func(w http.ResponseWriter, r *http.Request) {
		restartRange.Store(r.Header.Get("Range"))
		if r.Header.Get("Range") != "bytes=600-" {
			http.Error(w, "expected resume from 600", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Range", "bytes 600-1023/1024")
		w.Header().Set("Content-Length", "424")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[600:])
	})
	srv := httptest.NewServer(cors(mux))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	app := start(t, seedIn(srv.URL, dir))
	app.WaitFor("id:file-auto.bin", 2*time.Second)
	app.LongPress("id:file-auto.bin")
	app.Press("id:batch-download")
	app.WaitUntil(5*time.Second, func() bool {
		b, err := os.ReadFile(filepath.Join(dir, "auto.bin"))
		return err == nil && bytes.Equal(b, payload)
	})
	if dropHits.Load() != 2 {
		t.Fatalf("auto.bin hits = %d, want 2", dropHits.Load())
	}

	// Simulate app killed mid-download with .part on disk and stale Done=0 in storage.
	dir2 := t.TempDir()
	if err := os.WriteFile(transfer.PartPath(dir2, "killed.bin", "tr-kill-1"), payload[:600], 0o644); err != nil {
		t.Fatal(err)
	}
	sites2, _ := json.Marshal(map[string]any{"version": 1, "value": []store.Site{{ID: "s1", Name: "Home", URL: srv.URL, Settings: store.SiteSettings{LocalDir: dir2}}}})
	tasks2, _ := json.Marshal(map[string]any{"version": 1, "value": []map[string]any{{
		"id": "tr-kill-1", "name": "killed.bin", "direction": "download",
		"siteId": "s1", "siteName": "Home", "remotePath": "/killed.bin", "localPath": dir2,
		"total": len(payload), "done": 0, "status": "active",
	}}})
	app2 := start(t, nsgotest.Storage(map[string]string{
		"sites": string(sites2), "currentSite": `{"version":1,"value":"s1"}`, "transfers": string(tasks2),
	}))
	app2.WaitUntil(5*time.Second, func() bool {
		b, err := os.ReadFile(filepath.Join(dir2, "killed.bin"))
		return err == nil && bytes.Equal(b, payload)
	})
	if got, _ := restartRange.Load().(string); got != "bytes=600-" {
		t.Fatalf("restarted task Range = %q, want bytes=600-", got)
	}
}

func TestDownloadAutoResumesOnReturnToForeground(t *testing.T) {
	prevPolicy := downloadRetryPolicy
	downloadRetryPolicy = transfer.RetryPolicy{MaxRetries: -1}
	t.Cleanup(func() { downloadRetryPolicy = prevPolicy })

	payload := bytes.Repeat([]byte("F"), 600)
	var bgDone atomic.Bool
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, data any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success", "data": data})
	}
	mux.HandleFunc("/api/fs/list", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{
			"content":  []map[string]any{{"name": "fg.bin", "size": len(payload), "is_dir": false}},
			"total":    1,
			"provider": "Local",
		})
	})
	mux.HandleFunc("/api/fs/get", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"name": "fg.bin", "size": len(payload), "raw_url": "http://" + r.Host + "/d/fg.bin"})
	})
	mux.HandleFunc("/d/fg.bin", func(w http.ResponseWriter, r *http.Request) {
		if !bgDone.Swap(true) {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = w.Write(payload[:300])
			return
		}
		if r.Header.Get("Range") != "bytes=300-" {
			http.Error(w, "expected range resume from 300", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Range", "bytes 300-599/600")
		w.Header().Set("Content-Length", "300")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[300:])
	})
	srv := httptest.NewServer(cors(mux))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	app := start(t, seedIn(srv.URL, dir))
	app.SetEnvironment(core.Environment{Lifecycle: core.LifecycleBackground})
	app.WaitFor("id:file-fg.bin", 2*time.Second)
	app.LongPress("id:file-fg.bin")
	app.Press("id:batch-download")

	app.Press("id:tab-transfers")
	app.WaitFor("role:button Retry", 3*time.Second)

	// Returning to foreground automatically retries interrupted downloads from .part.
	app.SetEnvironment(core.Environment{Lifecycle: core.LifecycleActive})
	app.WaitUntil(5*time.Second, func() bool {
		b, err := os.ReadFile(filepath.Join(dir, "fg.bin"))
		return err == nil && bytes.Equal(b, payload)
	})
}

func TestAndroidDialogDownloadPreservesPartAcrossFailureAndResumes(t *testing.T) {
	asAndroid(t)
	prevStage := dialogStagingBase
	prevPolicy := downloadRetryPolicy
	stageBase := t.TempDir()
	dialogStagingBase = func() string { return stageBase }
	downloadRetryPolicy = transfer.RetryPolicy{MaxRetries: -1}
	t.Cleanup(func() {
		dialogStagingBase = prevStage
		downloadRetryPolicy = prevPolicy
	})

	payload := bytes.Repeat([]byte("D"), 1000)
	var phase atomic.Int32
	var sawRange atomic.Value
	sawRange.Store("")
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, data any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success", "data": data})
	}
	mux.HandleFunc("/api/fs/list", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{
			"content":  []map[string]any{{"name": "dialog.bin", "size": len(payload), "is_dir": false}},
			"total":    1,
			"provider": "Local",
		})
	})
	mux.HandleFunc("/api/fs/get", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"name": "dialog.bin", "size": len(payload), "raw_url": "http://" + r.Host + "/d/dialog.bin"})
	})
	mux.HandleFunc("/d/dialog.bin", func(w http.ResponseWriter, r *http.Request) {
		if phase.Load() == 0 {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = w.Write(payload[:400])
			return
		}
		rng := r.Header.Get("Range")
		sawRange.Store(rng)
		if rng != "bytes=400-" {
			http.Error(w, "expected resume from 400", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Range", "bytes 400-999/1000")
		w.Header().Set("Content-Length", "600")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[400:])
	})
	srv := httptest.NewServer(cors(mux))
	t.Cleanup(srv.Close)

	saveOut := filepath.Join(t.TempDir(), "saved-dialog.bin")
	app := start(t, seedIn(srv.URL, localDirAsk))
	app.SaveFile = func(r nsgotest.FileSave) string {
		data, err := os.ReadFile(r.Path)
		if err != nil {
			return ""
		}
		if err := os.WriteFile(saveOut, data, 0o600); err != nil {
			return ""
		}
		return saveOut
	}
	app.WaitFor("id:file-dialog.bin", 2*time.Second)
	app.LongPress("id:file-dialog.bin")
	app.Press("id:batch-download")

	app.Press("id:tab-transfers")
	app.WaitFor("role:button Retry", 3*time.Second)

	// Confirm the 400-byte .part staging file survived the failure.
	var foundPart int64
	_ = filepath.WalkDir(stageBase, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), transfer.PartSuffix) {
			if info, ierr := d.Info(); ierr == nil {
				foundPart = info.Size()
			}
		}
		return nil
	})
	if foundPart != 400 {
		t.Fatalf("dialog staging .part size = %d, want 400", foundPart)
	}

	phase.Store(1)
	app.Press("role:button Retry")
	app.WaitUntil(5*time.Second, func() bool {
		b, err := os.ReadFile(saveOut)
		return err == nil && bytes.Equal(b, payload)
	})
	if got, _ := sawRange.Load().(string); got != "bytes=400-" {
		t.Fatalf("dialog retry Range = %q, want bytes=400-", got)
	}
}

func TestTransferBannerShowsOnlyOnTransfersPageAndDismisses(t *testing.T) {
	srv := newFakeServer(t)
	dir := t.TempDir()
	prefs, _ := json.Marshal(map[string]any{"version": 1, "value": store.Prefs{
		View: store.ViewList, Sort: store.SortName,
		Notifications: store.NotifyPrefs{Set: true, Master: true, Asked: true, Requested: true},
	}})
	sites, _ := json.Marshal(map[string]any{"version": 1, "value": []store.Site{
		{ID: "s1", Name: "Home", URL: srv.URL, Settings: store.SiteSettings{LocalDir: dir}},
	}})
	app := start(t, nsgotest.Storage(map[string]string{
		"sites": string(sites), "currentSite": `{"version":1,"value":"s1"}`, "prefs": string(prefs),
	}))
	app.NotificationPermission = core.NotificationsGranted
	app.WaitFor("id:file-Docs", 2*time.Second)
	app.Press("id:file-Docs")
	app.WaitFor("id:file-notes.txt", 2*time.Second)
	app.LongPress("id:file-notes.txt")
	app.Press("id:batch-download")
	app.WaitUntil(5*time.Second, func() bool {
		b, err := os.ReadFile(filepath.Join(dir, "notes.txt"))
		return err == nil && string(b) == "hello, world"
	})
	app.WaitUntil(2*time.Second, func() bool {
		for _, n := range app.Notifications {
			if strings.HasPrefix(n.ID, notifyItemPrefix) && strings.Contains(n.Title, "notes.txt") {
				return true
			}
		}
		return false
	})

	// Must never appear on Files or Settings pages.
	if app.Exists("id:transfer-banner") {
		t.Fatal("transfer banner must not render on Files page")
	}
	app.Press("id:tab-settings")
	app.WaitFor("id:notify-master", time.Second)
	if app.Exists("id:transfer-banner") {
		t.Fatal("transfer banner must not render on Settings page")
	}

	// Visible on Transfers page, hidden again when switching away, and dismissible on click.
	app.Press("id:tab-transfers")
	app.WaitFor("id:transfer-banner", time.Second)
	app.WaitFor("text:OpenList Client · Transfer summary", time.Second)
	app.WaitFor("text:1 task completed", time.Second)

	app.Press("id:tab-files")
	app.WaitFor("id:file-notes.txt", time.Second)
	if app.Exists("id:transfer-banner") {
		t.Fatal("transfer banner should hide when switching away from Transfers page")
	}

	app.Press("id:tab-transfers")
	app.WaitFor("id:transfer-banner", time.Second)
	app.Press("id:transfer-banner")
	app.WaitForGone("id:transfer-banner", time.Second)
}
