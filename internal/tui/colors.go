package tui

import (
	"image/color"
	"math"

	"github.com/gdamore/tcell/v2"

	"mempie/internal/memslice"
)

// categoryHSL fixes each fixed /proc/meminfo category's sticky color, by
// name — a category's color depends only on its label, never on its rank
// position, so "Free" and "Cache" always look the same across every
// refresh and every drill level. This is what sticky colors means for the
// things a viewer actually builds a mental map against over a session, as
// opposed to individual processes, which have no comparable stable
// identity (a given comm+pid combination never recurs, and PIDs get
// reused) — see processPalette below.
//
// Hand-tuned per category rather than an evenly-spaced hue wheel indexed
// by list position: an earlier version did the latter, which arbitrarily
// landed "Free" on a bold red — a jarring, alarm-like color for what's
// actually the calmest possible category (unallocated RAM). Hues below
// are chosen for a calmer, more cohesive "modern dashboard" feel (lower
// saturation than a raw rainbow) and so a category's color has at least a
// loose semantic fit: Free reads as a calm teal-green, Cache classic
// blue, the two Slab tiers share a violet family (reclaimable lighter,
// unreclaimable darker) since they're conceptually related, etc.
var categoryHSL = map[string][3]float64{
	"Free":                 {165, 0.45, 0.50},
	"Cache":                {210, 0.55, 0.55},
	"Shmem":                {190, 0.50, 0.55},
	"Slab (reclaimable)":   {260, 0.40, 0.62},
	"Slab (unreclaimable)": {265, 0.45, 0.45},
	"KernelStack":          {30, 0.55, 0.55},
	"PageTables":           {45, 0.55, 0.55},
	"Percpu":               {330, 0.45, 0.60},
	"VmallocUsed":          {5, 0.55, 0.55},
	"HugePages":            {235, 0.45, 0.50},
	"SwapCached":           {55, 0.50, 0.58},
}

// categoryColors maps each category label to its sticky color.
var categoryColors = buildCategoryColors()

func buildCategoryColors() map[string]color.RGBA {
	m := make(map[string]color.RGBA, len(categoryHSL))
	for label, hsl := range categoryHSL {
		m[label] = hslToRGBA(hsl[0], hsl[1], hsl[2])
	}
	return m
}

// remainderColor is Remainder's own fixed, sticky, neutral color — never
// reused for anything else. Remainder is, in its own way, as much of a
// "constant" as a fixed category: there's always exactly zero or one per
// drill level, even though its exact contents differ every time.
var remainderColor = color.RGBA{132, 132, 142, 255}

// processPaletteSize is how many distinct rotating hues process slices
// cycle through. Offset from the category wheel (by half a hue step) and
// deliberately desaturated, so a process slice never gets confused with a
// category's bold, sticky color even when the two land on similar hues.
const processPaletteSize = 8

var processPalette = buildProcessPalette()

func buildProcessPalette() [processPaletteSize]color.RGBA {
	var p [processPaletteSize]color.RGBA
	for i := range processPaletteSize {
		hue := float64(i)*360/processPaletteSize + 180.0/processPaletteSize
		p[i] = hslToRGBA(hue, 0.38, 0.60)
	}
	return p
}

// sliceColors assigns each slice in a view its display color: a sticky,
// name-keyed color for a fixed category or Remainder, or the next color
// in the rotating process palette (by position among *process* slices
// specifically, not overall position in the view) for a process slice.
// Computed once per redraw and shared by the chart and legend so the two
// always agree.
func sliceColors(slices []memslice.Slice) []color.RGBA {
	colors := make([]color.RGBA, len(slices))
	processRank := 0
	for i, s := range slices {
		switch s.Kind {
		case memslice.KindCategory:
			if c, ok := categoryColors[s.Label]; ok {
				colors[i] = c
			} else {
				colors[i] = remainderColor // shouldn't happen; a safe, visible fallback
			}
		case memslice.KindRemainder:
			colors[i] = remainderColor
		default: // KindProcess
			colors[i] = processPalette[processRank%processPaletteSize]
			processRank++
		}
	}
	return colors
}

// hslToRGBA converts an HSL color (h in degrees [0,360), s and l in
// [0,1]) to an opaque RGBA.
func hslToRGBA(h, s, l float64) color.RGBA {
	c := (1 - math.Abs(2*l-1)) * s
	hp := h / 60
	x := c * (1 - math.Abs(math.Mod(hp, 2)-1))
	var r, g, b float64
	switch {
	case hp < 1:
		r, g, b = c, x, 0
	case hp < 2:
		r, g, b = x, c, 0
	case hp < 3:
		r, g, b = 0, c, x
	case hp < 4:
		r, g, b = 0, x, c
	case hp < 5:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	m := l - c/2
	return color.RGBA{
		R: uint8((r + m) * 255),
		G: uint8((g + m) * 255),
		B: uint8((b + m) * 255),
		A: 255,
	}
}

// chartSelected is the outline color drawn around the currently
// highlighted wedge — chosen to contrast against every wedge color, not
// just some.
var chartSelected = color.RGBA{255, 255, 255, 255}

// tcell styles for the top/bottom bars, legend, and help dialog.
var (
	statusLabel  = tcell.StyleDefault.Foreground(tcell.NewRGBColor(150, 150, 160))
	statusValue  = tcell.StyleDefault.Bold(true)
	statusHotkey = tcell.StyleDefault.Foreground(tcell.NewRGBColor(186, 120, 255)).Bold(true)

	pausedBadgeStyle = tcell.StyleDefault.
				Foreground(tcell.NewRGBColor(20, 20, 20)).
				Background(tcell.NewRGBColor(240, 190, 40)).
				Bold(true)
	// liveBadgeStyle is deliberately quieter than pausedBadgeStyle's
	// reverse-video badge — paused is the state worth interrupting for;
	// live is just the default, so it only needs to be legible, not loud.
	liveBadgeStyle = tcell.StyleDefault.Foreground(tcell.NewRGBColor(90, 210, 140)).Bold(true)

	legendLabelStyle    = tcell.StyleDefault.Foreground(tcell.NewRGBColor(220, 220, 220))
	legendSizeStyle     = tcell.StyleDefault.Foreground(tcell.NewRGBColor(150, 150, 160))
	legendSelectedStyle = tcell.StyleDefault.Bold(true).Reverse(true)

	helpBorderStyle = tcell.StyleDefault.Foreground(tcell.NewRGBColor(186, 120, 255))
	helpTitleStyle  = tcell.StyleDefault.Bold(true)
	helpKeyStyle    = tcell.StyleDefault.Foreground(tcell.NewRGBColor(186, 120, 255)).Bold(true)
	helpDescStyle   = tcell.StyleDefault.Foreground(tcell.NewRGBColor(210, 210, 210))
)

// tcellColorFromRGBA mirrors a piechart color.RGBA as a tcell color, so a
// legend swatch (drawn as a colored terminal cell) matches its wedge
// exactly.
func tcellColorFromRGBA(c color.RGBA) tcell.Color {
	return tcell.NewRGBColor(int32(c.R), int32(c.G), int32(c.B))
}
