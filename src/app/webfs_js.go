//go:build js && wasm

package app

import (
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"
	"syscall/js"
	"time"
)

// The web has no paths on disk: a folder the user chose is a handle the page
// keeps, and keeps between visits in IndexedDB. One is kept for each site.

var folders = struct {
	sync.Mutex
	m map[string]js.Value
}{m: map[string]js.Value{}}

func folderOf(siteID string) (js.Value, bool) {
	folders.Lock()
	defer folders.Unlock()
	h, ok := folders.m[siteID]
	return h, ok
}

func setFolder(siteID string, h js.Value) {
	folders.Lock()
	defer folders.Unlock()
	folders.m[siteID] = h
}

// jsErr is an error a script threw.
type jsErr struct{ name, msg string }

func (e *jsErr) Error() string {
	if e.name == "" || e.name == "Error" {
		return e.msg
	}
	return e.name + ": " + e.msg
}

func asErr(v js.Value) error {
	if v.Type() == js.TypeObject {
		return &jsErr{name: v.Get("name").String(), msg: v.Get("message").String()}
	}
	return errors.New(v.String())
}

func errName(err error) string {
	var e *jsErr
	if errors.As(err, &e) {
		return e.name
	}
	return ""
}

// then runs ok or fail when a promise settles, on the script's event loop,
// where nothing may block.
func then(p js.Value, ok func(js.Value), fail func(error)) {
	var onOK, onFail js.Func
	release := func() { onOK.Release(); onFail.Release() }
	arg := func(a []js.Value) js.Value {
		if len(a) > 0 {
			return a[0]
		}
		return js.Undefined()
	}
	onOK = js.FuncOf(func(_ js.Value, a []js.Value) any { release(); ok(arg(a)); return nil })
	onFail = js.FuncOf(func(_ js.Value, a []js.Value) any { release(); fail(asErr(arg(a))); return nil })
	p.Call("then", onOK, onFail)
}

// await waits for a promise, from a goroutine of the app's own: not from a
// callback of the script, which it would stop.
func await(p js.Value) (js.Value, error) {
	type result struct {
		v   js.Value
		err error
	}
	ch := make(chan result, 1)
	then(p, func(v js.Value) { ch <- result{v: v} }, func(err error) { ch <- result{err: err} })
	r := <-ch
	return r.v, r.err
}

// ---- IndexedDB, for the handles ----

func openDB(use func(db js.Value, err error)) {
	idb := js.Global().Get("indexedDB")
	if idb.IsUndefined() || idb.IsNull() {
		use(js.Undefined(), errors.New("this browser keeps no database"))
		return
	}
	req := idb.Call("open", "openlist-client", 1)
	var up, ok, fail js.Func
	release := func() { up.Release(); ok.Release(); fail.Release() }
	up = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		req.Get("result").Call("createObjectStore", "kv")
		return nil
	})
	ok = js.FuncOf(func(_ js.Value, _ []js.Value) any { release(); use(req.Get("result"), nil); return nil })
	fail = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		release()
		use(js.Undefined(), asErr(req.Get("error")))
		return nil
	})
	req.Set("onupgradeneeded", up)
	req.Set("onsuccess", ok)
	req.Set("onerror", fail)
}

// request calls done when an IndexedDB request ends.
func request(req js.Value, done func(js.Value, error)) {
	var ok, fail js.Func
	release := func() { ok.Release(); fail.Release() }
	ok = js.FuncOf(func(_ js.Value, _ []js.Value) any { release(); done(req.Get("result"), nil); return nil })
	fail = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		release()
		done(js.Undefined(), asErr(req.Get("error")))
		return nil
	})
	req.Set("onsuccess", ok)
	req.Set("onerror", fail)
}

func saveHandle(siteID string, h js.Value) {
	openDB(func(db js.Value, err error) {
		if err != nil {
			return
		}
		store := db.Call("transaction", "kv", "readwrite").Call("objectStore", "kv")
		if h.IsUndefined() {
			store.Call("delete", siteID)
		} else {
			store.Call("put", h, siteID)
		}
	})
}

// ---- what the app asks ----

// webFolderSupported reports whether the browser lets a page write in a
// folder the user chose.
func webFolderSupported() bool {
	return js.Global().Get("showDirectoryPicker").Type() == js.TypeFunction
}

// webFolderName is the name of the folder a site saves in, "" for none.
func webFolderName(siteID string) string {
	if h, ok := folderOf(siteID); ok {
		return h.Get("name").String()
	}
	return ""
}

// webFolderLoad brings back the folder a site chose on an earlier visit.
func webFolderLoad(siteID string, done func(name string)) {
	if _, ok := folderOf(siteID); ok {
		done(webFolderName(siteID))
		return
	}
	openDB(func(db js.Value, err error) {
		if err != nil {
			done("")
			return
		}
		req := db.Call("transaction", "kv", "readonly").Call("objectStore", "kv").Call("get", siteID)
		request(req, func(v js.Value, err error) {
			if err != nil || v.Type() != js.TypeObject {
				done("")
				return
			}
			setFolder(siteID, v)
			done(v.Get("name").String())
		})
	})
}

// webFolderPick asks the user for a folder to save in, with the browser's
// folder picker, which lets the page write to it. It has to run as the user
// presses something. A cancelled picker answers with no name.
func webFolderPick(siteID string, done func(name string, err error)) {
	picker := js.Global().Get("showDirectoryPicker")
	if picker.Type() != js.TypeFunction {
		done("", errNoFolderPicker)
		return
	}
	then(picker.Invoke(map[string]any{"mode": "readwrite"}), func(h js.Value) {
		setFolder(siteID, h)
		saveHandle(siteID, h)
		done(h.Get("name").String(), nil)
	}, func(err error) {
		if errName(err) == "AbortError" {
			done("", nil)
			return
		}
		done("", err)
	})
}

// webFolderForget lets a site save through the browser's own download again.
func webFolderForget(siteID string) {
	folders.Lock()
	delete(folders.m, siteID)
	folders.Unlock()
	saveHandle(siteID, js.Undefined())
}

// webFolderState says whether the page may write in the folder of a site:
// "granted", "prompt" (it has to ask), "denied" or "none".
func webFolderState(siteID string, done func(state string)) {
	h, ok := folderOf(siteID)
	if !ok {
		done("none")
		return
	}
	then(h.Call("queryPermission", map[string]any{"mode": "readwrite"}), func(v js.Value) { done(v.String()) },
		func(error) { done("denied") })
}

// webFolderAllow asks the browser for write access to the folder of a site,
// as the user presses something.
func webFolderAllow(siteID string, done func(granted bool, err error)) {
	h, ok := folderOf(siteID)
	if !ok {
		done(false, errors.New("no folder is chosen"))
		return
	}
	then(h.Call("requestPermission", map[string]any{"mode": "readwrite"}), func(v js.Value) { done(v.String() == "granted", nil) },
		func(err error) { done(false, err) })
}

// webFile is a file being written in a chosen folder, which appears whole
// when it is closed.
type webFile struct {
	w      js.Value
	dir    js.Value
	Name   string
	closed bool
}

// webFolderCreate makes a file in the folder of a site, under a name that
// is free there. It waits on the browser, so it runs off the script's own
// callbacks.
func webFolderCreate(siteID, name string) (*webFile, error) {
	h, ok := folderOf(siteID)
	if !ok {
		return nil, errors.New("no folder is chosen")
	}
	state, err := await(h.Call("queryPermission", map[string]any{"mode": "readwrite"}))
	if err != nil {
		return nil, err
	}
	if state.String() != "granted" {
		return nil, &jsErr{name: "NotAllowedError", msg: "the page has no write access to the folder, which the browser asks for again"}
	}
	final := name
	for i := 1; ; i++ {
		_, err := await(h.Call("getFileHandle", final))
		if err != nil {
			if errName(err) == "NotFoundError" {
				break
			}
			return nil, err
		}
		if i > 99 {
			return nil, errors.New("too many files of that name")
		}
		final = numbered(name, i)
	}
	fh, err := await(h.Call("getFileHandle", final, map[string]any{"create": true}))
	if err != nil {
		return nil, err
	}
	w, err := await(fh.Call("createWritable"))
	if err != nil {
		return nil, err
	}
	return &webFile{w: w, dir: h, Name: final}, nil
}

// numbered puts " (n)" before the extension of a name.
func numbered(name string, n int) string {
	ext := ""
	if i := strings.LastIndex(name, "."); i > 0 {
		name, ext = name[:i], name[i:]
	}
	return name + " (" + strconv.Itoa(n) + ")" + ext
}

func (f *webFile) Write(p []byte) (int, error) {
	buf := js.Global().Get("Uint8Array").New(len(p))
	js.CopyBytesToJS(buf, p)
	if _, err := await(f.w.Call("write", buf)); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Close makes the file appear in the folder.
func (f *webFile) Close() error {
	f.closed = true
	_, err := await(f.w.Call("close"))
	return err
}

// Abort leaves nothing behind.
func (f *webFile) Abort() {
	if !f.closed {
		f.closed = true
		_, _ = await(f.w.Call("abort"))
	}
}

var _ io.WriteCloser = (*webFile)(nil)

// webDownloadBlob hands data to the browser as a download, which needs no
// permission: the browser puts it in its own downloads folder.
func webDownloadBlob(name, mime string, data []byte) error {
	buf := js.Global().Get("Uint8Array").New(len(data))
	js.CopyBytesToJS(buf, data)
	blob := js.Global().Get("Blob").New([]any{buf}, map[string]any{"type": mime})
	url := js.Global().Get("URL").Call("createObjectURL", blob)
	a := js.Global().Get("document").Call("createElement", "a")
	a.Set("href", url)
	a.Set("download", name)
	a.Call("click")
	time.AfterFunc(time.Minute, func() { js.Global().Get("URL").Call("revokeObjectURL", url) })
	return nil
}
