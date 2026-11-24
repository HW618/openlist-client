package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"github.com/HW618/openlist-client/src/internal/store"
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
