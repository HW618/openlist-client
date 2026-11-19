package app

import (
	"os"
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
