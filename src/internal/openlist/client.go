// Package openlist is a small client for the OpenList HTTP API
// (https://github.com/OpenListTeam/OpenList): sign in, list and change files,
// upload, and queue offline downloads.
//
// Every JSON endpoint answers with the envelope {code, message, data}; a code
// other than 200 becomes an *APIError.
package openlist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Client talks to one OpenList server.
type Client struct {
	// BaseURL is the server's address, such as https://pan.example.com.
	BaseURL string
	// Token is sent as the Authorization header; empty is a guest.
	Token string
	// HTTP is the client requests go through; http.DefaultClient when nil.
	HTTP *http.Client
}

// APIError is an error the server answered with.
type APIError struct {
	// Code is the envelope's code, or the HTTP status when the answer was no
	// envelope.
	Code    int
	Message string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return "openlist: error " + strconv.Itoa(e.Code)
	}
	return fmt.Sprintf("openlist: %s (%d)", e.Message, e.Code)
}

// Unauthorized reports whether err says the token is missing, wrong or
// expired.
func Unauthorized(err error) bool {
	var a *APIError
	return errors.As(err, &a) && (a.Code == 401 || a.Code == 403)
}

// Kind is what an entry is, which decides its icon and how it previews.
type Kind uint8

const (
	KindOther Kind = iota
	KindFolder
	KindVideo
	KindAudio
	KindDoc
	KindImage
	KindArchive
)

// Entry is a file or folder.
type Entry struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	IsDir    bool      `json:"is_dir"`
	Modified time.Time `json:"modified"`
	Sign     string    `json:"sign"`
	Thumb    string    `json:"thumb"`
	// Type is the server's own classification: 1 folder, 2 video, 3 audio,
	// 4 text, 5 image.
	Type int `json:"type"`
}

// Kind classifies e from the server's type, falling back to its extension.
func (e Entry) Kind() Kind {
	if e.IsDir || e.Type == 1 {
		return KindFolder
	}
	switch e.Type {
	case 2:
		return KindVideo
	case 3:
		return KindAudio
	case 4:
		return KindDoc
	case 5:
		return KindImage
	}
	return KindOfName(e.Name)
}

// KindOfName classifies a file by its extension.
func KindOfName(name string) Kind {
	switch strings.ToLower(strings.TrimPrefix(path.Ext(name), ".")) {
	case "mp4", "mkv", "mov", "avi", "webm", "m4v", "flv", "wmv":
		return KindVideo
	case "mp3", "flac", "wav", "aac", "m4a", "ogg", "opus", "wma":
		return KindAudio
	case "png", "jpg", "jpeg", "gif", "webp", "bmp", "svg", "heic", "avif":
		return KindImage
	case "txt", "md", "markdown", "json", "yaml", "yml", "log", "ini", "conf", "xml", "csv", "go", "js", "ts", "py", "sh", "html", "css":
		return KindDoc
	case "zip", "rar", "7z", "tar", "gz", "tgz", "bz2", "xz", "iso", "dmg", "pkg":
		return KindArchive
	}
	return KindOther
}

// Listing is a folder's content.
type Listing struct {
	Entries []Entry
	// Provider names the storage driver that holds the folder, when the
	// server says.
	Provider string
	Total    int
}

// Object is one file or folder with what is needed to read it.
type Object struct {
	Entry
	// RawURL is where the content can be fetched from.
	RawURL   string `json:"raw_url"`
	Provider string `json:"provider"`
}

// OfflineTool is a downloader the server offers for offline downloads.
type OfflineTool string

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

var defaultHTTP = &http.Client{CheckRedirect: CheckRedirect}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return defaultHTTP
}

// URL joins an API path to the server's address.
func (c *Client) URL(p string) string {
	return strings.TrimRight(c.BaseURL, "/") + p
}

func (c *Client) newRequest(ctx context.Context, method, p string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.URL(p), body)
	if err != nil {
		return nil, err
	}
	if c.Token != "" {
		req.Header.Set("Authorization", c.Token)
	}
	return req, nil
}

// call sends body as JSON to the endpoint and decodes the envelope's data
// into out, which may be nil.
func (c *Client) call(ctx context.Context, method, p string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := c.newRequest(ctx, method, p, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.finish(req, out)
}

func (c *Client) finish(req *http.Request, out any) error {
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		if resp.StatusCode >= 400 {
			return &APIError{Code: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
		}
		return fmt.Errorf("openlist: the answer is no OpenList envelope: %w", err)
	}
	if env.Code != 200 {
		return &APIError{Code: env.Code, Message: env.Message}
	}
	if out != nil && len(env.Data) > 0 && string(env.Data) != "null" {
		return json.Unmarshal(env.Data, out)
	}
	return nil
}

// Login signs in with an account and returns the token to use.
func (c *Client) Login(ctx context.Context, username, password string) (string, error) {
	var out struct {
		Token string `json:"token"`
	}
	err := c.call(ctx, http.MethodPost, "/api/auth/login", map[string]string{"username": username, "password": password}, &out)
	return out.Token, err
}

// Ping measures how long the server takes to answer, and fails when it does
// not.
func (c *Client) Ping(ctx context.Context) (time.Duration, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/ping", nil)
	if err != nil {
		return 0, err
	}
	start := time.Now()
	resp, err := c.http().Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode >= 400 {
		return 0, &APIError{Code: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
	}
	return time.Since(start), nil
}

// List reads the folder at dir; refresh asks the server to skip its cache.
func (c *Client) List(ctx context.Context, dir string, refresh bool) (Listing, error) {
	var out struct {
		Content  []Entry `json:"content"`
		Total    int     `json:"total"`
		Provider string  `json:"provider"`
	}
	err := c.call(ctx, http.MethodPost, "/api/fs/list", map[string]any{"path": Clean(dir), "page": 1, "per_page": 0, "refresh": refresh}, &out)
	return Listing{Entries: out.Content, Provider: out.Provider, Total: out.Total}, err
}

// Get reads one file or folder, with the address its content is fetched from.
func (c *Client) Get(ctx context.Context, p string) (Object, error) {
	var out Object
	err := c.call(ctx, http.MethodPost, "/api/fs/get", map[string]any{"path": Clean(p)}, &out)
	if err == nil {
		out.RawURL = c.Absolute(out.RawURL)
	}
	return out, err
}

// CheckRedirect enforces the token safety rule across HTTP redirects: it
// rejects non-HTTP(S) targets and strips Authorization whenever a redirect
// changes hosts or downgrades from HTTPS to HTTP.
func CheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if !strings.EqualFold(req.URL.Scheme, "http") && !strings.EqualFold(req.URL.Scheme, "https") {
		return fmt.Errorf("openlist: unsupported redirect scheme %q", req.URL.Scheme)
	}
	for _, prev := range via {
		if strings.EqualFold(prev.URL.Scheme, "https") && !strings.EqualFold(req.URL.Scheme, "https") {
			req.Header.Del("Authorization")
			break
		}
		if !strings.EqualFold(prev.URL.Host, req.URL.Host) {
			req.Header.Del("Authorization")
			break
		}
	}
	return nil
}

// Absolute makes a server-relative address absolute, rejecting non-HTTP(S) schemes.
func (c *Client) Absolute(u string) string {
	if u == "" {
		return ""
	}
	if !strings.HasPrefix(u, "/") {
		if parsed, err := url.Parse(u); err != nil {
			return ""
		} else if parsed.Scheme != "" {
			if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
				return ""
			}
			return u
		}
		u = "/" + u
	}
	return c.URL(u)
}

// DirectLink is the address /d serves the file at p from; sign is the
// entry's signature, when the server signs links.
func (c *Client) DirectLink(p, sign string) string {
	u := c.URL("/d" + (&url.URL{Path: Clean(p)}).EscapedPath())
	if sign != "" {
		u += "?sign=" + url.QueryEscape(sign)
	}
	return u
}

// Search finds entries under dir whose names contain keywords. It needs the
// server's search index.
func (c *Client) Search(ctx context.Context, dir, keywords string) ([]SearchResult, error) {
	var out struct {
		Content []SearchResult `json:"content"`
	}
	err := c.call(ctx, http.MethodPost, "/api/fs/search", map[string]any{"parent": Clean(dir), "keywords": keywords, "scope": 0, "page": 1, "per_page": 100}, &out)
	return out.Content, err
}

// SearchResult is one hit of Search.
type SearchResult struct {
	Parent string `json:"parent"`
	Name   string `json:"name"`
	IsDir  bool   `json:"is_dir"`
	Size   int64  `json:"size"`
	Type   int    `json:"type"`
}

// Path is the full path of the hit.
func (r SearchResult) Path() string { return Join(r.Parent, r.Name) }

// Mkdir makes the folder at p.
func (c *Client) Mkdir(ctx context.Context, p string) error {
	return c.call(ctx, http.MethodPost, "/api/fs/mkdir", map[string]any{"path": Clean(p)}, nil)
}

// Rename gives the file or folder at p a new name.
func (c *Client) Rename(ctx context.Context, p, name string) error {
	return c.call(ctx, http.MethodPost, "/api/fs/rename", map[string]any{"path": Clean(p), "name": name}, nil)
}

// Remove deletes the named children of dir.
func (c *Client) Remove(ctx context.Context, dir string, names []string) error {
	return c.call(ctx, http.MethodPost, "/api/fs/remove", map[string]any{"dir": Clean(dir), "names": names}, nil)
}

// Move moves the named children of src into dst.
func (c *Client) Move(ctx context.Context, src, dst string, names []string) error {
	return c.call(ctx, http.MethodPost, "/api/fs/move", map[string]any{"src_dir": Clean(src), "dst_dir": Clean(dst), "names": names}, nil)
}

// Copy copies the named children of src into dst.
func (c *Client) Copy(ctx context.Context, src, dst string, names []string) error {
	return c.call(ctx, http.MethodPost, "/api/fs/copy", map[string]any{"src_dir": Clean(src), "dst_dir": Clean(dst), "names": names}, nil)
}

// Put uploads r, size bytes long, to the file at p, replacing it.
func (c *Client) Put(ctx context.Context, p string, r io.Reader, size int64) error {
	req, err := c.newRequest(ctx, http.MethodPut, "/api/fs/put", r)
	if err != nil {
		return err
	}
	req.Header.Set("File-Path", url.PathEscape(Clean(p)))
	req.Header.Set("Content-Type", "application/octet-stream")
	if size >= 0 {
		req.ContentLength = size
	}
	return c.finish(req, nil)
}

// OfflineTools lists the downloaders the server offers.
func (c *Client) OfflineTools(ctx context.Context) ([]OfflineTool, error) {
	var out []string
	if err := c.call(ctx, http.MethodGet, "/api/public/offline_download_tools", nil, &out); err != nil {
		return nil, err
	}
	tools := make([]OfflineTool, len(out))
	for i, t := range out {
		tools[i] = OfflineTool(t)
	}
	return tools, nil
}

// AddOfflineDownload queues the links to be downloaded by tool into dir.
func (c *Client) AddOfflineDownload(ctx context.Context, dir string, urls []string, tool OfflineTool) error {
	return c.call(ctx, http.MethodPost, "/api/fs/add_offline_download",
		map[string]any{"path": Clean(dir), "urls": urls, "tool": string(tool), "delete_policy": "delete_on_upload_succeed"}, nil)
}

// Clean returns p as an absolute, slash-separated path without a trailing
// slash; the empty path is the root.
func Clean(p string) string {
	if p == "" {
		return "/"
	}
	return path.Clean("/" + p)
}

// Join appends name to the folder dir.
func Join(dir, name string) string { return Clean(path.Join(Clean(dir), name)) }

// Parent is the folder that holds p; the root is its own parent.
func Parent(p string) string { return Clean(path.Dir(Clean(p))) }

// Base is the last element of p, or "" for the root.
func Base(p string) string {
	p = Clean(p)
	if p == "/" {
		return ""
	}
	return path.Base(p)
}

// Segments splits p into its folder names, root first excluded.
func Segments(p string) []string {
	p = Clean(p)
	if p == "/" {
		return nil
	}
	return strings.Split(strings.TrimPrefix(p, "/"), "/")
}

// Task states, as the server numbers them.
const (
	TaskPending = iota
	TaskRunning
	TaskSucceeded
	TaskCanceling
	TaskCanceled
	TaskErrored
	TaskFailing
	TaskFailed
	TaskWaitingRetry
	TaskBeforeRetry
)

// OfflineTask is one offline download as the server's task manager shows it.
// Either the download itself or the move of its result into the storage.
type OfflineTask struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State int    `json:"state"`
	// Status is what the downloader says, such as "[aria2]: downloading".
	Status string `json:"status"`
	// Progress is 0 to 100.
	Progress   float64   `json:"progress"`
	Error      string    `json:"error"`
	StartTime  time.Time `json:"start_time"`
	EndTime    time.Time `json:"end_time"`
	TotalBytes int64     `json:"total_bytes"`
	// Transfer is true for the move into the storage, false for the download.
	Transfer bool `json:"-"`
}

// Finished reports whether the task will not move on by itself.
func (t OfflineTask) Finished() bool {
	switch t.State {
	case TaskSucceeded, TaskCanceled, TaskErrored, TaskFailed:
		return true
	}
	return false
}

// Failed reports whether the task ended without finishing its work.
func (t OfflineTask) Failed() bool {
	switch t.State {
	case TaskCanceled, TaskErrored, TaskFailed:
		return true
	}
	return false
}

// offlineKinds are the task managers an offline download runs through.
var offlineKinds = []struct {
	path     string
	transfer bool
}{{"offline_download", false}, {"offline_download_transfer", true}}

// OfflineTasks lists the offline downloads the server knows, the unfinished
// ones and the finished ones, newest first. The task manager is for admins.
func (c *Client) OfflineTasks(ctx context.Context) ([]OfflineTask, error) {
	var all []OfflineTask
	for _, k := range offlineKinds {
		for _, set := range []string{"undone", "done"} {
			var list []OfflineTask
			if err := c.call(ctx, http.MethodGet, "/api/admin/task/"+k.path+"/"+set, nil, &list); err != nil {
				return nil, err
			}
			for _, t := range list {
				t.Transfer = k.transfer
				all = append(all, t)
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if fi, fj := all[i].Finished(), all[j].Finished(); fi != fj {
			return !fi
		}
		return all[i].StartTime.After(all[j].StartTime)
	})
	return all, nil
}

// OfflineTaskAction does action ("cancel", "delete" or "retry") to a task.
func (c *Client) OfflineTaskAction(ctx context.Context, t OfflineTask, action string) error {
	kind := "offline_download"
	if t.Transfer {
		kind = "offline_download_transfer"
	}
	return c.call(ctx, http.MethodPost, "/api/admin/task/"+kind+"/"+action+"?tid="+url.QueryEscape(t.ID), nil, nil)
}

// ClearOfflineDone forgets the finished offline downloads on the server:
// the downloads and the moves of their results, whether they succeeded,
// failed or were canceled.
func (c *Client) ClearOfflineDone(ctx context.Context) error {
	for _, k := range offlineKinds {
		if err := c.call(ctx, http.MethodPost, "/api/admin/task/"+k.path+"/clear_done", nil, nil); err != nil {
			return err
		}
	}
	return nil
}

// Roles of an account, as the server numbers them.
const (
	RoleGeneral = 0
	RoleGuest   = 1
	RoleAdmin   = 2
)

// User is the account a token belongs to.
type User struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Role     int    `json:"role"`
	Disabled bool   `json:"disabled"`
	BasePath string `json:"base_path"`
}

// Me asks the server who the token is: it fails with an Unauthorized error
// when the token is missing, wrong or expired, and a server that lets guests
// in answers with the guest account for no token.
func (c *Client) Me(ctx context.Context) (User, error) {
	var u User
	err := c.call(ctx, http.MethodGet, "/api/me", nil, &u)
	return u, err
}
