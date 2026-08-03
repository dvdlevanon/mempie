package tui

// legendWidthCols is the fixed column width of the legend pane when shown.
const legendWidthCols = 36

// minChartColsForLegend is the smallest leftover chart width (after
// reserving legendWidthCols + a 1-column gap) still considered worth
// keeping the legend around for. The legend/tooltip decision is driven
// purely by this — "is there still enough room for a usable chart" — not
// by any separate minimum on the terminal's total width: an earlier
// version additionally required the terminal be at least 90 columns
// wide outright, which hid the legend even on terminals with plenty of
// leftover chart space once the legend's own width was subtracted (e.g.
// 80 columns: 80-36-1 = 43 leftover, easily enough, but 80 < 90 hid it
// anyway). That extra check added nothing this one doesn't already
// cover, so it's gone.
const minChartColsForLegend = 20

// layout is one frame's computed screen regions, derived fresh from the
// terminal's current cell dimensions on every redraw (mempie does no
// layout caching between frames — terminal resizes are the common case
// this needs to handle correctly, and recomputing is cheap).
type layout struct {
	showLegend  bool
	legendCol   int // first column of the legend pane (only if showLegend)
	legendWidth int

	chartColStart, chartColEnd int // chart pane's column range [start, end)
	contentTop, contentBottom  int // usable row range [top, bottom) for chart/legend

	bottomRow int // row index of the bottom status bar, or -1 if no room
}

// computeLayout derives a layout from the terminal's current size in
// character cells. Row 0 is always the top hotkey bar; the last row is
// the bottom status bar (dropped only if the terminal is too short to
// spare it).
func computeLayout(cols, rows int) layout {
	l := layout{}

	l.contentTop = 1
	if rows >= 4 {
		l.bottomRow = rows - 1
		l.contentBottom = rows - 1
	} else {
		l.bottomRow = -1
		l.contentBottom = rows
	}
	if l.contentBottom < l.contentTop {
		l.contentBottom = l.contentTop
	}

	if cols-legendWidthCols-1 >= minChartColsForLegend {
		l.showLegend = true
		l.legendWidth = legendWidthCols
		l.legendCol = cols - legendWidthCols
		l.chartColStart = 0
		l.chartColEnd = l.legendCol - 1 // leave a 1-column gap before the legend
	} else {
		l.chartColStart = 0
		l.chartColEnd = cols
	}
	if l.chartColEnd < l.chartColStart {
		l.chartColEnd = l.chartColStart
	}

	return l
}

// chartCellBox picks the largest square-pixel region (in character cells)
// that fits within an availCols x availRows box, given how many pixels
// wide/tall each cell is. Terminal cells are usually taller than they are
// wide, so a naive "use every available cell" would draw an oval, not a
// circle — this picks whichever of "full width" or "full height" yields a
// pixel-square box without overflowing the other dimension.
func chartCellBox(availCols, availRows int, cellW, cellH float64) (cols, rows int) {
	if availCols <= 0 || availRows <= 0 {
		return 0, 0
	}
	if cellW <= 0 || cellH <= 0 {
		return availCols, availRows
	}

	rowsForFullWidth := int(float64(availCols) * cellW / cellH)
	if rowsForFullWidth <= availRows {
		return availCols, max(rowsForFullWidth, 1)
	}
	colsForFullHeight := int(float64(availRows) * cellH / cellW)
	return max(colsForFullHeight, 1), availRows
}
