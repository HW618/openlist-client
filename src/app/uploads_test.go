package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/nsgotest"

	"github.com/HW618/openlist-client/src/internal/store"
	"github.com/HW618/openlist-client/src/internal/transfer"
)

func TestPickedFilesAreLetGoWhenTheirUploadsAreOver(t *testing.T) {
	dir := t.TempDir()
	copyOf := filepath.Join(dir, "copy.bin")
	if err := os.WriteFile(copyOf, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &App{
		uploadFile: map[string]core.File{
			"mem:done":  {Name: "done"},
			"mem:fail":  {Name: "fail"},
			"mem:gone":  {Name: "gone"},
			copyOf:      {Name: "copy.bin", Path: copyOf, Temporary: true},
			"mem:twice": {Name: "twice"},
		},
		uploadSeen: map[string]bool{},
	}
	up := func(key string, st transfer.Status) transfer.Task {
		return transfer.Task{ID: key + string(st), Direction: transfer.Upload, LocalPath: key, Status: st}
	}
	a.releaseUploads([]transfer.Task{
		up("mem:done", transfer.Active), up("mem:fail", transfer.Failed), up("mem:gone", transfer.Paused),
		up(copyOf, transfer.Active), up("mem:twice", transfer.Done), up("mem:twice", transfer.Active),
	})
	if len(a.uploadFile) != 5 {
		t.Fatalf("files in use or to be retried were let go: %d left", len(a.uploadFile))
	}
	// done is released; failed and paused uploads keep theirs; a file queued
	// twice stays while one of them is to go; "gone" was taken off the list
	a.releaseUploads([]transfer.Task{
		up("mem:done", transfer.Done), up("mem:fail", transfer.Failed),
		up(copyOf, transfer.Done), up("mem:twice", transfer.Active),
	})
	for _, k := range []string{"mem:done", "mem:gone", copyOf} {
		if _, ok := a.uploadFile[k]; ok {
			t.Errorf("%s was kept after its upload was over", k)
		}
	}
	for _, k := range []string{"mem:fail", "mem:twice"} {
		if _, ok := a.uploadFile[k]; !ok {
			t.Errorf("%s was let go while it may still be needed", k)
		}
	}
	for i := 0; i < 100; i++ { // the copy the platform made is deleted, off this goroutine
		if _, err := os.Stat(copyOf); os.IsNotExist(err) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the temporary copy was not deleted")
}

func TestUploadNotLimitedByAPIHTTPTimeout(t *testing.T) {
	oldAPI := apiHTTP
	apiHTTP = &http.Client{Timeout: 40 * time.Millisecond}
	t.Cleanup(func() { apiHTTP = oldAPI })

	var putLen atomic.Int64
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, data any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success", "data": data})
	}
	mux.HandleFunc("/api/fs/list", func(w http.ResponseWriter, r *http.Request) {
		reply(w, map[string]any{"content": []any{}, "total": 0, "provider": "Local"})
	})
	mux.HandleFunc("/api/fs/put", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(120 * time.Millisecond) // longer than apiHTTP.Timeout (40ms)
		b, _ := io.ReadAll(r.Body)
		putLen.Store(int64(len(b)))
		reply(w, nil)
	})
	srv := httptest.NewServer(cors(mux))
	t.Cleanup(srv.Close)

	src := filepath.Join(t.TempDir(), "slow-upload.bin")
	if err := os.WriteFile(src, []byte("payload-12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	sites, _ := json.Marshal(map[string]any{"version": 1, "value": []store.Site{{ID: "s1", Name: "Home", URL: srv.URL}}})
	app := start(t, nsgotest.Storage(map[string]string{
		"sites": string(sites), "currentSite": `{"version":1,"value":"s1"}`,
	}))
	app.PickFiles = func(nsgotest.FilePick) []core.File {
		return []core.File{{Name: "slow-upload.bin", Path: src, Size: int64(len("payload-12345"))}}
	}
	app.WaitFor("id:fab", 2*time.Second)
	app.Press("id:fab")
	app.WaitFor("text:Upload files", 2*time.Second)
	app.Press("text:Upload files")
	app.WaitFor("id:upload-pick", 2*time.Second)
	app.Press("id:upload-pick")

	app.Press("id:tab-transfers")
	app.WaitFor("text:Done · 13 B", 3*time.Second)
	if putLen.Load() != int64(len("payload-12345")) {
		t.Fatalf("uploaded bytes = %d, want %d", putLen.Load(), len("payload-12345"))
	}
}
