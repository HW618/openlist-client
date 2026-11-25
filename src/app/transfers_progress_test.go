package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"github.com/HW618/openlist-client/src/internal/store"
)

// Progress bars must use ui.Progress's 0–100 scale. Passing a 0–1 fraction
// paints ~1% of the track when the text already says ~89%.
func TestOfflineProgressBarUsesPercentScale(t *testing.T) {
	srv := newFakeServer(t)
	app := start(t, seed(srv.URL))
	app.WaitFor("id:file-Docs", 2*time.Second)
	app.Press("id:tab-transfers")
	app.WaitFor("text:Downloading · 42% · [aria2]: downloading", 2*time.Second)

	var now float32
	app.WaitUntil(2*time.Second, func() bool {
		if !app.Exists("role:progressbar") {
			return false
		}
		now = app.Find("role:progressbar").Props.Value.Now
		return now >= 40
	})
	if now < 40 || now > 100 {
		t.Fatalf("offline progress bar Now = %v, want about 42 on the 0–100 scale", now)
	}
}

func TestTransferProgressBarAndBannerUsePercentScale(t *testing.T) {
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
	mux.HandleFunc("/api/admin/task/", func(w http.ResponseWriter, r *http.Request) {
		reply(w, []any{})
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
		Notifications: store.NotifyPrefs{Set: true, Master: true, Asked: true, Requested: true},
	}})
	sites, _ := json.Marshal(map[string]any{"version": 1, "value": []store.Site{
		{ID: "s1", Name: "Home", URL: srv.URL, Settings: store.SiteSettings{LocalDir: dir}},
	}})
	app := start(t, nsgotest.Storage(map[string]string{
		"sites": string(sites), "currentSite": `{"version":1,"value":"s1"}`, "prefs": string(prefs),
	}))
	app.NotificationPermission = core.NotificationsGranted
	app.WaitFor("id:file-movie.bin", 2*time.Second)
	app.LongPress("id:file-movie.bin")
	app.Press("id:batch-download")

	<-half
	app.Press("id:tab-transfers")
	app.WaitFor("id:transfer-banner", 2*time.Second)

	var taskNow, bannerNow float32
	app.WaitUntil(3*time.Second, func() bool {
		if !app.Exists(`role:progressbar movie.bin`) {
			return false
		}
		taskNow = app.Find(`role:progressbar movie.bin`).Props.Value.Now
		bannerNow = app.Find(`role:progressbar OpenList Client · Transfer progress`).Props.Value.Now
		return taskNow >= 40 && bannerNow >= 40
	})
	if taskNow < 40 || taskNow > 100 {
		t.Fatalf("task progress bar Now = %v, want about 50 on the 0–100 scale", taskNow)
	}
	if bannerNow < 40 || bannerNow > 100 {
		t.Fatalf("banner progress bar Now = %v, want about 50 on the 0–100 scale", bannerNow)
	}

	close(finish)
	app.WaitUntil(5*time.Second, func() bool {
		b, err := os.ReadFile(filepath.Join(dir, "movie.bin"))
		return err == nil && len(b) == 1000
	})
}
