//go:build !windows

package app

// windowsDownloads has nothing to ask off Windows.
func windowsDownloads() string { return "" }
