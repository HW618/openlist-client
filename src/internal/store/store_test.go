package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPushRecentKeepsThreeNewestFirst(t *testing.T) {
	var s SiteSettings
	for _, d := range []string{"/a", "/b", "/c", "/d"} {
		s.PushRecent(d)
	}
	if got := s.RecentOffline; len(got) != 3 || got[0] != "/d" || got[2] != "/b" {
		t.Fatalf("recent = %v", got)
	}
	s.PushRecent("/c")
	if got := s.RecentOffline; len(got) != 3 || got[0] != "/c" || got[1] != "/d" || got[2] != "/b" {
		t.Fatalf("after reusing /c: %v", got)
	}
}

func TestNormalizeURL(t *testing.T) {
	for in, want := range map[string]string{
		" https://pan.example.com/ ":   "https://pan.example.com",
		"http://192.168.1.10:5244":     "http://192.168.1.10:5244",
		"https://x.test/sub/?a=1#frag": "https://x.test/sub",
	} {
		if got, err := NormalizeURL(in); err != nil || got != want {
			t.Errorf("NormalizeURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "pan.example.com", "ftp://x", "http://", "://x"} {
		if _, err := NormalizeURL(bad); err == nil {
			t.Errorf("NormalizeURL(%q) accepted it", bad)
		}
	}
}

func TestSortKeyNext(t *testing.T) {
	if SortName.Next() != SortSize || SortSize.Next() != SortDate || SortDate.Next() != SortName {
		t.Fatal("the order is name, size, date")
	}
}

func TestPrefsFix(t *testing.T) {
	p := Prefs{View: "x", Sort: "y"}.Fix()
	if p.View != ViewList || p.Sort != SortName {
		t.Fatalf("Fix = %+v", p)
	}
}

func TestFileSecrets(t *testing.T) {
	dir := t.TempDir()
	s := &FileSecrets{Dir: filepath.Join(dir, "sub")}
	if _, ok := s.Get("a"); ok {
		t.Fatal("empty store has a token")
	}
	if err := s.Set("a", "tok"); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("b", "tok-b"); err != nil {
		t.Fatal(err)
	}
	if tok, ok := s.Get("a"); !ok || tok != "tok" {
		t.Fatalf("Get = %q, %v", tok, ok)
	}
	if fi, err := os.Stat(s.file()); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, %v", fi, err)
	}
	if err := s.Remove("a"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("a"); ok {
		t.Fatal("removed token still there")
	}
	if tok, _ := s.Get("b"); tok != "tok-b" {
		t.Fatal("removing a removed b")
	}
}

func TestNewIDIsUnique(t *testing.T) {
	if NewID() == NewID() {
		t.Fatal("two IDs match")
	}
}

func TestFileSecretsAreSealedAndOutliveTheApp(t *testing.T) {
	dir := t.TempDir()
	s := &FileSecrets{Dir: dir}
	if err := s.Set("a", "super-secret-token"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tokens.enc", "tokens.key"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "super-secret-token") {
			t.Fatalf("%s holds the token as text", name)
		}
	}
	// Another run of the app reads what the first kept.
	again := &FileSecrets{Dir: dir}
	if tok, ok := again.Get("a"); !ok || tok != "super-secret-token" {
		t.Fatalf("Get after a restart = %q, %v", tok, ok)
	}
	// A sealed file that was tampered with holds nothing.
	b, _ := os.ReadFile(filepath.Join(dir, "tokens.enc"))
	b[len(b)-1] ^= 1
	if err := os.WriteFile(filepath.Join(dir, "tokens.enc"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := (&FileSecrets{Dir: dir}).Get("a"); ok {
		t.Fatal("a tampered file gave a token")
	}
}

func TestFileSecretsTakeTheTokensOfEarlierVersions(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "tokens.json"), []byte(`{"a":"old"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &FileSecrets{Dir: dir}
	if tok, ok := s.Get("a"); !ok || tok != "old" {
		t.Fatalf("Get = %q, %v", tok, ok)
	}
	if err := s.Set("b", "new"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "tokens.json")); err == nil {
		t.Fatal("the text file is still there")
	}
	if tok, _ := s.Get("a"); tok != "old" {
		t.Fatal("the earlier token was lost")
	}
}

func TestFileSecretsAllAndWipe(t *testing.T) {
	dir := t.TempDir()
	s := &FileSecrets{Dir: dir}
	_ = s.Set("a", "1")
	_ = s.Set("b", "2")
	if all := s.All(); len(all) != 2 || all["a"] != "1" || all["b"] != "2" {
		t.Fatalf("All = %v", all)
	}
	s.Wipe()
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("files left: %v", entries)
	}
	if len(s.All()) != 0 {
		t.Fatal("tokens left")
	}
}
