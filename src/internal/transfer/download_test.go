package transfer

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

func TestFetchResumableRetriesAndResumesAfterDrop(t *testing.T) {
	payload := bytes.Repeat([]byte("abcdefgh"), 512) // 4096 bytes
	var resolves atomic.Int32
	var sawRange atomic.Value
	sawRange.Store("")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := resolves.Load()
		if attempt == 1 && r.URL.Path == "/dl-1" {
			w.Header().Set("ETag", `"v1"`)
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = w.Write(payload[:1500])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			// Returning early after 1500 bytes when Content-Length is 4096 causes unexpected EOF / short read.
			return
		}
		rng := r.Header.Get("Range")
		sawRange.Store(rng)
		if r.URL.Path != "/dl-2" || rng != "bytes=1500-" {
			http.Error(w, "bad resume request", http.StatusBadRequest)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Range", "bytes 1500-4095/4096")
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)-1500))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[1500:])
	}))
	defer srv.Close()

	dir := t.TempDir()
	resolve := func(ctx context.Context) (Target, error) {
		n := resolves.Add(1)
		h := http.Header{"User-Agent": {"test-agent"}}
		return Target{URL: srv.URL + "/dl-" + strconv.Itoa(int(n)), Header: h}, nil
	}
	p, err := FetchResumable(context.Background(), srv.Client(), resolve, dir, "file.bin", "t1", RetryPolicy{
		MaxRetries:   2,
		InitialDelay: 5 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatalf("FetchResumable failed: %v", err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("downloaded length = %d, want %d", len(got), len(payload))
	}
	if resolves.Load() != 2 {
		t.Fatalf("resolve calls = %d, want 2", resolves.Load())
	}
	if gotRange, _ := sawRange.Load().(string); gotRange != "bytes=1500-" {
		t.Fatalf("Range header = %q, want bytes=1500-", gotRange)
	}
	if _, err := os.Stat(PartPath(dir, "file.bin", "t1")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("part file should be cleaned up, stat err = %v", err)
	}
}

func TestFetchResumableResetsBudgetOnForwardProgress(t *testing.T) {
	payload := bytes.Repeat([]byte("0123456789"), 250) // 2500 bytes
	const chunk = 500
	var hits atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		from := 0
		if rng := r.Header.Get("Range"); strings.HasPrefix(rng, "bytes=") {
			from, _ = strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(rng, "bytes="), "-"))
		}
		end := min(from+chunk, len(payload))
		if from == 0 {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = w.Write(payload[:end])
			return
		}
		w.Header().Set("Content-Range", "bytes "+strconv.Itoa(from)+"-"+strconv.Itoa(len(payload)-1)+"/"+strconv.Itoa(len(payload)))
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)-from))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[from:end])
	}))
	defer srv.Close()

	dir := t.TempDir()
	// MaxRetries is only 1, yet 5 drops occur: each chunk makes forward progress
	// so consecFails resets every time.
	p, err := FetchResumable(context.Background(), srv.Client(), func(context.Context) (Target, error) {
		return Target{URL: srv.URL}, nil
	}, dir, "chunked.bin", "t2", RetryPolicy{
		MaxRetries:   1,
		InitialDelay: 5 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatalf("FetchResumable failed despite forward progress: %v", err)
	}
	got, _ := os.ReadFile(p)
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %d bytes, want %d", len(got), len(payload))
	}
	if hits.Load() != 5 {
		t.Fatalf("hits = %d, want 5", hits.Load())
	}
}

func TestFetchResumableStopsAfterMaxRetriesWithoutProgress(t *testing.T) {
	payload := bytes.Repeat([]byte("Z"), 1000)
	var hits atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n == 1 {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = w.Write(payload[:100])
			return
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	dir := t.TempDir()
	_, err := FetchResumable(context.Background(), srv.Client(), func(context.Context) (Target, error) {
		return Target{URL: srv.URL}, nil
	}, dir, "stuck.bin", "t3", RetryPolicy{
		MaxRetries:   2,
		InitialDelay: 5 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
	}, nil)
	if err == nil {
		t.Fatal("expected failure after exhausting MaxRetries")
	}
	if hits.Load() != 3 {
		t.Fatalf("hits = %d, want 3 (1 initial + 2 retries)", hits.Load())
	}
	st, serr := os.Stat(PartPath(dir, "stuck.bin", "t3"))
	if serr != nil || st.Size() != 100 {
		t.Fatalf("part file size = %v, err = %v; want 100 bytes preserved", st, serr)
	}
}

func TestFetchResumableStallTimeoutReconnectsAndResumes(t *testing.T) {
	payload := bytes.Repeat([]byte("S"), 1000)
	unblock := make(chan struct{})
	var hits atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = w.Write(payload[:500])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			<-unblock
			return
		}
		if r.Header.Get("Range") != "bytes=500-" {
			http.Error(w, "missing range", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Range", "bytes 500-999/1000")
		w.Header().Set("Content-Length", "500")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[500:])
	}))
	t.Cleanup(func() {
		close(unblock)
		srv.Close()
	})

	dir := t.TempDir()
	p, err := FetchResumable(context.Background(), srv.Client(), func(context.Context) (Target, error) {
		return Target{URL: srv.URL}, nil
	}, dir, "stall.bin", "t4", RetryPolicy{
		MaxRetries:   2,
		InitialDelay: 5 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		StallTimeout: 60 * time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatalf("FetchResumable stalled flow failed: %v", err)
	}
	got, _ := os.ReadFile(p)
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %d bytes, want %d", len(got), len(payload))
	}
}

func TestFetchResumableWakeUnblocksBackoffImmediately(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Length", "4")
		_, _ = w.Write([]byte("okay"))
	}))
	defer srv.Close()

	wake := make(chan struct{})
	go func() {
		time.Sleep(30 * time.Millisecond)
		close(wake)
	}()

	start := time.Now()
	dir := t.TempDir()
	p, err := FetchResumable(context.Background(), srv.Client(), func(context.Context) (Target, error) {
		return Target{URL: srv.URL}, nil
	}, dir, "wake.txt", "t5", RetryPolicy{
		MaxRetries:   1,
		InitialDelay: 5 * time.Second,
		MaxDelay:     5 * time.Second,
		WakeCh:       func() <-chan struct{} { return wake },
	}, nil)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("FetchResumable wake failed: %v", err)
	}
	if elapsed > time.Second {
		t.Fatalf("backoff wait took %v, wake channel should have unblocked immediately", elapsed)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "okay" {
		t.Fatalf("got %q, want okay", got)
	}
}

func TestFetchResumableDoesNotRetryPermanentOr4xxOrLocalErrors(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.WriteHeader(code)
		}))
		_, err := FetchResumable(context.Background(), srv.Client(), func(context.Context) (Target, error) {
			return Target{URL: srv.URL}, nil
		}, t.TempDir(), "nope.txt", "p1", RetryPolicy{MaxRetries: 3, InitialDelay: time.Millisecond}, nil)
		srv.Close()
		if err == nil || hits.Load() != 1 {
			t.Fatalf("status %d: hits=%d err=%v; want 1 hit and error", code, hits.Load(), err)
		}
	}

	var permHits atomic.Int32
	_, err := FetchResumable(context.Background(), nil, func(context.Context) (Target, error) {
		permHits.Add(1)
		return Target{}, Permanent(errors.New("connection reset by peer"))
	}, t.TempDir(), "perm.txt", "p2", RetryPolicy{MaxRetries: 3, InitialDelay: time.Millisecond}, nil)
	if err == nil || permHits.Load() != 1 {
		t.Fatalf("Permanent error: hits=%d err=%v; want 1 hit", permHits.Load(), err)
	}

	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		roDir := t.TempDir()
		if err := os.Chmod(roDir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(roDir, 0o700) })
		var roHits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			roHits.Add(1)
			w.Header().Set("Content-Length", "3")
			_, _ = w.Write([]byte("abc"))
		}))
		defer srv.Close()
		_, err := FetchResumable(context.Background(), srv.Client(), func(context.Context) (Target, error) {
			return Target{URL: srv.URL}, nil
		}, roDir, "ro.txt", "p3", RetryPolicy{MaxRetries: 3, InitialDelay: time.Millisecond}, nil)
		if !errors.Is(err, os.ErrPermission) || roHits.Load() != 1 {
			t.Fatalf("read-only dir: hits=%d err=%v; want 1 hit and ErrPermission", roHits.Load(), err)
		}
	}
}

func TestFetchResumableRetries503And429(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch hits.Add(1) {
		case 1:
			w.WriteHeader(http.StatusServiceUnavailable)
		case 2:
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			w.Header().Set("Content-Length", "7")
			_, _ = w.Write([]byte("success"))
		}
	}))
	defer srv.Close()

	p, err := FetchResumable(context.Background(), srv.Client(), func(context.Context) (Target, error) {
		return Target{URL: srv.URL}, nil
	}, t.TempDir(), "retry.txt", "r1", RetryPolicy{
		MaxRetries:   3,
		InitialDelay: 5 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatalf("FetchResumable failed: %v", err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "success" {
		t.Fatalf("got %q, want success", got)
	}
	if hits.Load() != 3 {
		t.Fatalf("hits = %d, want 3", hits.Load())
	}
}

func TestFetchResumableRestartsWhenETagChanges(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch hits.Add(1) {
		case 1:
			w.Header().Set("ETag", `"v1"`)
			w.Header().Set("Content-Length", "10")
			_, _ = w.Write([]byte("old1"))
		case 2:
			// Origin ignores If-Range and sends 206 with new ETag.
			w.Header().Set("ETag", `"v2"`)
			w.Header().Set("Content-Range", "bytes 4-9/10")
			w.Header().Set("Content-Length", "6")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("ontent"))
		default:
			w.Header().Set("ETag", `"v2"`)
			w.Header().Set("Content-Length", "10")
			_, _ = w.Write([]byte("newcontent"))
		}
	}))
	defer srv.Close()

	p, err := FetchResumable(context.Background(), srv.Client(), func(context.Context) (Target, error) {
		return Target{URL: srv.URL}, nil
	}, t.TempDir(), "etag.txt", "e1", RetryPolicy{
		MaxRetries:   2,
		InitialDelay: 5 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatalf("FetchResumable ETag change failed: %v", err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "newcontent" {
		t.Fatalf("got %q, want newcontent", got)
	}
}

func TestFetchResumablePersistsValidatorAcrossRestartAndDetectsChange(t *testing.T) {
	v1Payload := bytes.Repeat([]byte("12345"), 300) // 1500 bytes
	v2Payload := bytes.Repeat([]byte("ABCDE"), 360) // 1800 bytes

	// Run 1: cancel mid-download after receiving partial bytes with ETag "v1".
	dir := t.TempDir()
	ctx1, cancel1 := context.WithCancel(context.Background())
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(v1Payload)))
		_, _ = w.Write(v1Payload[:500])
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer srv1.Close()

	var mu sync.Mutex
	var vals1 []string
	var sawV1BeforeCancel bool
	_, err := FetchResumable(ctx1, srv1.Client(), func(context.Context) (Target, error) {
		return Target{URL: srv1.URL}, nil
	}, dir, "file.bin", "tv1", RetryPolicy{
		MaxRetries:   1,
		InitialDelay: 5 * time.Millisecond,
		OnValidator: func(v string) {
			mu.Lock()
			vals1 = append(vals1, v)
			mu.Unlock()
		},
	}, func(done, _ int64) {
		if done >= 500 {
			mu.Lock()
			sawV1BeforeCancel = len(vals1) > 0 && vals1[len(vals1)-1] == `"v1"`
			mu.Unlock()
			cancel1()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run 1 err = %v, want context.Canceled", err)
	}
	if !sawV1BeforeCancel {
		t.Fatalf("OnValidator did not receive \"v1\" before cancel; got %v", vals1)
	}
	partPath := PartPath(dir, "file.bin", "tv1")
	st, err := os.Stat(partPath)
	if err != nil || st.Size() < 500 {
		t.Fatalf("part size after cancel = %v, err = %v", st, err)
	}
	partCopy, err := os.ReadFile(partPath)
	if err != nil {
		t.Fatal(err)
	}

	// Run 2a: RFC-compliant server sees stale If-Range "v1" for changed file "v2" and returns 200 OK.
	dir2a := t.TempDir()
	if err := os.WriteFile(PartPath(dir2a, "file.bin", "tv2a"), partCopy, 0o644); err != nil {
		t.Fatal(err)
	}
	var gotRange2a, gotIfRange2a string
	srv2a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRange2a = r.Header.Get("Range")
		gotIfRange2a = r.Header.Get("If-Range")
		w.Header().Set("ETag", `"v2"`)
		if gotIfRange2a == `"v2"` && gotRange2a != "" {
			w.WriteHeader(http.StatusPartialContent)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(v2Payload)))
		_, _ = w.Write(v2Payload)
	}))
	defer srv2a.Close()

	var vals2a []string
	p2a, err := FetchResumable(context.Background(), srv2a.Client(), func(context.Context) (Target, error) {
		return Target{URL: srv2a.URL}, nil
	}, dir2a, "file.bin", "tv2a", RetryPolicy{
		MaxRetries: 1,
		Validator:  `"v1"`,
		OnValidator: func(v string) {
			vals2a = append(vals2a, v)
		},
	}, nil)
	if err != nil {
		t.Fatalf("run 2a failed: %v", err)
	}
	if gotRange2a == "" || gotIfRange2a != `"v1"` {
		t.Fatalf("run 2a headers: Range=%q If-Range=%q", gotRange2a, gotIfRange2a)
	}
	if len(vals2a) == 0 || vals2a[len(vals2a)-1] != `"v2"` {
		t.Fatalf("run 2a validators = %v, want last \"v2\"", vals2a)
	}
	if b, _ := os.ReadFile(p2a); !bytes.Equal(b, v2Payload) {
		t.Fatalf("run 2a body mismatch: len=%d want %d", len(b), len(v2Payload))
	}

	// Run 2b: server ignores If-Range and sends 206 with new ETag "v2".
	dir2b := t.TempDir()
	if err := os.WriteFile(PartPath(dir2b, "file.bin", "tv2b"), partCopy, 0o644); err != nil {
		t.Fatal(err)
	}
	var hits2b atomic.Int32
	srv2b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hits2b.Add(1) == 1 {
			w.Header().Set("ETag", `"v2"`)
			w.Header().Set("Content-Range", "bytes "+strconv.Itoa(len(partCopy))+"-"+strconv.Itoa(len(v2Payload)-1)+"/"+strconv.Itoa(len(v2Payload)))
			w.Header().Set("Content-Length", strconv.Itoa(len(v2Payload)-len(partCopy)))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(v2Payload[len(partCopy):])
			return
		}
		w.Header().Set("ETag", `"v2"`)
		w.Header().Set("Content-Length", strconv.Itoa(len(v2Payload)))
		_, _ = w.Write(v2Payload)
	}))
	defer srv2b.Close()

	var vals2b []string
	p2b, err := FetchResumable(context.Background(), srv2b.Client(), func(context.Context) (Target, error) {
		return Target{URL: srv2b.URL}, nil
	}, dir2b, "file.bin", "tv2b", RetryPolicy{
		MaxRetries: 1,
		Validator:  `"v1"`,
		OnValidator: func(v string) {
			vals2b = append(vals2b, v)
		},
	}, nil)
	if err != nil {
		t.Fatalf("run 2b failed: %v", err)
	}
	if b, _ := os.ReadFile(p2b); !bytes.Equal(b, v2Payload) {
		t.Fatalf("run 2b body mismatch: len=%d want %d", len(b), len(v2Payload))
	}
	if len(vals2b) < 2 || vals2b[0] != "" || vals2b[len(vals2b)-1] != `"v2"` {
		t.Fatalf("run 2b validators = %v, want [\"\" \"v2\"]", vals2b)
	}

	// Run 2c: server file unchanged ("v1"), resumes cleanly via 206 with If-Range "v1".
	var gotRange2c, gotIfRange2c string
	srv2c := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRange2c = r.Header.Get("Range")
		gotIfRange2c = r.Header.Get("If-Range")
		if gotIfRange2c != `"v1"` || gotRange2c != "bytes="+strconv.Itoa(int(st.Size()))+"-" {
			http.Error(w, "unexpected resume headers", http.StatusBadRequest)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Range", "bytes "+strconv.Itoa(int(st.Size()))+"-"+strconv.Itoa(len(v1Payload)-1)+"/"+strconv.Itoa(len(v1Payload)))
		w.Header().Set("Content-Length", strconv.Itoa(len(v1Payload)-int(st.Size())))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(v1Payload[st.Size():])
	}))
	defer srv2c.Close()

	var vals2c []string
	p2c, err := FetchResumable(context.Background(), srv2c.Client(), func(context.Context) (Target, error) {
		return Target{URL: srv2c.URL}, nil
	}, dir, "file.bin", "tv1", RetryPolicy{
		MaxRetries: 1,
		Validator:  `"v1"`,
		OnValidator: func(v string) {
			vals2c = append(vals2c, v)
		},
	}, nil)
	if err != nil {
		t.Fatalf("run 2c failed: %v", err)
	}
	if len(vals2c) != 0 {
		t.Fatalf("run 2c should not change validator, got %v", vals2c)
	}
	if b, _ := os.ReadFile(p2c); !bytes.Equal(b, v1Payload) {
		t.Fatalf("run 2c body mismatch: len=%d want %d", len(b), len(v1Payload))
	}
}

func TestFetchResumableClearsValidatorWhenServer200HasNoneAndProgressResets(t *testing.T) {
	newPayload := bytes.Repeat([]byte("N"), 1000)
	dir := t.TempDir()
	// Pre-populate an 800-byte .part from an earlier file with ETag "v1".
	if err := os.WriteFile(PartPath(dir, "reset.bin", "trst"), bytes.Repeat([]byte("O"), 800), 0o644); err != nil {
		t.Fatal(err)
	}

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch hits.Add(1) {
		case 1:
			if r.Header.Get("Range") != "bytes=800-" || r.Header.Get("If-Range") != `"v1"` {
				http.Error(w, "bad first request", http.StatusBadRequest)
				return
			}
			// Server replaces file without ETag/Last-Modified and drops after 300 bytes.
			w.Header().Set("Content-Length", strconv.Itoa(len(newPayload)))
			_, _ = w.Write(newPayload[:300])
		case 2:
			if r.Header.Get("Range") != "bytes=300-" || r.Header.Get("If-Range") != "" {
				http.Error(w, "bad second request", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Range", "bytes 300-999/1000")
			w.Header().Set("Content-Length", "700")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(newPayload[300:500]) // short read to test second progress reset with MaxRetries: 1
		default:
			if r.Header.Get("Range") != "bytes=500-" || r.Header.Get("If-Range") != "" {
				http.Error(w, "bad third request", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Range", "bytes 500-999/1000")
			w.Header().Set("Content-Length", "500")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(newPayload[500:])
		}
	}))
	defer srv.Close()

	var vals []string
	p, err := FetchResumable(context.Background(), srv.Client(), func(context.Context) (Target, error) {
		return Target{URL: srv.URL}, nil
	}, dir, "reset.bin", "trst", RetryPolicy{
		MaxRetries:   1,
		InitialDelay: 5 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Validator:    `"v1"`,
		OnValidator: func(v string) {
			vals = append(vals, v)
		},
	}, nil)
	if err != nil {
		t.Fatalf("FetchResumable failed: %v", err)
	}
	if b, _ := os.ReadFile(p); !bytes.Equal(b, newPayload) {
		t.Fatalf("got %d bytes, want %d", len(b), len(newPayload))
	}
	if len(vals) != 1 || vals[0] != "" {
		t.Fatalf("OnValidator calls = %v, want [\"\"]", vals)
	}
}

func TestFetchResumableClearsStaleValidatorWhenPartMissing(t *testing.T) {
	var sawRange, sawIfRange string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRange = r.Header.Get("Range")
		sawIfRange = r.Header.Get("If-Range")
		w.Header().Set("Content-Length", "4")
		_, _ = w.Write([]byte("done"))
	}))
	defer srv.Close()

	var vals []string
	p, err := FetchResumable(context.Background(), srv.Client(), func(context.Context) (Target, error) {
		return Target{URL: srv.URL}, nil
	}, t.TempDir(), "missing.txt", "tmiss", RetryPolicy{
		MaxRetries: 1,
		Validator:  `"v1"`,
		OnValidator: func(v string) {
			vals = append(vals, v)
		},
	}, nil)
	if err != nil {
		t.Fatalf("FetchResumable failed: %v", err)
	}
	if sawRange != "" || sawIfRange != "" {
		t.Fatalf("headers should be empty, got Range=%q If-Range=%q", sawRange, sawIfRange)
	}
	if len(vals) != 1 || vals[0] != "" {
		t.Fatalf("OnValidator calls = %v, want [\"\"]", vals)
	}
	if b, _ := os.ReadFile(p); string(b) != "done" {
		t.Fatalf("got %q, want done", b)
	}
}
