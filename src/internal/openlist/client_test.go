package openlist

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fake is a stand-in OpenList server recording what it was asked.
type fake struct {
	t      *testing.T
	srv    *httptest.Server
	auth   string
	bodies map[string]map[string]any
	put    string
	putTo  string
}

func newFake(t *testing.T) (*fake, *Client) {
	f := &fake{t: t, bodies: map[string]map[string]any{}}
	mux := http.NewServeMux()
	reply := func(w http.ResponseWriter, code int, msg string, data any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "message": msg, "data": data})
	}
	handle := func(route string, answer func(body map[string]any) (int, string, any)) {
		mux.HandleFunc(route, func(w http.ResponseWriter, r *http.Request) {
			f.auth = r.Header.Get("Authorization")
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			f.bodies[route] = body
			code, msg, data := answer(body)
			reply(w, code, msg, data)
		})
	}
	handle("/api/auth/login", func(b map[string]any) (int, string, any) {
		if b["password"] != "secret" {
			return 400, "password is incorrect", nil
		}
		return 200, "success", map[string]any{"token": "tok-1"}
	})
	handle("/api/fs/list", func(b map[string]any) (int, string, any) {
		if f.auth == "" && b["path"] == "/private" {
			return 401, "guest is disabled", nil
		}
		return 200, "success", map[string]any{"total": 2, "provider": "Local", "content": []map[string]any{
			{"name": "docs", "is_dir": true, "type": 1, "modified": "2026-11-18T09:12:00Z"},
			{"name": "movie.mp4", "size": 1234, "type": 2, "modified": "2026-11-18T09:13:00Z", "sign": "abc"},
		}}
	})
	handle("/api/fs/get", func(b map[string]any) (int, string, any) {
		return 200, "success", map[string]any{"name": "movie.mp4", "size": 1234, "raw_url": "/d/movie.mp4?sign=abc"}
	})
	for _, op := range []string{"mkdir", "rename", "remove", "move", "copy", "add_offline_download"} {
		handle("/api/fs/"+op, func(map[string]any) (int, string, any) { return 200, "success", nil })
	}
	handle("/api/public/offline_download_tools", func(map[string]any) (int, string, any) {
		return 200, "success", []string{"aria2", "qBittorrent"}
	})
	mux.HandleFunc("/api/fs/put", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		f.put, f.putTo = string(b), r.Header.Get("File-Path")
		reply(w, 200, "success", nil)
	})
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "pong") })
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f, &Client{BaseURL: f.srv.URL, Token: "tok-0"}
}

func TestLogin(t *testing.T) {
	_, c := newFake(t)
	tok, err := c.Login(context.Background(), "admin", "secret")
	if err != nil || tok != "tok-1" {
		t.Fatalf("Login = %q, %v", tok, err)
	}
	_, err = c.Login(context.Background(), "admin", "wrong")
	var api *APIError
	if err == nil || !strings.Contains(err.Error(), "password is incorrect") {
		t.Fatalf("a wrong password gave %v", err)
	}
	_ = api
}

func TestListSendsTokenAndDecodes(t *testing.T) {
	f, c := newFake(t)
	l, err := c.List(context.Background(), "mnt/a/", true)
	if err != nil {
		t.Fatal(err)
	}
	if f.auth != "tok-0" || f.bodies["/api/fs/list"]["path"] != "/mnt/a" || f.bodies["/api/fs/list"]["refresh"] != true {
		t.Fatalf("request = %v, auth %q", f.bodies["/api/fs/list"], f.auth)
	}
	if len(l.Entries) != 2 || l.Provider != "Local" {
		t.Fatalf("listing = %+v", l)
	}
	if l.Entries[0].Kind() != KindFolder || l.Entries[1].Kind() != KindVideo || l.Entries[1].Sign != "abc" {
		t.Fatalf("entries = %+v", l.Entries)
	}
}

func TestUnauthorized(t *testing.T) {
	_, c := newFake(t)
	c.Token = ""
	_, err := c.List(context.Background(), "/private", false)
	if !Unauthorized(err) {
		t.Fatalf("List without a token gave %v", err)
	}
}

func TestGetMakesRawURLAbsolute(t *testing.T) {
	f, c := newFake(t)
	o, err := c.Get(context.Background(), "/movie.mp4")
	if err != nil || o.RawURL != f.srv.URL+"/d/movie.mp4?sign=abc" {
		t.Fatalf("Get = %+v, %v", o, err)
	}
}

func TestChangesSendTheirArguments(t *testing.T) {
	f, c := newFake(t)
	ctx := context.Background()
	if err := c.Move(ctx, "/a", "/b", []string{"x", "y"}); err != nil {
		t.Fatal(err)
	}
	m := f.bodies["/api/fs/move"]
	if m["src_dir"] != "/a" || m["dst_dir"] != "/b" || len(m["names"].([]any)) != 2 {
		t.Fatalf("move body = %v", m)
	}
	if err := c.Rename(ctx, "/a/x", "z"); err != nil || f.bodies["/api/fs/rename"]["name"] != "z" {
		t.Fatalf("rename: %v %v", err, f.bodies["/api/fs/rename"])
	}
	if err := c.Remove(ctx, "/a", []string{"x"}); err != nil || f.bodies["/api/fs/remove"]["dir"] != "/a" {
		t.Fatalf("remove: %v", err)
	}
	if err := c.Mkdir(ctx, "/a/new"); err != nil || f.bodies["/api/fs/mkdir"]["path"] != "/a/new" {
		t.Fatalf("mkdir: %v", err)
	}
}

func TestPutStreamsBodyToPath(t *testing.T) {
	f, c := newFake(t)
	if err := c.Put(context.Background(), "/up/一个 文件.txt", strings.NewReader("hello"), 5); err != nil {
		t.Fatal(err)
	}
	if f.put != "hello" || f.putTo != "%2Fup%2F%E4%B8%80%E4%B8%AA%20%E6%96%87%E4%BB%B6.txt" {
		t.Fatalf("server got %q at %q", f.put, f.putTo)
	}
}

func TestOfflineDownload(t *testing.T) {
	f, c := newFake(t)
	ctx := context.Background()
	tools, err := c.OfflineTools(ctx)
	if err != nil || len(tools) != 2 || tools[0] != "aria2" {
		t.Fatalf("tools = %v, %v", tools, err)
	}
	if err := c.AddOfflineDownload(ctx, "/dl", []string{"https://x/y.zip"}, "aria2"); err != nil {
		t.Fatal(err)
	}
	b := f.bodies["/api/fs/add_offline_download"]
	if b["path"] != "/dl" || b["tool"] != "aria2" {
		t.Fatalf("body = %v", b)
	}
}

func TestPing(t *testing.T) {
	_, c := newFake(t)
	if d, err := c.Ping(context.Background()); err != nil || d <= 0 {
		t.Fatalf("Ping = %v, %v", d, err)
	}
	c.BaseURL = "http://127.0.0.1:1"
	if _, err := c.Ping(context.Background()); err == nil {
		t.Fatal("Ping of a closed port succeeded")
	}
}

func TestPaths(t *testing.T) {
	for _, c := range []struct{ got, want string }{
		{Clean(""), "/"}, {Clean("a//b/"), "/a/b"}, {Join("/a", "b"), "/a/b"}, {Join("/", "b"), "/b"},
		{Parent("/a/b"), "/a"}, {Parent("/a"), "/"}, {Parent("/"), "/"}, {Base("/a/b"), "b"}, {Base("/"), ""},
	} {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
	if s := Segments("/a/b"); len(s) != 2 || s[1] != "b" {
		t.Errorf("Segments = %v", s)
	}
	if Segments("/") != nil {
		t.Error("the root has segments")
	}
}

func TestDirectLink(t *testing.T) {
	c := &Client{BaseURL: "https://x.test/"}
	if got := c.DirectLink("/a b/c.txt", "s=1"); got != "https://x.test/d/a%20b/c.txt?sign=s%3D1" {
		t.Fatalf("DirectLink = %q", got)
	}
}

func TestOfflineTasksAreListedWithRunningFirst(t *testing.T) {
	var asked []string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/admin/task/", func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.Method+" "+r.URL.RequestURI())
		var data any = []any{}
		switch r.URL.Path {
		case "/api/admin/task/offline_download/undone":
			data = []map[string]any{{"id": "a", "name": "download a.iso", "state": 1, "status": "[aria2]: downloading", "progress": 42.5}}
		case "/api/admin/task/offline_download/done":
			data = []map[string]any{{"id": "b", "name": "download b.iso", "state": 7, "error": "boom", "progress": 10}}
		case "/api/admin/task/offline_download_transfer/done":
			data = []map[string]any{{"id": "c", "name": "transfer c.iso", "state": 2, "progress": 100}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "message": "success", "data": data})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := &Client{BaseURL: srv.URL}
	got, err := c.OfflineTasks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].ID != "a" || got[0].Progress != 42.5 || got[0].Finished() {
		t.Fatalf("tasks = %+v", got)
	}
	var failed, transfer int
	for _, tk := range got {
		if tk.Failed() {
			failed++
		}
		if tk.Transfer {
			transfer++
		}
	}
	if failed != 1 || transfer != 1 {
		t.Fatalf("failed %d, transfer %d in %+v", failed, transfer, got)
	}
	if err := c.OfflineTaskAction(context.Background(), got[2], "retry"); err != nil {
		t.Fatal(err)
	}
	if last := asked[len(asked)-1]; !strings.HasPrefix(last, "POST /api/admin/task/") || !strings.HasSuffix(last, "/retry?tid="+got[2].ID) {
		t.Fatalf("last request = %q", last)
	}
}
