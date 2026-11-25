package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/nsgotest"

	"github.com/HW618/openlist-client/src/internal/store"
	"github.com/HW618/openlist-client/src/internal/transfer"
)

func TestStartupRelocatesIOSPathsAndResumesWithPersistedValidator(t *testing.T) {
	const (
		oldUUID = "11111111-1111-1111-1111-111111111111"
		newUUID = "22222222-2222-2222-2222-222222222222"
	)
	base := t.TempDir()
	oldLocalDir := filepath.ToSlash(filepath.Join(base, "Containers", "Data", "Application", oldUUID, "Documents", "OpenList"))
	newHome := filepath.Join(base, "Containers", "Data", "Application", newUUID)
	newLocalDir := filepath.Join(newHome, "Documents", "OpenList")
	if err := os.MkdirAll(newLocalDir, 0o755); err != nil {
		t.Fatal(err)
	}

	prevRelocate := relocateSavedPath
	relocateSavedPath = func(p string) string {
		return relocateIOSContainerPath(p, newHome)
	}
	t.Cleanup(func() { relocateSavedPath = prevRelocate })

	v1Payload := bytes.Repeat([]byte("1"), 800)
	v2Payload := bytes.Repeat([]byte("2"), 1000)
	if err := os.WriteFile(transfer.PartPath(newLocalDir, "ios.bin", "tr-ios-1"), v1Payload[:300], 0o644); err != nil {
		t.Fatal(err)
	}

	var sawRange, sawIfRange atomic.Value
	sawRange.Store("")
	sawIfRange.Store("")

	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, data any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success", "data": data})
	}
	mux.HandleFunc("/api/fs/list", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"content": []any{}, "total": 0, "provider": "Local"})
	})
	mux.HandleFunc("/api/fs/get", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"name": "ios.bin", "size": len(v2Payload), "raw_url": "http://" + r.Host + "/d/ios.bin"})
	})
	mux.HandleFunc("/d/ios.bin", func(w http.ResponseWriter, r *http.Request) {
		rng := r.Header.Get("Range")
		ifRange := r.Header.Get("If-Range")
		sawRange.Store(rng)
		sawIfRange.Store(ifRange)
		w.Header().Set("ETag", `"v2"`)
		if ifRange == `"v2"` && rng != "" {
			w.WriteHeader(http.StatusPartialContent)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(v2Payload)))
		_, _ = w.Write(v2Payload)
	})
	srv := httptest.NewServer(cors(mux))
	t.Cleanup(srv.Close)

	sites, _ := json.Marshal(map[string]any{"version": 1, "value": []store.Site{
		{ID: "s1", Name: "Home", URL: srv.URL, Settings: store.SiteSettings{LocalDir: oldLocalDir}},
	}})
	tasks, _ := json.Marshal(map[string]any{"version": 1, "value": []transfer.Task{{
		ID:         "tr-ios-1",
		Name:       "ios.bin",
		Direction:  transfer.Download,
		SiteID:     "s1",
		SiteName:   "Home",
		RemotePath: "/ios.bin",
		LocalPath:  oldLocalDir,
		Validator:  `"v1"`,
		Total:      int64(len(v1Payload)),
		Done:       0,
		Status:     transfer.Active,
	}}})

	app := start(t, nsgotest.Storage(map[string]string{
		"sites":       string(sites),
		"currentSite": `{"version":1,"value":"s1"}`,
		"transfers":   string(tasks),
	}))

	// 1. Site LocalDir is relocated and persisted in sitesStore.
	rawSites, ok := app.Storage().GetItem("sites")
	if !ok {
		t.Fatal("missing sites in storage")
	}
	var savedSites struct {
		Value []store.Site `json:"value"`
	}
	if err := json.Unmarshal([]byte(rawSites), &savedSites); err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.ToSlash(newLocalDir)
	if len(savedSites.Value) != 1 || filepath.ToSlash(savedSites.Value[0].Settings.LocalDir) != wantDir {
		t.Fatalf("relocated site LocalDir = %+v, want %q", savedSites.Value, wantDir)
	}

	// 2. Relocated task discovers .part, sends Range + If-Range, restarts on ETag change, and saves intact v2 file.
	app.WaitUntil(5*time.Second, func() bool {
		b, err := os.ReadFile(filepath.Join(newLocalDir, "ios.bin"))
		return err == nil && bytes.Equal(b, v2Payload)
	})
	if got, _ := sawRange.Load().(string); got != "bytes=300-" {
		t.Fatalf("Range header = %q, want bytes=300-", got)
	}
	if got, _ := sawIfRange.Load().(string); got != `"v1"` {
		t.Fatalf("If-Range header = %q, want \"v1\"", got)
	}

	// 3. Task LocalPath is relocated and Validator is cleared once Done.
	app.WaitUntil(2*time.Second, func() bool {
		rawTasks, ok := app.Storage().GetItem("transfers")
		if !ok {
			return false
		}
		var savedTasks struct {
			Value []transfer.Task `json:"value"`
		}
		if err := json.Unmarshal([]byte(rawTasks), &savedTasks); err != nil || len(savedTasks.Value) != 1 {
			return false
		}
		tk := savedTasks.Value[0]
		return tk.Status == transfer.Done && filepath.ToSlash(tk.LocalPath) == wantDir && tk.Validator == ""
	})
}

func TestSyncTasksPersistsValidatorChange(t *testing.T) {
	payload := bytes.Repeat([]byte("S"), 1000)
	unblock := make(chan struct{})
	var released atomic.Bool
	release := func() {
		if released.CompareAndSwap(false, true) {
			close(unblock)
		}
	}

	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, data any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success", "data": data})
	}
	mux.HandleFunc("/api/fs/list", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{
			"content":  []map[string]any{{"name": "sync.bin", "size": len(payload), "is_dir": false}},
			"total":    1,
			"provider": "Local",
		})
	})
	mux.HandleFunc("/api/fs/get", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"name": "sync.bin", "size": len(payload), "raw_url": "http://" + r.Host + "/d/sync.bin"})
	})
	mux.HandleFunc("/d/sync.bin", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = w.Write(payload[:400])
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-unblock:
			_, _ = w.Write(payload[400:])
		case <-r.Context().Done():
		}
	})
	srv := httptest.NewServer(cors(mux))
	// Register unblock cleanup after srv.Close cleanup so t.Cleanup (LIFO) unblocks handlers before srv.Close waits.
	t.Cleanup(srv.Close)
	t.Cleanup(release)

	dir := t.TempDir()
	app := start(t, seedIn(srv.URL, dir))
	app.WaitFor("id:file-sync.bin", 2*time.Second)
	app.LongPress("id:file-sync.bin")
	app.Press("id:batch-download")

	// Validator must be persisted in storage while the download is still active.
	app.WaitUntil(3*time.Second, func() bool {
		raw, ok := app.Storage().GetItem("transfers")
		if !ok {
			return false
		}
		var saved struct {
			Value []transfer.Task `json:"value"`
		}
		if err := json.Unmarshal([]byte(raw), &saved); err != nil || len(saved.Value) != 1 {
			return false
		}
		return saved.Value[0].Status == transfer.Active && saved.Value[0].Validator == `"v1"`
	})

	release()
	app.WaitUntil(5*time.Second, func() bool {
		b, err := os.ReadFile(filepath.Join(dir, "sync.bin"))
		return err == nil && bytes.Equal(b, payload)
	})
	app.WaitUntil(2*time.Second, func() bool {
		raw, ok := app.Storage().GetItem("transfers")
		if !ok {
			return false
		}
		var saved struct {
			Value []transfer.Task `json:"value"`
		}
		if err := json.Unmarshal([]byte(raw), &saved); err != nil || len(saved.Value) != 1 {
			return false
		}
		return saved.Value[0].Status == transfer.Done && saved.Value[0].Validator == ""
	})
}
