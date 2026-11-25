// Package access finds out whether the app may do what it needs, the network
// and a folder to save in, and tells a failure of permission from any other.
package access

import (
	"context"
	"errors"
	"net/http"
	"os"
	"regexp"
	"strings"
)

// Kind is what a problem is about.
type Kind uint8

const (
	// None is a failure that is no matter of permission.
	None Kind = iota
	// Folder is the system refusing to write in a folder on this device.
	Folder
	// Network is the system refusing the app a connection.
	Network
	// Server is the server refusing the account: it is not signed in, or
	// lacks the right.
	Server
	// Browser is the browser refusing to save a file or to write in a folder
	// the user chose.
	Browser
	// Keychain is the system having no secure store to keep the sign-in in,
	// as a Linux session without a Secret Service.
	Keychain
	// Notifications is the system refusing to show notifications, or the
	// user having denied them.
	Notifications
)

// Problem is a failure of permission.
type Problem struct {
	Kind Kind
	// Detail is the error as it was reported.
	Detail string
}

var (
	serverRe  = regexp.MustCompile(`\(40[13]\)|answered 40[13]\b|status 40[13]\b|\bunauthorized\b|\bforbidden\b`)
	networkRe = regexp.MustCompile(`(dial|connect|connection|lookup)[^\n]*(operation not permitted|no route to host|permission denied|not allowed)`)
	browserRe = regexp.MustCompile(`createwritable|not allowed by the user agent|notallowederror|securityerror|request is not allowed|user activation|requires user`)
	folderRe  = regexp.MustCompile(`permission denied|operation not permitted|read-only file system|access is denied|not permitted|eacces|eperm`)
)

// ClassifyText tells the kind of a failure from its message, as a transfer
// keeps it.
func ClassifyText(msg string) Problem {
	low := strings.ToLower(msg)
	switch {
	case msg == "":
		return Problem{}
	case browserRe.MatchString(low):
		return Problem{Browser, msg}
	case networkRe.MatchString(low):
		return Problem{Network, msg}
	case serverRe.MatchString(low):
		return Problem{Server, msg}
	case folderRe.MatchString(low):
		return Problem{Folder, msg}
	}
	return Problem{None, msg}
}

// Classify tells the kind of an error.
func Classify(err error) Problem {
	if err == nil {
		return Problem{}
	}
	if errors.Is(err, os.ErrPermission) {
		if p := ClassifyText(err.Error()); p.Kind == Network {
			return p
		}
		return Problem{Folder, err.Error()}
	}
	return ClassifyText(err.Error())
}

// ProbeDir finds out whether a file can be made in dir, which it makes when
// it is not there.
func ProbeDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".openlist-probe-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_, err = f.Write([]byte{0})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if rerr := os.Remove(name); err == nil {
		err = rerr
	}
	return err
}

// ProbeNetwork finds out whether the app may connect to a server: a request
// for its /ping that gets any answer at all has been let through.
func ProbeNetwork(ctx context.Context, hc *http.Client, base string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/ping", nil)
	if err != nil {
		return err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}
