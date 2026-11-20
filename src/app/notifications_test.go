package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
		Notifications: store.NotifyPrefs{Set: true, Master: true, Success: true, Failure: true, Asked: true},
	}})
	sites, _ := json.Marshal(map[string]any{"version": 1, "value": []store.Site{{ID: "s1", Name: "Home", URL: srv.URL}}})
	app := start(t, nsgotest.Storage(map[string]string{
		"sites": string(sites), "currentSite": `{"version":1,"value":"s1"}`, "prefs": string(prefs),
	}))
	app.Press("id:tab-settings")
	app.WaitFor("text:Notifications", time.Second)
	app.WaitFor("id:notify-master", time.Second)
	app.WaitFor("id:notify-success", time.Second)
	app.WaitFor("id:notify-failure", time.Second)
	app.WaitFor("text:Platform limits", time.Second)
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
	app.WaitUntil(3*time.Second, func() bool {
		for _, n := range app.Notifications {
			if n.ID == notifyTransferDone {
				return true
			}
		}
		return false
	})
	app.PressNotification(notifyTransferDone)
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
			if n.ID == notifyTransferDone || n.ID == notifyTransferFailed {
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
