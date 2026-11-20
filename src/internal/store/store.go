// Package store holds what the app keeps between runs, as plain values: the
// saved OpenList sites with their own settings, the app's preferences, and a
// place for the sites' tokens.
package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/url"
	"strings"
	"sync"
)

// Site is one saved OpenList server. Its token is kept apart, in Secrets.
type Site struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	URL      string       `json:"url"`
	Settings SiteSettings `json:"settings"`
}

// SiteSettings are the settings of one site: each site has its own.
type SiteSettings struct {
	// FilesPath is the folder the Files page opens when the app starts or the
	// site is switched to; "" is the root.
	FilesPath string `json:"filesPath,omitempty"`
	// OfflinePath is where an offline download is saved from the root; ""
	// is the root.
	OfflinePath string `json:"offlinePath,omitempty"`
	// LocalDir is where downloads are saved on this device; "" is the app's
	// own default.
	LocalDir string `json:"localDir,omitempty"`
	// RecentOffline are the last folders offline downloads were saved to,
	// newest first, at most MaxRecent.
	RecentOffline []string `json:"recentOffline,omitempty"`
}

// MaxRecent is how many recent offline folders a site remembers.
const MaxRecent = 3

// PushRecent makes dir the newest recent offline folder.
func (s *SiteSettings) PushRecent(dir string) {
	out := []string{dir}
	for _, d := range s.RecentOffline {
		if d != dir && len(out) < MaxRecent {
			out = append(out, d)
		}
	}
	s.RecentOffline = out
}

// ViewMode is how a folder's content is laid out.
type ViewMode string

const (
	ViewList ViewMode = "list"
	ViewGrid ViewMode = "grid"
)

// SortKey is what a folder's content is ordered by.
type SortKey string

const (
	SortName SortKey = "name"
	SortSize SortKey = "size"
	SortDate SortKey = "date"
)

// Next is the key after k in the order name, size, date.
func (k SortKey) Next() SortKey {
	switch k {
	case SortName:
		return SortSize
	case SortSize:
		return SortDate
	}
	return SortName
}

// Prefs are the app-wide preferences, the same for every site.
type Prefs struct {
	Dark bool `json:"dark"`
	// View and Sort are what a folder starts with; the Files page can change
	// them for the moment.
	View     ViewMode `json:"view"`
	Sort     SortKey  `json:"sort"`
	SortDesc bool     `json:"sortDesc,omitempty"`
	// Notifications are task-completion notification toggles. When Set is
	// false (older prefs or a first run), Master/Success/Failure default on.
	Notifications NotifyPrefs `json:"notifications,omitempty"`
}

// NotifyPrefs are whether the app shows system notifications for transfers
// and offline tasks.
type NotifyPrefs struct {
	// Set is true once defaults have been written or the user changed a
	// toggle; when false, Master/Success/Failure are treated as on.
	Set bool `json:"set,omitempty"`
	// Master is the overall switch.
	Master bool `json:"master"`
	// Success and Failure control which outcomes notify when Master is on.
	Success bool `json:"success"`
	Failure bool `json:"failure"`
	// Asked is whether the app has already guided the user to grant
	// notification permission (first launch or Settings).
	Asked bool `json:"asked,omitempty"`
}

// DefaultPrefs are the preferences of a first run.
func DefaultPrefs() Prefs { return Prefs{View: ViewList, Sort: SortName} }

// Fix replaces what is missing or unknown with the defaults.
func (p Prefs) Fix() Prefs {
	if p.View != ViewGrid {
		p.View = ViewList
	}
	if p.Sort != SortSize && p.Sort != SortDate {
		p.Sort = SortName
	}
	return p
}

// NotifyMaster reports whether task notifications are enabled (default on).
func (p Prefs) NotifyMaster() bool {
	if !p.Notifications.Set {
		return true
	}
	return p.Notifications.Master
}

// NotifySuccess reports whether successful tasks notify (default on).
func (p Prefs) NotifySuccess() bool {
	if !p.Notifications.Set {
		return true
	}
	return p.Notifications.Success
}

// NotifyFailure reports whether failed tasks notify (default on).
func (p Prefs) NotifyFailure() bool {
	if !p.Notifications.Set {
		return true
	}
	return p.Notifications.Failure
}

// EnsureNotifyPrefs writes the default-on notification prefs once Set is needed.
func (p *Prefs) EnsureNotifyPrefs() {
	if p.Notifications.Set {
		return
	}
	p.Notifications = NotifyPrefs{Set: true, Master: true, Success: true, Failure: true}
}

// NormalizeURL checks that raw is the address of a server, an http or https
// URL, and returns it without a trailing slash.
func NormalizeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("store: the address is no http or https URL")
	}
	u.Fragment, u.RawQuery = "", ""
	return strings.TrimRight(u.String(), "/"), nil
}

// NewID returns an identifier for a new site.
func NewID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return "site-" + hex.EncodeToString(b[:])
}

// Secrets keeps the token of each site.
type Secrets interface {
	Get(siteID string) (token string, ok bool)
	Set(siteID, token string) error
	Remove(siteID string) error
}

// MemorySecrets keeps tokens until the app closes, for where there is no
// folder to keep them in.
type MemorySecrets struct {
	mu sync.Mutex
	m  map[string]string
}

// Get implements Secrets.
func (s *MemorySecrets) Get(siteID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.m[siteID]
	return t, ok
}

// Set implements Secrets.
func (s *MemorySecrets) Set(siteID, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[siteID] = token
	return nil
}

// Remove implements Secrets.
func (s *MemorySecrets) Remove(siteID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, siteID)
	return nil
}
