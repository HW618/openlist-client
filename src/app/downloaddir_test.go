package app

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/HW618/openlist-client/src/internal/store"
)

func TestDownloadsFolderOfEachSystem(t *testing.T) {
	files := map[string]string{
		filepath.FromSlash("/home/u/.config/user-dirs.dirs"): "# comment\nXDG_DESKTOP_DIR=\"$HOME/桌面\"\nXDG_DOWNLOAD_DIR=\"$HOME/下载\"\n",
		filepath.FromSlash("/conf/user-dirs.dirs"):           "XDG_DOWNLOAD_DIR=\"/data/dl\"\n",
	}
	read := func(p string) ([]byte, error) {
		if s, ok := files[p]; ok {
			return []byte(s), nil
		}
		return nil, errors.New("missing")
	}
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	none := env(nil)

	cases := []struct {
		name, goos, home string
		env              func(string) string
		want             string
	}{
		{"mac", "darwin", "/Users/u", none, filepath.FromSlash("/Users/u/Downloads")},
		{"windows without the system's answer", "windows", `C:\Users\u`, none, filepath.Join(`C:\Users\u`, "Downloads")},
		{"linux in the user's language", "linux", "/home/u", none, filepath.FromSlash("/home/u/下载")},
		{"linux with another config folder", "linux", "/home/u", env(map[string]string{"XDG_CONFIG_HOME": "/conf"}), filepath.FromSlash("/data/dl")},
		{"linux with the folder in the environment", "linux", "/home/u", env(map[string]string{"XDG_DOWNLOAD_DIR": "/mnt/d"}), filepath.FromSlash("/mnt/d")},
		{"linux with no config", "linux", "/home/x", none, filepath.FromSlash("/home/x/Downloads")},
		{"android", "android", "/data/user/0/app", none, filepath.FromSlash("/storage/emulated/0/Download")},
		{"android with its storage named", "android", "/data", env(map[string]string{"EXTERNAL_STORAGE": "/sdcard"}), filepath.FromSlash("/sdcard/Download")},
		{"ios keeps to the app's Documents", "ios", "/var/mobile/app", none, filepath.FromSlash("/var/mobile/app/Documents")},
		{"browser has none", "js", "", none, ""},
		{"no home", "darwin", "", none, ""},
	}
	for _, c := range cases {
		if got := downloadsFolder(c.goos, c.home, c.env, read); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestDownloadDirIsTheChosenFolderElseTheSystems(t *testing.T) {
	a := &App{}
	def := defaultDownloadDir()
	if def == "" {
		t.Fatal("no default download folder")
	}
	if got := a.downloadDir(store.Site{}); got != def {
		t.Fatalf("unset: got %q, want %q", got, def)
	}
	if got := a.downloadDir(store.Site{Settings: store.SiteSettings{LocalDir: "/x/y"}}); got != "/x/y" {
		t.Fatalf("chosen: got %q", got)
	}
	// "Ask for each download" is no folder: the default is what is tried.
	if got := a.downloadDir(store.Site{Settings: store.SiteSettings{LocalDir: localDirAsk}}); got != def {
		t.Fatalf("ask: got %q, want %q", got, def)
	}
}
