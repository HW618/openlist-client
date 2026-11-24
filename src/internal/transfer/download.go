package transfer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// PartSuffix ends the name of a download that is not finished.
const PartSuffix = ".part"

// Fetch fetches url into dir under name and returns the path it saved to.
//
// The bytes land in name+".part" first, so an interrupted download is not
// taken for a finished file. When that file already exists the request asks
// the server for the rest with a Range header, and starts over if the server
// answers with the whole file. report hears how many bytes are on disk and
// how many the file has, 0 when unknown.
func Fetch(ctx context.Context, hc *http.Client, url string, header http.Header, dir, name string, report func(done, total int64)) (string, error) {
	return FetchWithPart(ctx, hc, url, header, dir, name, "", report)
}

// FetchWithPart is Fetch with a caller-chosen base name for the temporary .part
// file (without PartSuffix), so concurrent or distinct tasks sharing a display
// name do not collide on disk while still resuming across retries.
func FetchWithPart(ctx context.Context, hc *http.Client, url string, header http.Header, dir, name, partName string, report func(done, total int64)) (string, error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	name = SafeName(name)
	partBase := name
	if partName != "" {
		partBase = SafeName(partName)
	}
	part := filepath.Join(dir, partBase+PartSuffix)

	for attempt := 0; attempt < 2; attempt++ {
		var have int64
		if st, err := os.Stat(part); err == nil {
			have = st.Size()
		}
		dest, retry, err := fetchOnce(ctx, hc, url, header, dir, name, part, have, report)
		if !retry {
			return dest, err
		}
		_ = os.Remove(part)
	}
	return "", errors.New("download: range mismatch persisted")
}

func fetchOnce(ctx context.Context, hc *http.Client, url string, header http.Header, dir, name, part string, have int64, report func(done, total int64)) (dest string, retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", false, err
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	if have > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(have, 10)+"-")
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()

	flags := os.O_CREATE | os.O_WRONLY
	var total int64
	switch {
	case resp.StatusCode == http.StatusPartialContent && have > 0:
		if start, crTotal, ok := parseContentRange(resp.Header.Get("Content-Range")); ok {
			if start != have {
				return "", true, nil
			}
			if crTotal > 0 {
				total = crTotal
			}
		}
		if total == 0 {
			if resp.ContentLength >= 0 {
				total = have + resp.ContentLength
			}
		}
		flags |= os.O_APPEND
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && have > 0:
		if crTotal, ok := parseUnsatisfiableRange(resp.Header.Get("Content-Range")); ok && have == crTotal {
			p, ferr := finish(part, dir, name)
			return p, false, ferr
		}
		return "", true, nil
	case resp.StatusCode == http.StatusOK:
		flags |= os.O_TRUNC
		have = 0
		if resp.ContentLength > 0 {
			total = resp.ContentLength
		}
	default:
		return "", false, fmt.Errorf("download: the server answered %s", resp.Status)
	}

	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return "", false, err
	}
	done := have
	if report != nil {
		report(done, total)
	}
	buf := make([]byte, 64<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return "", false, werr
			}
			done += int64(n)
			if report != nil {
				report(done, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return "", false, rerr
		}
	}
	if err := f.Close(); err != nil {
		return "", false, err
	}
	if total > 0 && done < total {
		return "", false, errors.New("download: the connection closed before the file ended")
	}
	p, ferr := finish(part, dir, name)
	return p, false, ferr
}

// parseContentRange parses "bytes <start>-<end>/<total|*>".
func parseContentRange(v string) (start, total int64, ok bool) {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(strings.ToLower(v), "bytes ") {
		return 0, 0, false
	}
	rest := strings.TrimSpace(v[len("bytes "):])
	rng, tot, hasSlash := strings.Cut(rest, "/")
	if !hasSlash {
		return 0, 0, false
	}
	sStr, _, hasDash := strings.Cut(rng, "-")
	if !hasDash {
		return 0, 0, false
	}
	s, err := strconv.ParseInt(strings.TrimSpace(sStr), 10, 64)
	if err != nil || s < 0 {
		return 0, 0, false
	}
	tot = strings.TrimSpace(tot)
	if tot != "*" {
		t, err := strconv.ParseInt(tot, 10, 64)
		if err != nil || t < 0 {
			return 0, 0, false
		}
		return s, t, true
	}
	return s, 0, true
}

// parseUnsatisfiableRange parses "bytes */<total>" from a 416 response.
func parseUnsatisfiableRange(v string) (total int64, ok bool) {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(strings.ToLower(v), "bytes ") {
		return 0, false
	}
	rest := strings.TrimSpace(v[len("bytes "):])
	rng, tot, hasSlash := strings.Cut(rest, "/")
	if !hasSlash || strings.TrimSpace(rng) != "*" {
		return 0, false
	}
	t, err := strconv.ParseInt(strings.TrimSpace(tot), 10, 64)
	if err != nil || t < 0 {
		return 0, false
	}
	return t, true
}

var finishMu sync.Mutex

// finish moves a completed part file to its final name, which is made unique
// when a file of that name exists.
func finish(part, dir, name string) (string, error) {
	finishMu.Lock()
	defer finishMu.Unlock()
	dest := UniquePath(dir, name)
	if err := os.Rename(part, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// UniquePath is dir/name, or dir/name with " (1)", " (2)" and so on before
// its extension when a file of that name exists.
func UniquePath(dir, name string) string {
	p := filepath.Join(dir, name)
	if _, err := os.Stat(p); err != nil {
		return p
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		p = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, i, ext))
		if _, err := os.Stat(p); err != nil {
			return p
		}
	}
}

// SafeName is name made fit to be a file name on any system: without path
// separators, control characters, Windows reserved names, or characters Windows refuses.
func SafeName(name string) string {
	r := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	name = r.Replace(name)
	var b strings.Builder
	b.Grow(len(name))
	for _, ch := range name {
		if ch < 0x20 || ch == 0x7f {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(ch)
	}
	name = strings.TrimSpace(b.String())
	name = strings.TrimRight(name, ". ")
	if name == "" || name == "." || name == ".." {
		return "download"
	}
	stem, _, _ := strings.Cut(name, ".")
	switch strings.ToUpper(stem) {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return "_" + name
	}
	return name
}

// ProgressReader reports how many bytes of r are read.
type ProgressReader struct {
	R      io.Reader
	Report func(done int64)
	done   int64
}

// Read implements io.Reader.
func (p *ProgressReader) Read(b []byte) (int, error) {
	n, err := p.R.Read(b)
	if n > 0 {
		p.done += int64(n)
		if p.Report != nil {
			p.Report(p.done)
		}
	}
	return n, err
}

// TooLargeError is a file that is more than the memory it was to be read
// into may take.
type TooLargeError struct {
	// Size is how large the file is, or how much of it had come, and Max the
	// most that may.
	Size, Max int64
}

func (e *TooLargeError) Error() string {
	return fmt.Sprintf("download: the file is larger than the %d bytes that may be held in memory", e.Max)
}

// FetchBytes reads the file at link into memory, which holds at most max bytes
// of it (no limit when max is 0): a file that says it is larger is not asked
// for, and one that grows past it stops the read, with a *TooLargeError. See
// FetchTo.
func FetchBytes(ctx context.Context, hc *http.Client, link string, header http.Header, max int64, report func(done, total int64)) ([]byte, error) {
	if report == nil {
		report = func(int64, int64) {}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var big *TooLargeError
	w := &limitWriter{max: max}
	err := FetchTo(ctx, hc, link, header, w, func(done, total int64) {
		if max > 0 && total > max && big == nil {
			big = &TooLargeError{Size: total, Max: max}
			cancel()
		}
		report(done, total)
	})
	if big == nil && w.over {
		big = &TooLargeError{Size: w.n, Max: max}
	}
	if big != nil {
		return nil, big
	}
	if err != nil {
		return nil, err
	}
	return w.buf.Bytes(), nil
}

// limitWriter collects what is written to it, up to max bytes (all of it when
// max is 0), and fails the write that goes past.
type limitWriter struct {
	buf  bytes.Buffer
	max  int64
	n    int64
	over bool
}

func (w *limitWriter) Write(p []byte) (int, error) {
	if w.max > 0 && w.n+int64(len(p)) > w.max {
		w.over = true
		w.n += int64(len(p))
		return 0, errors.New("download: too large")
	}
	w.n += int64(len(p))
	return w.buf.Write(p)
}

// FetchTo copies the file at link to w, for places that have no disk path to
// write to. report hears how much has come and how much is to come, -1 when
// the server does not say.
func FetchTo(ctx context.Context, hc *http.Client, link string, header http.Header, w io.Writer, report func(done, total int64)) error {
	if hc == nil {
		hc = http.DefaultClient
	}
	if report == nil {
		report = func(int64, int64) {}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return err
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: the server answered %s", resp.Status)
	}
	total := resp.ContentLength
	report(0, total)
	pr := &ProgressReader{R: resp.Body, Report: func(done int64) { report(done, total) }}
	_, err = io.Copy(w, pr)
	return err
}
