package transfer

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const body = "0123456789abcdefghij"

// server serves body, honoring Range unless ignoreRange.
func server(ignoreRange bool) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rng := r.Header.Get("Range")
		if rng == "" || ignoreRange {
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			_, _ = w.Write([]byte(body))
			return
		}
		from, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rng, "bytes="), "-"))
		if from >= len(body) {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)-from))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte(body[from:]))
	}))
}

func TestFetchSavesFile(t *testing.T) {
	s := server(false)
	defer s.Close()
	dir := t.TempDir()
	var last, total int64
	p, err := Fetch(context.Background(), nil, s.URL, nil, dir, "a.txt", func(d, t int64) { last, total = d, t })
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != body || last != int64(len(body)) || total != int64(len(body)) {
		t.Fatalf("got %q done %d total %d", got, last, total)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.txt"+PartSuffix)); err == nil {
		t.Fatal("part file left behind")
	}
}

func TestFetchResumesFromPart(t *testing.T) {
	s := server(false)
	defer s.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"+PartSuffix), []byte(body[:8]), 0o644); err != nil {
		t.Fatal(err)
	}
	var first int64 = -1
	p, err := Fetch(context.Background(), nil, s.URL, nil, dir, "a.txt", func(d, _ int64) {
		if first < 0 {
			first = d
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != body {
		t.Fatalf("got %q", got)
	}
	if first != 8 {
		t.Fatalf("started at %d, want 8", first)
	}
}

func TestFetchRestartsWhenServerIgnoresRange(t *testing.T) {
	s := server(true)
	defer s.Close()
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"+PartSuffix), []byte("xx"), 0o644)
	p, err := Fetch(context.Background(), nil, s.URL, nil, dir, "a.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); string(got) != body {
		t.Fatalf("got %q", got)
	}
}

func TestFetchNeverOverwrites(t *testing.T) {
	s := server(false)
	defer s.Close()
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("mine"), 0o644)
	p, err := Fetch(context.Background(), nil, s.URL, nil, dir, "a.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != "a (1).txt" {
		t.Fatalf("saved to %s", p)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(got) != "mine" {
		t.Fatal("overwrote the existing file")
	}
}

func TestFetchFailsOnHTTPError(t *testing.T) {
	s := httptest.NewServer(http.NotFoundHandler())
	defer s.Close()
	if _, err := Fetch(context.Background(), nil, s.URL, nil, t.TempDir(), "a", nil); err == nil {
		t.Fatal("expected an error")
	}
}

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{
		"a/b:c":       "a_b_c",
		"  ":          "download",
		"..":          "download",
		"ok.txt":      "ok.txt",
		"a\x00b\nc":   "a_b_c",
		"file. ":      "file",
		"CON":         "_CON",
		"con.txt":     "_con.txt",
		"LPT1.tar.gz": "_LPT1.tar.gz",
	} {
		if got := SafeName(in); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFetchBytesReadsTheFileAndReportsProgress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "tok" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte("0123456789"))
	}))
	defer srv.Close()
	var last, total int64
	h := http.Header{}
	h.Set("Authorization", "tok")
	got, err := FetchBytes(context.Background(), srv.Client(), srv.URL, h, 0, func(d, t int64) { last, total = d, t })
	if err != nil || string(got) != "0123456789" || last != 10 || total != 10 {
		t.Fatalf("got %q, %v, progress %d/%d", got, err, last, total)
	}
	if _, err := FetchBytes(context.Background(), srv.Client(), srv.URL, nil, 0, func(int64, int64) {}); err == nil {
		t.Fatal("a refused request is an error")
	}
}

func TestFetchBytesRefusesAFileLargerThanTheMemoryItMayTake(t *testing.T) {
	payload := strings.Repeat("x", 4096)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = io.WriteString(w, payload)
	}))
	defer srv.Close()
	_, err := FetchBytes(context.Background(), srv.Client(), srv.URL, nil, 1024, func(int64, int64) {})
	var tooBig *TooLargeError
	if !errors.As(err, &tooBig) || tooBig.Size != 4096 || tooBig.Max != 1024 {
		t.Fatalf("err = %v", err)
	}
	// Within the limit it reads as before.
	got, err := FetchBytes(context.Background(), srv.Client(), srv.URL, nil, 4096, func(int64, int64) {})
	if err != nil || len(got) != 4096 {
		t.Fatalf("got %d bytes, %v", len(got), err)
	}
}

func TestFetchBytesStopsAFileThatGrowsPastTheLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// no Content-Length: the size is not known until it has come
		f := w.(http.Flusher)
		for i := 0; i < 8; i++ {
			_, _ = io.WriteString(w, strings.Repeat("y", 512))
			f.Flush()
		}
	}))
	defer srv.Close()
	_, err := FetchBytes(context.Background(), srv.Client(), srv.URL, nil, 1024, func(int64, int64) {})
	var tooBig *TooLargeError
	if !errors.As(err, &tooBig) {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchToAndBytesTakeNoClientAndNoProgressCallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, body)
	}))
	defer srv.Close()
	var sb strings.Builder
	if err := FetchTo(context.Background(), nil, srv.URL, http.Header{"x-note": {"a", "b"}}, &sb, nil); err != nil || sb.String() != body {
		t.Fatalf("FetchTo: %q, %v", sb.String(), err)
	}
	got, err := FetchBytes(context.Background(), srv.Client(), srv.URL, nil, 0, nil)
	if err != nil || string(got) != body {
		t.Fatalf("FetchBytes: %q, %v", got, err)
	}
}

func TestFetchWithPartIsolatesConcurrentSameNameFiles(t *testing.T) {
	p1Body := strings.Repeat("A", 1024)
	p2Body := strings.Repeat("B", 1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/1":
			_, _ = io.WriteString(w, p1Body)
		case "/2":
			_, _ = io.WriteString(w, p2Body)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	errCh := make(chan error, 2)
	pathCh := make(chan string, 2)
	for _, tc := range []struct {
		urlPath, id string
	}{{"/1", "tr-1"}, {"/2", "tr-2"}} {
		go func(u, id string) {
			p, err := FetchWithPart(context.Background(), srv.Client(), srv.URL+u, nil, dir, "same.bin", id, nil)
			errCh <- err
			pathCh <- p
		}(tc.urlPath, tc.id)
	}
	for i := 0; i < 2; i++ {
		if err := <-errCh; err != nil {
			t.Fatal(err)
		}
	}
	got1, _ := os.ReadFile(<-pathCh)
	got2, _ := os.ReadFile(<-pathCh)
	s1, s2 := string(got1), string(got2)
	if !((s1 == p1Body && s2 == p2Body) || (s1 == p2Body && s2 == p1Body)) {
		t.Fatalf("concurrent same-name files corrupted: len1=%d len2=%d", len(s1), len(s2))
	}
}

func TestFetch416WithShrunkRemoteRestartsCleanly(t *testing.T) {
	const remote = "short"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			w.Header().Set("Content-Range", "bytes */5")
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(remote)))
		_, _ = io.WriteString(w, remote)
	}))
	defer srv.Close()
	dir := t.TempDir()
	// Oversized stale .part (larger than remote's 5 bytes) must not be accepted as complete.
	_ = os.WriteFile(filepath.Join(dir, "tr-1.part"), []byte("oversized-stale-part"), 0o644)
	p, err := FetchWithPart(context.Background(), srv.Client(), srv.URL, nil, dir, "a.txt", "tr-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != remote {
		t.Fatalf("got %q, want %q", got, remote)
	}
}

func TestFetchRestartsOnContentRangeStartMismatch(t *testing.T) {
	const remote = "fresh-content"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			w.Header().Set("Content-Range", "bytes 0-5/13")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = io.WriteString(w, "fresh-")
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(remote)))
		_, _ = io.WriteString(w, remote)
	}))
	defer srv.Close()
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "tr-2.part"), []byte("1234"), 0o644)
	p, err := FetchWithPart(context.Background(), srv.Client(), srv.URL, nil, dir, "b.txt", "tr-2", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != remote {
		t.Fatalf("got %q, want %q", got, remote)
	}
}
