package app

import (
	"context"
	"io"
	"net/http"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/icons/lucide"
	"github.com/nsgo-dev/nsgo/ui"

	"github.com/HW618/openlist-client/src/internal/browse"
	"github.com/HW618/openlist-client/src/internal/openlist"
	"github.com/HW618/openlist-client/src/locales"
	"github.com/HW618/openlist-client/src/theme"
)

// maxTextPreview is the most bytes of a text file the preview reads.
const maxTextPreview = 1 << 20

// previewReq says which file to preview, with the client to read it.
type previewReq struct {
	Client *openlist.Client
	Item   browse.Item
}

// fetchPreview reads what the preview shows of a file: its address, and its
// text when it is a small text file.
func fetchPreview(ctx context.Context, r previewReq) (previewData, error) {
	obj, err := r.Client.Get(ctx, r.Item.Path)
	if err != nil {
		return previewData{}, err
	}
	d := previewData{Object: obj}
	if browse.Previewable(r.Item.Name) != "text" {
		return d, nil
	}
	if obj.Size > maxTextPreview {
		d.Large = true
		return d, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, obj.RawURL, nil)
	if err != nil {
		return d, err
	}
	if sameHost(r.Client.BaseURL, obj.RawURL) && r.Client.Token != "" {
		req.Header.Set("Authorization", r.Client.Token)
	}
	resp, err := downloadClient.Do(req)
	if err != nil {
		return d, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return d, &openlist.APIError{Code: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxTextPreview+1))
	if err != nil {
		return d, err
	}
	if len(b) > maxTextPreview {
		d.Large = true
		return d, nil
	}
	d.Text = string(b)
	return d, nil
}

// openPreview shows a file.
func (a *App) openPreview(it browse.Item) {
	a.preview.Set(&it)
}

func (a *App) closePreview() { a.preview.Set(nil) }

// previewLayer covers the app with the preview of the open file.
func (a *App) previewLayer(cx *core.Scope) core.Node {
	return core.Show{When: func() bool { return a.preview.Get() != nil }, Then: func(cx *core.Scope) core.Node {
		var it browse.Item
		cx.Untrack(func() {
			if p := a.preview.Get(); p != nil {
				it = *p
			}
		})
		return a.previewView(cx, it)
	}}
}

func (a *App) previewView(cx *core.Scope, it browse.Item) core.Node {
	th := theme.Of(cx)
	kind := browse.Previewable(it.Name)
	state := func() string {
		switch {
		case a.previewRes.Loading():
			if _, ok := a.previewRes.Get(); !ok {
				return "loading"
			}
		case a.previewRes.Err() != nil:
			return "error"
		}
		if _, ok := a.previewRes.Get(); !ok {
			return "loading"
		}
		if kind == "" {
			return "unsupported"
		}
		return kind
	}
	data := func() previewData { d, _ := a.previewRes.Get(); return d }
	url := func() string { return data().Object.RawURL }
	show := func(k string, build func(cx *core.Scope) core.Node) core.Node {
		return core.Show{When: func() bool { return state() == k }, Then: build}
	}
	fill := core.Style{Width: core.Pct(100), Height: core.Pct(100)}
	body := core.View{Style: fillStyle.Merge(core.Style{AlignItems: core.AlignCenter, JustifyContent: core.JustifyCenter}), Children: []core.Node{
		show("loading", func(cx *core.Scope) core.Node { return loadingView(cx, locales.Loading(cx)) }),
		show("error", func(cx *core.Scope) core.Node {
			return emptyStateFunc(cx, lucide.CircleAlert, locales.PreviewLoadFailed(cx), func() string {
				if e := a.previewRes.Err(); e != nil {
					return errText(e)
				}
				return ""
			}, ui.Button{Text: locales.Retry(cx), Variant: ui.ButtonSecondary, OnPress: a.previewRes.Refetch})
		}),
		show("unsupported", func(cx *core.Scope) core.Node {
			return emptyState(cx, kindIcon(it.Kind), it.Name, locales.PreviewUnsupported(cx),
				ui.Button{Text: locales.DownloadToDevice(cx), Icon: lucide.Download, OnPress: func() { a.download([]browse.Item{it}) }})
		}),
		show("image", func(cx *core.Scope) core.Node {
			return core.Image{SourceFunc: func() core.ImageSource { return core.Src(url()) }, ResizeMode: core.Contain, Label: it.Name, Style: fill}
		}),
		show("video", func(cx *core.Scope) core.Node {
			return core.Video{SourceFunc: url, Controls: true, ResizeMode: core.Contain, Label: locales.PreviewVideo(cx), Style: fill, TestID: "preview-video"}
		}),
		show("audio", func(cx *core.Scope) core.Node {
			return core.View{Style: core.Style{Width: core.Pct(100), AlignItems: core.AlignCenter, Gap: 16, Padding: core.All(theme.Space6)}, Children: []core.Node{
				tile(cx, lucide.Music, "audio", 96, 24),
				core.Video{SourceFunc: url, Controls: true, Label: locales.PreviewAudio(cx), Style: core.Style{Width: core.Pct(100), Height: core.Pt(54)}, TestID: "preview-audio"},
			}}
		}),
		show("text", func(cx *core.Scope) core.Node {
			return core.Show{When: func() bool { return data().Large }, Then: func(cx *core.Scope) core.Node {
				return emptyState(cx, lucide.FileText, it.Name, locales.PreviewTooLarge(cx),
					ui.Button{Text: locales.DownloadToDevice(cx), Icon: lucide.Download, OnPress: func() { a.download([]browse.Item{it}) }})
			}, Else: func(cx *core.Scope) core.Node {
				return core.Scroll{Style: fill, Children: []core.Node{
					core.Text{ContentFunc: func() string { return data().Text }, Selectable: core.SelectableOn,
						Style:     core.Style{FontFamily: "monospace", FontSize: 13, LineHeight: 19, Padding: core.All(theme.Space4)},
						StyleFunc: func() core.Style { return core.Style{Color: th.Text()} }}}}
			}}
		}),
	}}
	info := core.View{Style: core.Style{Gap: 10, Padding: core.Symmetric(12, theme.Space4), Borders: core.Edges{Top: 1},
		PaddingEnv: core.EnvEdges{Bottom: core.EnvSafeArea}},
		StyleFunc: func() core.Style {
			return core.Style{Background: th.Surface(), BorderColors: core.EdgeColors{Top: th.BorderSoft()}}
		},
		Children: []core.Node{
			row(16,
				txt(cx, locales.FileSize(cx)+" "+browse.Size(it.Size), theme.TypeCaption, cMuted),
				txt(cx, locales.FileModified(cx)+" "+browse.Date(it.Modified), theme.TypeCaption, cMuted)),
			buttons(
				ui.Button{Text: locales.CopyDirectLink(cx), Icon: lucide.Link, Variant: ui.ButtonSecondary, Flex: 1, TestID: "preview-copy",
					OnPress: func() { a.copyLinks([]browse.Item{it}) }},
				ui.Button{Text: locales.DownloadToDevice(cx), Icon: lucide.Download, Flex: 1, TestID: "preview-download",
					OnPress: func() { a.download([]browse.Item{it}) }}),
		}}
	return core.View{
		Role: core.RoleGroup, Label: locales.PreviewLabel(cx), TestID: "preview",
		Style:     core.Style{Position: core.Absolute, Left: core.Pt(0), Top: core.Pt(0), Right: core.Pt(0), Bottom: core.Pt(0), ZIndex: 50},
		StyleFunc: func() core.Style { return core.Style{Background: th.Background()} },
		Children: []core.Node{page(cx,
			header(cx,
				iconButton(lucide.ArrowLeft, locales.PreviewBackLabel(cx), a.closePreview),
				core.View{Style: fillStyle, Children: []core.Node{oneLine(txt(cx, it.Name, theme.TypeSheet, cText))}},
				iconButton(lucide.Download, locales.PreviewDownloadLabel(cx), func() { a.download([]browse.Item{it}) })),
			body, info)},
	}
}
