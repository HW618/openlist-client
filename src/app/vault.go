package app

import (
	"errors"
	"sync"
	"time"

	"github.com/nsgo-dev/nsgo/core"

	"github.com/HW618/openlist-client/src/internal/access"
	"github.com/HW618/openlist-client/src/locales"
)

// secretBackend is where the platform keeps secrets: core.Secrets, which is
// the keychain on Apple platforms, the Credential Manager on Windows, the
// Secret Service on Linux and the Android Keystore on Android.
type secretBackend interface {
	Get(account string) (value string, ok bool, err error)
	Set(account, value string) error
	Delete(account string) error
}

// probeAccount is the account the app reads to find out whether the platform
// has a secure store.
const probeAccount = "openlist-client-probe"

// vault keeps the tokens of the sites in the platform's secure store.
//
// The store may make a call wait, as a keychain does while it asks the user,
// so none of its calls run on the engine's goroutine: the tokens are read once
// off it into memory, which Get answers from, and each change is written
// through a queue, in order, off it.
//
// Where the platform has no secure store, as in a browser, or a Linux session
// without a Secret Service, the tokens stay in memory until the app closes:
// nothing falls back to a plain file.
type vault struct {
	backend secretBackend
	// legacy gives the tokens an earlier version kept in a file of its own,
	// and what deletes them once the secure store has them.
	legacy func() (tokens map[string]string, wipe func())
	// onFail hears of a call the store refused, on any goroutine.
	onFail func(error)

	mu      sync.Mutex
	cache   map[string]string
	touched map[string]bool // changed since the app started, which what is read must not replace
	down    bool            // the platform has no secure store

	// queue is the work for the store, in order. It has no bound, so that a
	// store that waits on the user, as a keychain does, never makes the call
	// that queued behind it wait.
	qmu   sync.Mutex
	queue []func()
	wake  chan struct{}
	once  sync.Once
}

func newVault(b secretBackend, legacy func() (map[string]string, func())) *vault {
	return &vault{backend: b, legacy: legacy, cache: map[string]string{}, touched: map[string]bool{}, wake: make(chan struct{}, 1)}
}

// run queues work for the goroutine that serves the store, which does it in
// the order it was queued. It never waits.
func (v *vault) run(job func()) {
	v.once.Do(func() { go v.serve() })
	v.qmu.Lock()
	v.queue = append(v.queue, job)
	v.qmu.Unlock()
	select {
	case v.wake <- struct{}{}:
	default:
	}
}

// serve does the queued work, one at a time.
func (v *vault) serve() {
	for range v.wake {
		for {
			v.qmu.Lock()
			if len(v.queue) == 0 {
				v.qmu.Unlock()
				break
			}
			job := v.queue[0]
			v.queue[0] = nil
			v.queue = v.queue[1:]
			v.qmu.Unlock()
			job()
		}
	}
}

// fail notes a refusal of the store, and tells onFail.
func (v *vault) fail(err error) {
	if errors.Is(err, core.ErrSecretsUnavailable) {
		v.mu.Lock()
		v.down = true
		v.mu.Unlock()
	}
	if v.onFail != nil {
		v.onFail(err)
	}
}

// Get implements store.Secrets.
func (v *vault) Get(siteID string) (string, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	t, ok := v.cache[siteID]
	return t, ok
}

// Set implements store.Secrets: the token is in use at once and kept as the
// store gets to it.
func (v *vault) Set(siteID, token string) error {
	v.mu.Lock()
	v.cache[siteID] = token
	v.touched[siteID] = true
	down := v.down
	v.mu.Unlock()
	if !down {
		v.run(func() {
			if err := v.backend.Set(siteID, token); err != nil {
				v.fail(err)
			}
		})
	}
	return nil
}

// Remove implements store.Secrets.
func (v *vault) Remove(siteID string) error {
	v.mu.Lock()
	delete(v.cache, siteID)
	v.touched[siteID] = true
	down := v.down
	v.mu.Unlock()
	if !down {
		v.run(func() {
			if err := v.backend.Delete(siteID); err != nil {
				v.fail(err)
			}
		})
	}
	return nil
}

// Preload reads the tokens of the sites from the store, brings in those an
// earlier version kept in a file, and calls done, on the goroutine that
// served the store, when it is over, whatever came of it.
func (v *vault) Preload(ids []string, done func()) {
	v.run(func() {
		defer done()
		found := map[string]string{}
		for _, id := range ids {
			tok, ok, err := v.backend.Get(id)
			if err != nil {
				v.fail(err)
				break
			}
			if ok && tok != "" {
				found[id] = tok
			}
		}
		v.mu.Lock()
		for id, tok := range found {
			if !v.touched[id] {
				v.cache[id] = tok
			}
		}
		down := v.down
		v.mu.Unlock()
		if down || len(ids) == 0 || v.legacy == nil {
			return
		}
		v.migrate(ids, found)
	})
}

// migrate moves the tokens of an earlier version into the store, and deletes
// that version's file once they are all there.
func (v *vault) migrate(ids []string, found map[string]string) {
	old, wipe := v.legacy()
	for _, id := range ids {
		tok, ok := old[id]
		if !ok || tok == "" {
			continue
		}
		if _, have := found[id]; have {
			continue
		}
		v.mu.Lock()
		skip := v.touched[id]
		v.mu.Unlock()
		if skip {
			continue
		}
		if err := v.backend.Set(id, tok); err != nil {
			v.fail(err)
			return
		}
		v.mu.Lock()
		v.cache[id] = tok
		v.mu.Unlock()
	}
	wipe()
}

// recheckWait is how long Recheck waits for the tokens it queued to reach the
// store, which may be asking the user.
const recheckWait = 15 * time.Second

// Recheck finds out whether the platform has a secure store now, as after the
// user installed a Secret Service, and when it has after it had none, keeps
// the tokens that were only in memory in it, and answers when they are there.
// It may wait on the store, so it is not for the engine's goroutine.
func (v *vault) Recheck() error {
	if _, _, err := v.backend.Get(probeAccount); err != nil {
		if errors.Is(err, core.ErrSecretsUnavailable) {
			// no store: later tokens stay in memory instead of queueing for a
			// store that will refuse them, one more time each
			v.mu.Lock()
			v.down = true
			v.mu.Unlock()
		}
		return err
	}
	v.mu.Lock()
	was := v.down
	v.down = false
	held := make(map[string]string, len(v.cache))
	for id, tok := range v.cache {
		held[id] = tok
	}
	v.mu.Unlock()
	if !was {
		return nil
	}
	kept := make(chan error, 1)
	v.run(func() {
		for id, tok := range held {
			if err := v.backend.Set(id, tok); err != nil {
				v.fail(err)
				kept <- err
				return
			}
		}
		kept <- nil
	})
	select {
	case err := <-kept:
		return err
	case <-time.After(recheckWait):
		return nil
	}
}

// preloadTokens reads the tokens of the saved sites from the secure store, off
// the engine's goroutine, and lets the screens ask the sites as they come.
func (a *App) preloadTokens() {
	a.ready.Set(false)
	a.vault.onFail = func(err error) { a.cx.Post(func() { a.secretsFailed(err) }) }
	var ids []string
	for _, s := range a.sites.GetUntracked() {
		ids = append(ids, s.ID)
	}
	a.vault.Preload(ids, func() {
		a.cx.Post(func() {
			a.cx.Batch(func() {
				a.syncClients()
				a.logins.Update(func(n int) int { return n + 1 })
				a.ready.Set(true)
			})
		})
	})
}

// secretsFailed tells the user the secure store refused the app: a Linux
// session without a Secret Service gets the panel that says what to install;
// a browser has none by nature, and keeps the sign-in in memory without a word.
func (a *App) secretsFailed(err error) {
	if errors.Is(err, core.ErrSecretsUnavailable) {
		switch {
		case isWeb():
		case onLinux():
			if !a.keychainTold {
				a.keychainTold = true
				a.raise([]accessIssue{{Kind: access.Keychain, Detail: err.Error()}})
			}
		default:
			a.toast(locales.ToastError(a.cx, errText(err)))
		}
		return
	}
	a.toast(locales.ToastError(a.cx, errText(err)))
}
