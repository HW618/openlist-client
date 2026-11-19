// Package browse holds the pure logic of the file browser: how entries are
// sorted and written, and where the floating button and its menu go.
package browse

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/HW618/openlist-client/src/internal/openlist"
	"github.com/HW618/openlist-client/src/internal/store"
)

// Item is one entry of a folder or of a search, with the path it has on the
// server.
type Item struct {
	Name     string
	Path     string
	IsDir    bool
	Size     int64
	Modified time.Time
	Sign     string
	Thumb    string
	Kind     openlist.Kind
}

// ItemFrom is the item for an entry listed in dir.
func ItemFrom(dir string, e openlist.Entry) Item {
	return Item{Name: e.Name, Path: openlist.Join(dir, e.Name), IsDir: e.IsDir, Size: e.Size,
		Modified: e.Modified, Sign: e.Sign, Thumb: e.Thumb, Kind: e.Kind()}
}

// ItemFromResult is the item for a search hit.
func ItemFromResult(r openlist.SearchResult) Item {
	kind := openlist.KindOfName(r.Name)
	if r.IsDir {
		kind = openlist.KindFolder
	}
	return Item{Name: r.Name, Path: r.Path(), IsDir: r.IsDir, Size: r.Size, Kind: kind}
}

// IsMount reports whether the item is a storage root: one of the entries of
// the server's root folder, which the server does not move or delete.
func IsMount(it Item) bool { return len(openlist.Segments(it.Path)) == 1 }

// Sort orders items with folders first, then by key; desc reverses the order
// inside each of the two groups. The slice is sorted in place and returned.
func Sort(items []Item, key store.SortKey, desc bool) []Item {
	less := func(a, b Item) bool {
		switch key {
		case store.SortSize:
			if a.Size != b.Size {
				return a.Size < b.Size
			}
		case store.SortDate:
			if !a.Modified.Equal(b.Modified) {
				return a.Modified.Before(b.Modified)
			}
		}
		return naturalLess(a.Name, b.Name)
	}
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		if desc {
			return less(b, a)
		}
		return less(a, b)
	})
	return items
}

// naturalLess compares names ignoring case, with runs of digits by value, so
// "file2" comes before "file10".
func naturalLess(a, b string) bool {
	ra, rb := []rune(strings.ToLower(a)), []rune(strings.ToLower(b))
	i, j := 0, 0
	for i < len(ra) && j < len(rb) {
		if unicode.IsDigit(ra[i]) && unicode.IsDigit(rb[j]) {
			si := i
			for i < len(ra) && unicode.IsDigit(ra[i]) {
				i++
			}
			sj := j
			for j < len(rb) && unicode.IsDigit(rb[j]) {
				j++
			}
			na, nb := strings.TrimLeft(string(ra[si:i]), "0"), strings.TrimLeft(string(rb[sj:j]), "0")
			if len(na) != len(nb) {
				return len(na) < len(nb)
			}
			if na != nb {
				return na < nb
			}
			continue
		}
		if ra[i] != rb[j] {
			return ra[i] < rb[j]
		}
		i++
		j++
	}
	return len(ra)-i < len(rb)-j
}

// Filter keeps the items whose name contains query, ignoring case; an empty
// query keeps everything.
func Filter(items []Item, query string) []Item {
	terms := Terms(query)
	if len(terms) == 0 {
		return items
	}
	var out []Item
	for _, it := range items {
		if Match(it.Name, terms) {
			out = append(out, it)
		}
	}
	return out
}

// Size writes a number of bytes the short way: "512 B", "1.5 MB".
func Size(n int64) string {
	if n < 0 {
		n = 0
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	v, suffix := float64(n), []string{"KB", "MB", "GB", "TB", "PB"}
	i := -1
	for v >= unit && i < len(suffix)-1 {
		v /= unit
		i++
	}
	if v >= 100 {
		return fmt.Sprintf("%.0f %s", v, suffix[i])
	}
	return fmt.Sprintf("%.1f %s", v, suffix[i])
}

// Speed writes bytes a second: "1.5 MB/s".
func Speed(bps float64) string { return Size(int64(bps)) + "/s" }

// Date writes a time as "2026-11-18 10:30" in the local zone, or "-" for the
// zero time.
func Date(t time.Time) string {
	if t.IsZero() || t.Year() < 1971 {
		return "-"
	}
	return t.Local().Format("2006-01-02 15:04")
}

// Latency writes how long a server took, "42 ms" or "1.2 s".
func Latency(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%d ms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1f s", d.Seconds())
}

// Meta is the line under an item's name: its size and date, or "folder" kind
// words for a folder given as kind.
func Meta(it Item, folder string) string {
	if it.IsDir {
		if it.Modified.IsZero() || it.Modified.Year() < 1971 {
			return folder
		}
		return folder + " · " + Date(it.Modified)
	}
	return Size(it.Size) + " · " + Date(it.Modified)
}

// KindName is the name of an item's kind for the theme's file colors:
// "video", "audio", "image", "doc" or "".
func KindName(k openlist.Kind) string {
	switch k {
	case openlist.KindVideo:
		return "video"
	case openlist.KindAudio:
		return "audio"
	case openlist.KindImage:
		return "image"
	case openlist.KindDoc:
		return "doc"
	}
	return ""
}

// Crumb is one step of a path.
type Crumb struct {
	Name string
	Path string
}

// Crumbs are the steps from the root to p, the root included first with an
// empty name.
func Crumbs(p string) []Crumb {
	out := []Crumb{{Name: "", Path: "/"}}
	cur := ""
	for _, s := range openlist.Segments(p) {
		cur += "/" + s
		out = append(out, Crumb{Name: s, Path: cur})
	}
	return out
}

// Previewable names what the preview shows an item as: "image", "video",
// "audio", "text" or "" for what it cannot show.
func Previewable(name string) string {
	switch openlist.KindOfName(name) {
	case openlist.KindImage:
		return "image"
	case openlist.KindVideo:
		return "video"
	case openlist.KindAudio:
		return "audio"
	}
	dot := strings.LastIndex(name, ".")
	if dot < 0 {
		return ""
	}
	switch strings.ToLower(name[dot+1:]) {
	case "txt", "md", "markdown", "json", "yaml", "yml", "xml", "csv", "log", "ini", "toml", "conf", "cfg",
		"go", "js", "ts", "py", "java", "c", "h", "cpp", "rs", "sh", "html", "css", "sql", "lua", "rb", "php":
		return "text"
	}
	return ""
}

// ShortMeta is what a narrow tile shows of an item: its size, or "Folder".
func ShortMeta(it Item, folder string) string {
	if it.IsDir {
		return folder
	}
	return Size(it.Size)
}
