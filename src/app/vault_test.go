package app

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"

	"github.com/HW618/openlist-client/src/internal/store"
)

// fakeSecrets is a secure store in memory that can be made to have none, or to
// make the first read wait.
type fakeSecrets struct {
	mu          sync.Mutex
	items       map[string]string
	unavailable bool
	gate        chan struct{} // reads wait for it to close, when set
	log         []string
}

func newFakeSecrets(items map[string]string) *fakeSecrets {
	if items == nil {
		items = map[string]string{}
	}
	return &fakeSecrets{items: items}
}

func (f *fakeSecrets) Get(account string) (string, bool, error) {
	if f.gate != nil {
		<-f.gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unavailable {
		return "", false, core.ErrSecretsUnavailable
	}
	v, ok := f.items[account]
	return v, ok, nil
}

func (f *fakeSecrets) Set(account, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unavailable {
		return core.ErrSecretsUnavailable
	}
	f.items[account] = value
	f.log = append(f.log, "set "+account)
	return nil
}

func (f *fakeSecrets) Delete(account string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unavailable {
		return core.ErrSecretsUnavailable
	}
	delete(f.items, account)
	f.log = append(f.log, "delete "+account)
	return nil
}

func (f *fakeSecrets) item(account string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.items[account]
	return v, ok
}

func (f *fakeSecrets) setUnavailable(b bool) {
	f.mu.Lock()
	f.unavailable = b
	f.mu.Unlock()
}

// preload reads the tokens and waits for it to be over.
func preload(t *testing.T, v *vault, ids ...string) {
	t.Helper()
	done := make(chan struct{})
	v.Preload(ids, func() { close(done) })
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the tokens were not read")
	}
}

// settle waits for what the vault queued to reach the store.
func settle(t *testing.T, v *vault) {
	t.Helper()
	done := make(chan struct{})
	v.run(func() { close(done) })
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the store did not answer")
	}
}

func TestVaultReadsTheTokensTheStoreKeeps(t *testing.T) {
	v := newVault(newFakeSecrets(map[string]string{"s1": "tok-1"}), nil)
	if _, ok := v.Get("s1"); ok {
		t.Fatal("a token was there before it was read")
	}
	preload(t, v, "s1", "s2")
	if tok, ok := v.Get("s1"); !ok || tok != "tok-1" {
		t.Fatalf("Get = %q, %v", tok, ok)
	}
	if _, ok := v.Get("s2"); ok {
		t.Fatal("a site without a token has one")
	}
}

func TestVaultKeepsChangesInOrderAndWhatTheUserDidFirst(t *testing.T) {
	b := newFakeSecrets(map[string]string{"s1": "old"})
	b.gate = make(chan struct{})
	v := newVault(b, nil)
	done := make(chan struct{})
	v.Preload([]string{"s1"}, func() { close(done) })
	// The user signs in again while the store is still being read.
	_ = v.Set("s1", "new")
	_ = v.Set("s2", "x")
	_ = v.Remove("s2")
	close(b.gate)
	<-done
	settle(t, v)
	if tok, _ := v.Get("s1"); tok != "new" {
		t.Fatalf("what was read replaced a newer token: %q", tok)
	}
	if tok, ok := b.item("s1"); !ok || tok != "new" {
		t.Fatalf("store holds %q, %v", tok, ok)
	}
	if _, ok := b.item("s2"); ok {
		t.Fatal("a removed token is in the store")
	}
}

func TestVaultRemoveDeletesFromTheStore(t *testing.T) {
	b := newFakeSecrets(map[string]string{"s1": "tok"})
	v := newVault(b, nil)
	preload(t, v, "s1")
	_ = v.Remove("s1")
	settle(t, v)
	if _, ok := b.item("s1"); ok {
		t.Fatal("the token is still in the store")
	}
	if _, ok := v.Get("s1"); ok {
		t.Fatal("the token is still in use")
	}
}

func TestVaultMovesTheTokensOfAnEarlierVersion(t *testing.T) {
	dir := t.TempDir()
	old := &store.FileSecrets{Dir: dir}
	_ = old.Set("s1", "from-file")
	_ = old.Set("s2", "also")
	b := newFakeSecrets(map[string]string{"s2": "newer"})
	v := newVault(b, func() (map[string]string, func()) { return old.All(), old.Wipe })
	preload(t, v, "s1", "s2")
	if tok, _ := v.Get("s1"); tok != "from-file" {
		t.Fatalf("s1 = %q", tok)
	}
	if tok, _ := v.Get("s2"); tok != "newer" {
		t.Fatalf("the store lost to the file: %q", tok)
	}
	if tok, ok := b.item("s1"); !ok || tok != "from-file" {
		t.Fatalf("store holds %q, %v", tok, ok)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("the earlier files are still there: %v", entries)
	}
}

func TestVaultKeepsTheEarlierFilesWhileTheStoreIsMissing(t *testing.T) {
	dir := t.TempDir()
	old := &store.FileSecrets{Dir: dir}
	_ = old.Set("s1", "from-file")
	b := newFakeSecrets(nil)
	b.setUnavailable(true)
	v := newVault(b, func() (map[string]string, func()) { return old.All(), old.Wipe })
	preload(t, v, "s1")
	if _, err := os.Stat(filepath.Join(dir, "tokens.enc")); err != nil {
		t.Fatal("the earlier file went with no store to take it")
	}
}

func TestVaultWithoutAStoreKeepsTokensInMemoryAndTellsOnce(t *testing.T) {
	b := newFakeSecrets(nil)
	b.setUnavailable(true)
	v := newVault(b, nil)
	var mu sync.Mutex
	var told []error
	v.onFail = func(err error) { mu.Lock(); told = append(told, err); mu.Unlock() }
	_ = v.Set("s1", "tok")
	settle(t, v)
	_ = v.Set("s2", "tok-2") // known to be down: no second call
	settle(t, v)
	if tok, ok := v.Get("s1"); !ok || tok != "tok" {
		t.Fatalf("the token was not kept in memory: %q, %v", tok, ok)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(told) != 1 || !errors.Is(told[0], core.ErrSecretsUnavailable) {
		t.Fatalf("told = %v", told)
	}
	if _, ok := b.item("s1"); ok {
		t.Fatal("a token reached a store that is not there")
	}
}

func TestVaultKeepsWhatWasInMemoryOnceTheStoreAppears(t *testing.T) {
	b := newFakeSecrets(nil)
	b.setUnavailable(true)
	v := newVault(b, nil)
	_ = v.Set("s1", "tok")
	settle(t, v)
	if err := v.Recheck(); !errors.Is(err, core.ErrSecretsUnavailable) {
		t.Fatalf("Recheck = %v", err)
	}
	b.setUnavailable(false) // the user installed a Secret Service
	if err := v.Recheck(); err != nil {
		t.Fatal(err)
	}
	// Recheck answers once the token is in the store, with no waiting here
	if tok, ok := b.item("s1"); !ok || tok != "tok" {
		t.Fatalf("the token was not moved to the store: %q, %v", tok, ok)
	}
	_ = v.Set("s2", "next")
	settle(t, v)
	if _, ok := b.item("s2"); !ok {
		t.Fatal("later tokens are not kept in the store")
	}
}

func TestSignInSurvivesARestartThroughTheSecureStore(t *testing.T) {
	srv := newFakeServer(t)
	b := newFakeSecrets(map[string]string{"s1": "good"})
	app := startWith(t, newVault(b, nil), seed(srv.URL))
	// The folder waits for the token, and then reads as the account.
	app.WaitFor("id:file-Docs", 2*time.Second)
	app.Press("id:tab-settings")
	app.Press("text:Test")
	app.WaitFor("text:"+srv.URL+" · Signed in as alice · User", 2*time.Second)
}

func TestFilesWaitForTheSecureStoreToAnswer(t *testing.T) {
	srv := newFakeServer(t)
	b := newFakeSecrets(map[string]string{"s1": "good"})
	b.gate = make(chan struct{})
	app := startWith(t, newVault(b, nil), seed(srv.URL))
	app.Advance(200 * time.Millisecond)
	for _, s := range app.Texts() {
		if s == "Docs" {
			t.Fatal("the folder was read before the store answered")
		}
	}
	close(b.gate)
	app.WaitFor("id:file-Docs", 2*time.Second)
}

func TestSignOutDeletesTheTokenFromTheSecureStore(t *testing.T) {
	srv := newFakeServer(t)
	b := newFakeSecrets(map[string]string{"s1": "good"})
	v := newVault(b, nil)
	app := startWith(t, v, seed(srv.URL))
	app.WaitFor("id:file-Docs", 2*time.Second)
	app.Press("id:tab-settings")
	app.Press("text:Sign out")
	app.WaitFor("text:Signed out of Home; the login saved on this device is deleted", time.Second)
	app.WaitUntil(2*time.Second, func() bool { _, ok := b.item("s1"); return !ok })
}

func TestLinuxWithoutASecretServiceSaysWhatToInstall(t *testing.T) {
	startupChecks = true
	t.Cleanup(func() { startupChecks = false })
	old := onLinux
	onLinux = func() bool { return true }
	t.Cleanup(func() { onLinux = old })
	srv := newFakeServer(t)
	b := newFakeSecrets(nil)
	b.setUnavailable(true)
	app := startWith(t, newVault(b, nil), seed(srv.URL))
	app.WaitFor("id:access-issue", 2*time.Second)
	app.WaitFor("text:Can't keep your sign-in safely", time.Second)
	app.WaitFor("id:access-recheck", time.Second)
	// The user installs a Secret Service and checks again.
	b.setUnavailable(false)
	app.Press("id:access-recheck")
	app.WaitFor("text:All permissions are in place", 2*time.Second)
}

func TestVaultQueueNeverMakesTheCallerWait(t *testing.T) {
	b := newFakeSecrets(nil)
	b.gate = make(chan struct{}) // the store is waiting on the user
	v := newVault(b, nil)
	done := make(chan struct{})
	go func() {
		defer close(done)
		v.Preload([]string{"s0"}, func() {}) // holds the store's goroutine
		for i := 0; i < 1000; i++ {
			_ = v.Set("s", "tok")
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("queueing waited for the store")
	}
	close(b.gate)
	settle(t, v)
	if tok, ok := b.item("s"); !ok || tok != "tok" {
		t.Fatalf("the writes were lost: %q, %v", tok, ok)
	}
}

func TestRecheckWithoutAStoreStopsLaterTokensFromQueueing(t *testing.T) {
	b := newFakeSecrets(nil)
	b.setUnavailable(true)
	v := newVault(b, nil) // no sites, so nothing was read and the store has not refused yet
	told := 0
	v.onFail = func(error) { told++ }
	if err := v.Recheck(); !errors.Is(err, core.ErrSecretsUnavailable) {
		t.Fatalf("Recheck = %v", err)
	}
	_ = v.Set("s1", "tok")
	settle(t, v)
	if told != 0 {
		t.Fatalf("the refusal was told %d more times", told)
	}
	if tok, ok := v.Get("s1"); !ok || tok != "tok" {
		t.Fatal("the token is not kept in memory")
	}
	b.setUnavailable(false)
	if err := v.Recheck(); err != nil {
		t.Fatal(err)
	}
	if tok, ok := b.item("s1"); !ok || tok != "tok" {
		t.Fatalf("the token did not reach the store once it appeared: %q, %v", tok, ok)
	}
}
