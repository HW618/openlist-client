package app

import (
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// localDirAsk is what the folder setting holds where the user wants a save
// dialog for each download (Android only), and not a folder.
const localDirAsk = "@ask"

// appFolder is the folder of the app's own in a Downloads folder.
const appFolder = "OpenList"

// defaultDownloadDir is the app's folder in the Downloads folder of this
// system, or in the temporary files where there is none.
func defaultDownloadDir() string {
	home, _ := os.UserHomeDir()
	if dir := downloadsFolder(runtime.GOOS, home, os.Getenv, os.ReadFile); dir != "" {
		return filepath.Join(dir, appFolder)
	}
	return filepath.Join(os.TempDir(), appFolder)
}

// downloadsFolder finds the Downloads folder of a system from its home
// folder, its environment and the files it keeps; "" where the system has
// none to give.
//
//   - macOS and Windows: Downloads in the home folder; Windows asks the system
//     first, as the user can move it (downloads_windows.go).
//   - Linux: the user's XDG_DOWNLOAD_DIR, which is named in the user's language
//     ("下载") and may be anywhere, else Downloads.
//   - Android: the shared Download folder of the device's storage.
//   - iOS: an app has no Downloads folder, so its own Documents.
//   - A browser: none; it keeps its own.
func downloadsFolder(goos, home string, env func(string) string, read func(string) ([]byte, error)) string {
	switch goos {
	case "js":
		return ""
	case "android":
		return filepath.Join(androidStorage(env), "Download")
	case "ios":
		if home == "" {
			return ""
		}
		return filepath.Join(home, "Documents")
	case "windows":
		if dir := windowsDownloads(); dir != "" {
			return dir
		}
	case "linux", "freebsd", "openbsd", "netbsd":
		if dir := xdgDownloads(home, env, read); dir != "" {
			return dir
		}
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, "Downloads")
}

// androidStorage is the shared storage of the device that the user sees as
// its internal storage.
func androidStorage(env func(string) string) string {
	if dir := env("EXTERNAL_STORAGE"); dir != "" {
		return dir
	}
	return "/storage/emulated/0"
}

// androidFolders are the shared folders of the device's storage that a
// download may go to, by name.
var androidFolders = []string{"Download", "Documents", "Pictures", "Movies", "Music"}

// androidFolderDir is the app's folder in one of the shared folders.
func androidFolderDir(name string) string {
	return filepath.Join(androidStorage(os.Getenv), name, appFolder)
}

// xdgDownloads reads the user's Downloads folder of the freedesktop
// user-dirs: the environment's, else the user-dirs.dirs file of the config
// folder.
func xdgDownloads(home string, env func(string) string, read func(string) ([]byte, error)) string {
	expand := func(v string) string {
		v = strings.TrimSpace(v)
		switch {
		case v == "$HOME":
			v = home
		case strings.HasPrefix(v, "$HOME/"):
			v = filepath.Join(home, v[len("$HOME/"):])
		}
		if !filepath.IsAbs(v) || v == home {
			return ""
		}
		return filepath.Clean(v)
	}
	if dir := expand(env("XDG_DOWNLOAD_DIR")); dir != "" {
		return dir
	}
	conf := env("XDG_CONFIG_HOME")
	if conf == "" && home != "" {
		conf = filepath.Join(home, ".config")
	}
	data, err := read(filepath.Join(conf, "user-dirs.dirs"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "XDG_DOWNLOAD_DIR=") {
			continue
		}
		return expand(strings.Trim(strings.TrimPrefix(line, "XDG_DOWNLOAD_DIR="), `"'`))
	}
	return ""
}

// relocateSavedPath rewrites stale iOS sandbox container paths whose container
// UUID changed across app upgrades. On other platforms it returns p unchanged.
var relocateSavedPath = defaultRelocateSavedPath

func defaultRelocateSavedPath(p string) string {
	if runtime.GOOS != "ios" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	return relocateIOSContainerPath(p, home)
}

func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}

func normalizeIOSPath(p string) string {
	p = filepath.ToSlash(p)
	if strings.HasPrefix(p, "/private/var/") {
		p = strings.TrimPrefix(p, "/private")
	}
	return path.Clean(p)
}

func splitIOSContainer(p string) (prefix, id, tail string, ok bool) {
	const marker = "/Containers/Data/Application/"
	norm := normalizeIOSPath(p)
	idx := strings.Index(norm, marker)
	if idx < 0 {
		return "", "", "", false
	}
	prefix = norm[:idx]
	rest := norm[idx+len(marker):]
	id, tail, _ = strings.Cut(rest, "/")
	if !isUUID(id) {
		return "", "", "", false
	}
	return prefix, id, tail, true
}

// relocateIOSContainerPath rewrites saved onto currentHome when both refer to
// the same iOS app container root (/Containers/Data/Application/<UUID>) and the
// container UUID changed.
func relocateIOSContainerPath(saved, currentHome string) string {
	if saved == "" || currentHome == "" || saved == localDirAsk || strings.HasPrefix(saved, "mem:") {
		return saved
	}
	prefix, id, tail, ok := splitIOSContainer(saved)
	if !ok {
		return saved
	}
	homePrefix, homeID, homeTail, ok := splitIOSContainer(currentHome)
	if !ok || homeTail != "" || prefix != homePrefix {
		return saved
	}
	if id == homeID {
		return saved
	}
	cleanHome := path.Clean(filepath.ToSlash(currentHome))
	if tail == "" {
		return cleanHome
	}
	return path.Join(cleanHome, tail)
}
