package access

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestClassifyTellsPermissionFromOtherFailures(t *testing.T) {
	for msg, want := range map[string]Kind{
		"open /Users/a/Downloads/x.part: permission denied":                                                          Folder,
		"mkdir /Volumes/X: read-only file system":                                                                    Folder,
		"Post \"http://192.168.1.2:5244/api/fs/list\": dial tcp 192.168.1.2:5244: connect: no route to host":         Network,
		"dial tcp 10.0.0.1:80: connect: operation not permitted":                                                     Network,
		"download: the server answered 403 Forbidden":                                                                Server,
		"openlist: permission denied (403)":                                                                          Server,
		"openlist: token is expired (401)":                                                                           Server,
		"Failed to execute 'createWritable' on 'FileSystemFileHandle': The request is not allowed by the user agent": Browser,
		"download: the connection closed before the file ended":                                                      None,
		"no space left on device":                                                                                    None,
		"":                                                                                                           None,
	} {
		if got := ClassifyText(msg).Kind; got != want {
			t.Errorf("ClassifyText(%q) = %v, want %v", msg, got, want)
		}
	}
	if Classify(&os.PathError{Op: "open", Path: "/x", Err: os.ErrPermission}).Kind != Folder {
		t.Error("an os permission error is about a folder")
	}
	if Classify(errors.New("boom")).Kind != None || Classify(nil).Kind != None {
		t.Error("other errors are no permission matter")
	}
}

func TestProbeDirWritesAndCleansUp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")
	if err := ProbeDir(dir); err != nil {
		t.Fatal(err)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Fatalf("probe left %v", left)
	}
	if os.Geteuid() == 0 {
		return
	}
	ro := t.TempDir()
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(ro, 0o700)
	if err := ProbeDir(ro); err == nil || Classify(err).Kind != Folder {
		t.Fatalf("a folder that cannot be written: err = %v", err)
	}
}

func TestProbeNetworkCountsAnyAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", 404) }))
	if err := ProbeNetwork(context.Background(), srv.Client(), srv.URL); err != nil {
		t.Fatal(err)
	}
	srv.Close()
	if err := ProbeNetwork(context.Background(), http.DefaultClient, srv.URL); err == nil {
		t.Fatal("a closed server cannot be reached")
	}
}
