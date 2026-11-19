package browse

import "math"

// The floating button's geometry, in points, as the prototype draws it.
const (
	// FabSize is the width and height of the main button.
	FabSize float32 = 52
	// MiniSize is the width and height of a menu button.
	MiniSize float32 = 44
	// EdgeThreshold is how near a side the button is let go to dock there.
	EdgeThreshold float32 = 36
	// DockInset is how far from the side a docked button rests.
	DockInset float32 = 12
	// DockVisible is the share of a docked button that stays in view.
	DockVisible float32 = 0.2

	sideMargin   float32 = 8
	dragTop      float32 = 58
	dragBottom   float32 = 64
	menuTop      float32 = 56
	menuBottom   float32 = 62
	menuGapY     float32 = 10
	menuGapX     float32 = 12
	menuItemH    float32 = MiniSize
	menuGapToFab float32 = 10
)

// Side is a side of the screen the button docks to.
type Side uint8

const (
	// SideNone is a button that floats free.
	SideNone Side = iota
	SideLeft
	SideRight
)

// Pos is the top left corner of the button in its container.
type Pos struct{ X, Y float32 }

// Home is where the button starts: at the right, above the tab bar.
func Home(w, h float32) Pos { return Clamp(Pos{X: w - FabSize - DockInset, Y: h - FabSize - 70}, w, h) }

// Clamp keeps the button inside the container's w by h, clear of its sides
// and of the header and tab bar.
func Clamp(p Pos, w, h float32) Pos {
	p.X = clamp(p.X, sideMargin, w-FabSize-sideMargin)
	p.Y = clamp(p.Y, dragTop, h-FabSize-dragBottom)
	return p
}

func clamp(v, lo, hi float32) float32 {
	if hi < lo {
		return lo
	}
	return float32(math.Max(float64(lo), math.Min(float64(hi), float64(v))))
}

// Release says where a button let go at p settles in a container w wide: it
// docks only when it is within EdgeThreshold of a side, resting DockInset
// from it; elsewhere it stays where it is.
func Release(p Pos, w float32) (Pos, Side) {
	left := p.X
	right := w - (p.X + FabSize)
	switch {
	case left <= EdgeThreshold:
		p.X = DockInset
		return p, SideLeft
	case right <= EdgeThreshold:
		p.X = w - FabSize - DockInset
		return p, SideRight
	}
	return p, SideNone
}

// Docked is the offset along x that tucks a docked button into its side so
// DockVisible of it stays in view; zero when it floats.
func Docked(p Pos, side Side, w float32) float32 {
	switch side {
	case SideLeft:
		return -(p.X + FabSize*(1-DockVisible))
	case SideRight:
		return w - p.X - FabSize*DockVisible
	}
	return 0
}

// Menu is where the menu of the button goes and how it is laid out.
type Menu struct {
	// Below is set when the menu opens under the button.
	Below bool
	// Rows and Cols arrange the items in columns that fill top down.
	Rows, Cols int
	// Left and Top are the menu's corner in the container; Width and Height
	// its size.
	Left, Top     float32
	Width, Height float32
	// ItemWidth is the width of each column.
	ItemWidth float32
}

// LayoutMenu places n items of itemW points wide for a button at p in a
// container w by h. It opens upward unless there is no room, uses as many
// columns as the room's height asks for, aligns with the button's side and
// shifts to stay clear of the container's sides.
func LayoutMenu(p Pos, w, h float32, n int, itemW float32) Menu {
	if n < 1 {
		n = 1
	}
	above := p.Y - menuTop - menuGapToFab
	below := h - menuBottom - (p.Y + FabSize) - menuGapToFab
	m := Menu{Below: above < 54 && below > above, ItemWidth: itemW}
	avail := above
	if m.Below {
		avail = below
	}
	avail = float32(math.Max(54, float64(avail)))
	maxRows := int(math.Max(1, math.Floor(float64((avail+menuGapY)/(menuItemH+menuGapY)))))
	m.Cols = int(math.Ceil(float64(n) / float64(maxRows)))
	// A menu never grows wider than the container; it gets taller instead.
	fit := int(math.Max(1, math.Floor(float64((w-2*sideMargin+menuGapX)/(itemW+menuGapX)))))
	m.Cols = min(m.Cols, fit)
	m.Rows = int(math.Ceil(float64(n) / float64(m.Cols)))
	m.Width = float32(m.Cols)*itemW + float32(m.Cols-1)*menuGapX
	m.Height = float32(m.Rows)*menuItemH + float32(m.Rows-1)*menuGapY
	if m.Below {
		m.Top = p.Y + FabSize + menuGapToFab
	} else {
		m.Top = p.Y - menuGapToFab - m.Height
	}
	if p.X+FabSize/2 < w/2 {
		m.Left = p.X
	} else {
		m.Left = p.X + FabSize - m.Width
	}
	m.Left = clamp(m.Left, sideMargin, w-sideMargin-m.Width)
	return m
}

// ItemWidth estimates the width of a menu button with its label: the label's
// text, its padding, the gap and the round button. Wide characters count
// double.
func ItemWidth(labels ...string) float32 {
	var widest float32
	for _, l := range labels {
		var wd float32 = 24
		for _, r := range l {
			if r >= 0x2E80 {
				wd += 13
			} else {
				wd += 7
			}
		}
		if wd > widest {
			widest = wd
		}
	}
	return widest + 10 + MiniSize
}
