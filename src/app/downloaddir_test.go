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

func TestRelocateIOSContainerPath(t *testing.T) {
	const (
		oldUUID = "11111111-1111-1111-1111-111111111111"
		newUUID = "22222222-2222-2222-2222-222222222222"
		dev1    = "AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA"
		dev2    = "BBBBBBBB-BBBB-BBBB-BBBB-BBBBBBBBBBBB"
	)
	deviceHome := "/var/mobile/Containers/Data/Application/" + newUUID
	simHome := "/Users/dev/Library/Developer/CoreSimulator/Devices/" + dev1 + "/data/Containers/Data/Application/" + newUUID

	cases := []struct {
		name        string
		saved       string
		currentHome string
		want        string
	}{
		{
			name:        "real device UUID change",
			saved:       "/var/mobile/Containers/Data/Application/" + oldUUID + "/Documents/OpenList",
			currentHome: deviceHome,
			want:        deviceHome + "/Documents/OpenList",
		},
		{
			name:        "private var prefix on saved",
			saved:       "/private/var/mobile/Containers/Data/Application/" + oldUUID + "/Documents/OpenList",
			currentHome: deviceHome,
			want:        deviceHome + "/Documents/OpenList",
		},
		{
			name:        "private var prefix on currentHome",
			saved:       "/var/mobile/Containers/Data/Application/" + oldUUID + "/Documents/OpenList",
			currentHome: "/private" + deviceHome,
			want:        "/private" + deviceHome + "/Documents/OpenList",
		},
		{
			name:        "same UUID unchanged",
			saved:       deviceHome + "/Documents/OpenList",
			currentHome: deviceHome,
			want:        deviceHome + "/Documents/OpenList",
		},
		{
			name:        "container root without subpath",
			saved:       "/var/mobile/Containers/Data/Application/" + oldUUID,
			currentHome: deviceHome,
			want:        deviceHome,
		},
		{
			name:        "simulator same device UUID change",
			saved:       "/Users/dev/Library/Developer/CoreSimulator/Devices/" + dev1 + "/data/Containers/Data/Application/" + oldUUID + "/Documents/OpenList",
			currentHome: simHome,
			want:        simHome + "/Documents/OpenList",
		},
		{
			name:        "simulator different device not rewritten",
			saved:       "/Users/dev/Library/Developer/CoreSimulator/Devices/" + dev2 + "/data/Containers/Data/Application/" + oldUUID + "/Documents/OpenList",
			currentHome: simHome,
			want:        "/Users/dev/Library/Developer/CoreSimulator/Devices/" + dev2 + "/data/Containers/Data/Application/" + oldUUID + "/Documents/OpenList",
		},
		{
			name:        "desktop path unchanged",
			saved:       "/Users/u/Downloads/OpenList",
			currentHome: deviceHome,
			want:        "/Users/u/Downloads/OpenList",
		},
		{
			name:        "windows path unchanged",
			saved:       `C:\Users\u\Downloads\OpenList`,
			currentHome: deviceHome,
			want:        `C:\Users\u\Downloads\OpenList`,
		},
		{
			name:        "ask sentinel unchanged",
			saved:       localDirAsk,
			currentHome: deviceHome,
			want:        localDirAsk,
		},
		{
			name:        "mem upload key unchanged",
			saved:       "mem:0",
			currentHome: deviceHome,
			want:        "mem:0",
		},
		{
			name:        "empty path unchanged",
			saved:       "",
			currentHome: deviceHome,
			want:        "",
		},
		{
			name:        "invalid UUID not rewritten",
			saved:       "/var/mobile/Containers/Data/Application/not-a-uuid/Documents/OpenList",
			currentHome: deviceHome,
			want:        "/var/mobile/Containers/Data/Application/not-a-uuid/Documents/OpenList",
		},
	}
	for _, tc := range cases {
		if got := relocateIOSContainerPath(tc.saved, tc.currentHome); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
