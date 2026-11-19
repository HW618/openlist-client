package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nsgo-dev/nsgo/core"

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
