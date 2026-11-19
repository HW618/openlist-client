// Package theme is the OpenList client's look: the colors of the prototype in
// both appearances, its radii, spacing and type sizes, and the app's own color
// roles.
package theme

import (
	_ "embed"
	"fmt"

	"github.com/nsgo-dev/nsgo/core"
	"github.com/nsgo-dev/nsgo/ui"
)

//go:embed openlist.yaml
var source []byte

// Load parses the theme embedded in the app.
func Load() *core.Theme {
	t, err := core.ParseTheme(source)
	if err != nil {
		panic(fmt.Sprintf("theme: openlist.yaml: %v", err))
	}
	return t
}

// Radii of the prototype: controls, cards and sheets, and pills.
const (
	RadiusSM   core.Points = 8
	RadiusMD   core.Points = 12
	RadiusLG   core.Points = 18
	RadiusPill core.Points = 980
)

// Spacing scale.
const (
	Space1  core.Points = 4
	Space2  core.Points = 8
	Space3  core.Points = 12
	Space4  core.Points = 16
	Space5  core.Points = 20
	Space6  core.Points = 24
	Space8  core.Points = 32
	Space12 core.Points = 48
)

// Touch is the least size of what a finger presses.
const Touch core.Points = 44

// ContentMax is how wide the pages grow before they center on a large
// window; WideBreakpoint is the window width from which they do.
const (
	ContentMax     core.Points = 1080
	WideBreakpoint core.Points = 768
	// DialogBreakpoint is the window width from which a sheet shows as a
	// centered dialog.
	DialogBreakpoint core.Points = 640
)

// Type sizes. Line heights follow the prototype's 1.3 to 1.47.
var (
	TypeCaption = core.Style{FontSize: 12, LineHeight: 16}
	TypeSmall   = core.Style{FontSize: 14, LineHeight: 20}
	TypeBody    = core.Style{FontSize: 15, LineHeight: 20, FontWeight: ui.Medium}
	TypeTab     = core.Style{FontSize: 11, LineHeight: 14, FontWeight: ui.Medium}
	TypeTitle   = core.Style{FontSize: 20, LineHeight: 26, FontWeight: ui.Semibold}
	TypeSheet   = core.Style{FontSize: 17, LineHeight: 22, FontWeight: ui.Semibold}
)

// Colors are the colors of the theme where a scope is: ui's roles, and the
// ones the app adds. Call a method inside a StyleFunc to follow the theme and
// the appearance.
type Colors struct {
	ui.Colors
	cx *core.Scope
}

// Of is the colors where cx is.
func Of(cx *core.Scope) Colors { return Colors{ui.ColorsOf(cx), cx} }

func (c Colors) role(name string) core.Color { return c.cx.Palette().Colors[name] }

// SurfaceWarm is the fill of cards and rows, a touch off the page.
func (c Colors) SurfaceWarm() core.Color { return c.role("surfaceWarm") }

// BorderSoft is the hairline around cards and between rows.
func (c Colors) BorderSoft() core.Color { return c.role("borderSoft") }

// TextSecondary is text between the text and its muted form.
func (c Colors) TextSecondary() core.Color { return c.role("textSecondary") }

// KindColor is the color of a kind of file; "" gives the accent.
func (c Colors) KindColor(kind string) core.Color {
	switch kind {
	case "video":
		return c.role("kindVideo")
	case "audio":
		return c.role("kindAudio")
	case "image":
		return c.role("kindImage")
	case "doc":
		return c.role("kindDoc")
	}
	return c.Accent()
}
