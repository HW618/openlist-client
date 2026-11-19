package app

import "golang.org/x/sys/windows"

// windowsDownloads asks the system for the Downloads folder, which the user
// can move out of the profile.
func windowsDownloads() string {
	dir, err := windows.KnownFolderPath(windows.FOLDERID_Downloads, windows.KF_FLAG_DEFAULT)
	if err != nil {
		return ""
	}
	return dir
}
