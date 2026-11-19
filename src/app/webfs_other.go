//go:build !(js && wasm)

package app

import (
	"errors"
	"io"
)

// Outside the browser a chosen folder is a path, and none of this is needed.

func webFolderSupported() bool                         { return false }
func webFolderName(string) string                      { return "" }
func webFolderLoad(_ string, done func(name string))   { done("") }
func webFolderPick(_ string, done func(string, error)) { done("", errNoFolderPicker) }
func webFolderForget(string)                           {}
func webFolderState(_ string, done func(state string)) { done("none") }
func webFolderAllow(_ string, done func(bool, error))  { done(false, errors.New("not a browser")) }
func webDownloadBlob(string, string, []byte) error     { return errors.New("not a browser") }

// webFile is a file written in a chosen folder.
type webFile struct{ Name string }

func (*webFile) Write([]byte) (int, error) { return 0, errors.New("not a browser") }
func (*webFile) Close() error              { return nil }
func (*webFile) Abort()                    {}

var _ io.WriteCloser = (*webFile)(nil)

func webFolderCreate(string, string) (*webFile, error) { return nil, errors.New("not a browser") }
