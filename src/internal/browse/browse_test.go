package browse

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HW618/openlist-client/src/internal/openlist"
	"github.com/HW618/openlist-client/src/internal/store"
)

func names(items []Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Name
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSortPutsFoldersFirstAndNamesNaturally(t *testing.T) {
	items := []Item{{Name: "file10"}, {Name: "b", IsDir: true}, {Name: "file2"}, {Name: "A", IsDir: true}}
	got := names(Sort(items, store.SortName, false))
	want := []string{"A", "b", "file2", "file10"}
	if !eq(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSortDescKeepsFoldersFirst(t *testing.T) {
	items := []Item{{Name: "a"}, {Name: "d", IsDir: true}, {Name: "c"}, {Name: "b", IsDir: true}}
	got := names(Sort(items, store.SortName, true))
	want := []string{"d", "b", "c", "a"}
	if !eq(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSortBySizeAndDate(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	items := []Item{{Name: "big", Size: 9, Modified: t0}, {Name: "small", Size: 1, Modified: t0.Add(time.Hour)}}
	if got := names(Sort(items, store.SortSize, false)); !eq(got, []string{"small", "big"}) {
		t.Fatalf("size: %v", got)
	}
	if got := names(Sort(items, store.SortDate, true)); !eq(got, []string{"small", "big"}) {
		t.Fatalf("date desc: %v", got)
	}
}

func TestFilter(t *testing.T) {
	items := []Item{{Name: "Report.PDF"}, {Name: "notes.txt"}}
	if got := Filter(items, " report"); len(got) != 1 || got[0].Name != "Report.PDF" {
		t.Fatalf("got %v", got)
	}
	if got := Filter(items, ""); len(got) != 2 {
		t.Fatal("empty query must keep all")
	}
}

func TestSize(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KB", 1536: "1.5 KB", 5 << 20: "5.0 MB", 250 << 20: "250 MB", 3 << 30: "3.0 GB"} {
		if got := Size(n); got != want {
			t.Errorf("Size(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestDateAndLatency(t *testing.T) {
	if Date(time.Time{}) != "-" {
		t.Fatal("zero time")
	}
	if got := Latency(42 * time.Millisecond); got != "42 ms" {
		t.Fatal(got)
	}
	if got := Latency(1500 * time.Millisecond); got != "1.5 s" {
		t.Fatal(got)
	}
}

func TestIsMountAndCrumbs(t *testing.T) {
	if !IsMount(Item{Path: "/Aliyun"}) || IsMount(Item{Path: "/Aliyun/docs"}) {
		t.Fatal("mount detection")
	}
	c := Crumbs("/a/b")
	if len(c) != 3 || c[0].Path != "/" || c[2].Path != "/a/b" || c[2].Name != "b" {
		t.Fatalf("crumbs %v", c)
	}
	if got := Crumbs("/"); len(got) != 1 {
		t.Fatalf("root crumbs %v", got)
	}
}

func TestPreviewable(t *testing.T) {
	for name, want := range map[string]string{"a.JPG": "image", "a.mp4": "video", "a.mp3": "audio", "a.md": "text", "a.zip": "", "noext": ""} {
		if got := Previewable(name); got != want {
			t.Errorf("Previewable(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestItemFrom(t *testing.T) {
	it := ItemFrom("/a", openlist.Entry{Name: "x.mp4", Size: 5})
	if it.Path != "/a/x.mp4" || it.Kind != openlist.KindVideo {
		t.Fatalf("%+v", it)
	}
}

// ---- floating button ----

func TestClampKeepsButtonInside(t *testing.T) {
	p := Clamp(Pos{X: -50, Y: 5}, 400, 800)
	if p.X != 8 || p.Y != 58 {
		t.Fatalf("%+v", p)
	}
	p = Clamp(Pos{X: 999, Y: 999}, 400, 800)
	if p.X != 400-FabSize-8 || p.Y != 800-FabSize-64 {
		t.Fatalf("%+v", p)
	}
}

func TestReleaseDocksOnlyNearAnEdge(t *testing.T) {
	const w = 400
	if p, s := Release(Pos{X: 20, Y: 300}, w); s != SideLeft || p.X != DockInset {
		t.Fatalf("left: %+v %v", p, s)
	}
	if p, s := Release(Pos{X: w - FabSize - 10, Y: 300}, w); s != SideRight || p.X != w-FabSize-DockInset {
		t.Fatalf("right: %+v %v", p, s)
	}
	if p, s := Release(Pos{X: 150, Y: 300}, w); s != SideNone || p.X != 150 {
		t.Fatalf("free: %+v %v", p, s)
	}
}

func TestDockedLeavesAFifthInView(t *testing.T) {
	const w = 400
	p, side := Release(Pos{X: w - FabSize - 4, Y: 300}, w)
	off := Docked(p, side, w)
	visibleLeft := p.X + off
	// The button's left edge sits so that exactly a fifth of it is before the
	// container's right edge.
	if got := w - visibleLeft; got < FabSize*0.19 || got > FabSize*0.21 {
		t.Fatalf("visible %.2f of %.2f", got, FabSize)
	}
	p, side = Release(Pos{X: 3, Y: 300}, w)
	off = Docked(p, side, w)
	if got := p.X + off + FabSize; got < FabSize*0.19 || got > FabSize*0.21 {
		t.Fatalf("left visible %.2f", got)
	}
	if Docked(Pos{X: 150}, SideNone, w) != 0 {
		t.Fatal("a free button is not offset")
	}
}

func TestMenuOpensUpWithOneColumnWhenThereIsRoom(t *testing.T) {
	m := LayoutMenu(Pos{X: 330, Y: 600}, 400, 800, 6, 150)
	if m.Below || m.Cols != 1 || m.Rows != 6 {
		t.Fatalf("%+v", m)
	}
	if m.Left+m.Width > 400-8+0.01 || m.Left < 8 {
		t.Fatalf("menu leaves the screen: %+v", m)
	}
	if m.Top+m.Height > 600 {
		t.Fatalf("menu overlaps the button: %+v", m)
	}
}

func TestMenuUsesColumnsWhenShort(t *testing.T) {
	m := LayoutMenu(Pos{X: 330, Y: 200}, 400, 380, 6, 150)
	if m.Cols < 2 {
		t.Fatalf("expected more columns: %+v", m)
	}
	if m.Left < 8 || m.Left+m.Width > 392.01 {
		t.Fatalf("menu leaves the screen: %+v", m)
	}
}

func TestMenuOpensBelowAtTheTop(t *testing.T) {
	m := LayoutMenu(Pos{X: 10, Y: 60}, 400, 800, 6, 150)
	if !m.Below {
		t.Fatalf("expected below: %+v", m)
	}
	if m.Top != 60+FabSize+10 {
		t.Fatalf("top %v", m.Top)
	}
	if m.Left != 10 {
		t.Fatalf("a left button's menu aligns left: %v", m.Left)
	}
}

func TestItemWidthCountsWideCharacters(t *testing.T) {
	if ItemWidth("离线下载") <= ItemWidth("abcd") {
		t.Fatal("wide characters must count more")
	}
}

type fakeLister map[string][]openlist.Entry

func (f fakeLister) list(_ context.Context, dir string) ([]Item, error) {
	es, ok := f[dir]
	if !ok {
		return nil, errors.New("no such folder")
	}
	items := make([]Item, len(es))
	for i, e := range es {
		items[i] = ItemFrom(dir, e)
	}
	return items, nil
}

func TestCrawlFindsBelowTheFolder(t *testing.T) {
	l := fakeLister{
		"/":       {{Name: "A", IsDir: true}, {Name: "b.txt"}},
		"/A":      {{Name: "Deep", IsDir: true}, {Name: "report one.pdf"}},
		"/A/Deep": {{Name: "Report two.PDF"}, {Name: "other.txt"}},
	}
	got, err := Crawl(context.Background(), l.list, "/", "report pdf", DefaultCrawl, nil)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, it := range got {
		paths = append(paths, it.Path)
	}
	slices.Sort(paths)
	if !eq(paths, []string{"/A/Deep/Report two.PDF", "/A/report one.pdf"}) {
		t.Fatalf("got %v", paths)
	}
	// below a folder only
	got, _ = Crawl(context.Background(), l.list, "/A/Deep", "report", DefaultCrawl, nil)
	if len(got) != 1 {
		t.Fatalf("got %v", names(got))
	}
}

func TestCrawlLimitsAndErrors(t *testing.T) {
	l := fakeLister{
		"/":    {{Name: "A", IsDir: true}},
		"/A":   {{Name: "B", IsDir: true}, {Name: "x1"}},
		"/A/B": {{Name: "x2"}},
	}
	got, _ := Crawl(context.Background(), l.list, "/", "x", CrawlLimits{Depth: 1, Folders: 10, Hits: 10, Workers: 2}, nil)
	if len(got) != 1 || got[0].Name != "x1" {
		t.Fatalf("depth limit: %v", names(got))
	}
	got, _ = Crawl(context.Background(), l.list, "/", "x", CrawlLimits{Depth: 5, Folders: 10, Hits: 1, Workers: 2}, nil)
	if len(got) != 1 {
		t.Fatalf("hit limit: %v", names(got))
	}
	got, _ = Crawl(context.Background(), l.list, "/", "x", CrawlLimits{Depth: 5, Folders: 2, Hits: 10, Workers: 2}, nil)
	if len(got) != 1 {
		t.Fatalf("folder limit: %v", names(got))
	}
	if _, err := Crawl(context.Background(), l.list, "/missing", "x", DefaultCrawl, nil); err == nil {
		t.Fatal("an unreadable start is an error")
	}
	// a folder that cannot be read below the start is skipped
	l["/A"] = append(l["/A"], openlist.Entry{Name: "locked", IsDir: true})
	got, err := Crawl(context.Background(), l.list, "/", "x", DefaultCrawl, nil)
	if err != nil || len(got) != 2 {
		t.Fatalf("skips unreadable: %v %v", names(got), err)
	}
}

func TestCrawlReportsProgressAndStopsOnCancel(t *testing.T) {
	l := fakeLister{
		"/":  {{Name: "A", IsDir: true}, {Name: "x0"}},
		"/A": {{Name: "x1"}},
	}
	var mu sync.Mutex
	var seen []int
	_, _ = Crawl(context.Background(), l.list, "/", "x", DefaultCrawl, func(h []Item) {
		mu.Lock()
		seen = append(seen, len(h))
		mu.Unlock()
	})
	if len(seen) != 2 || seen[0] != 1 || seen[1] != 2 {
		t.Fatalf("progress %v", seen)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err := Crawl(ctx, l.list, "/", "x", DefaultCrawl, nil)
	if err != nil || len(got) > 2 {
		t.Fatalf("cancelled: %v %v", names(got), err)
	}
}

func TestMerge(t *testing.T) {
	a := []Item{{Name: "a", Path: "/a", Size: 1}}
	b := []Item{{Name: "a", Path: "/a", Size: 2}, {Name: "b", Path: "/b"}}
	got := Merge(a, b)
	if len(got) != 2 || got[0].Size != 1 {
		t.Fatalf("got %+v", got)
	}
}

func TestCrawlProgressDoesNotHoldUpTheWorkersAndNeverGoesBack(t *testing.T) {
	l := fakeLister{"/": {}}
	for i := 0; i < 8; i++ {
		d := "d" + string(rune('a'+i))
		l["/"] = append(l["/"], openlist.Entry{Name: d, IsDir: true})
		l["/"+d] = []openlist.Entry{{Name: "x " + d}}
	}
	var inCallback, overlapped atomic.Bool
	list := func(ctx context.Context, dir string) ([]Item, error) {
		if dir != "/" && inCallback.Load() {
			overlapped.Store(true) // a folder was read while a callback was still busy
		}
		return l.list(ctx, dir)
	}
	last := 0
	got, err := Crawl(context.Background(), list, "/", "x", CrawlLimits{Depth: 2, Folders: 50, Hits: 50, Workers: 4}, func(hits []Item) {
		inCallback.Store(true)
		defer inCallback.Store(false)
		if len(hits) < last {
			t.Errorf("progress went back from %d to %d hits", last, len(hits))
		}
		last = len(hits)
		time.Sleep(30 * time.Millisecond)
	})
	if err != nil || len(got) != 8 {
		t.Fatalf("got %d hits, %v", len(got), err)
	}
	if !overlapped.Load() {
		t.Fatal("a slow progress callback held up the other workers")
	}
}
