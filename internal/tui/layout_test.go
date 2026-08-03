package tui

import "testing"

func TestComputeLayoutNarrowHidesLegend(t *testing.T) {
	// leftover = 50 - 36 - 1 = 13 < minChartColsForLegend(20): not enough
	// room left for a usable chart once the legend's fixed width is
	// reserved.
	l := computeLayout(50, 30)
	if l.showLegend {
		t.Error("showLegend = true at 50 cols, want false (not enough leftover chart space)")
	}
	if l.chartColEnd != 50 {
		t.Errorf("chartColEnd = %d, want 50 (full width when no legend)", l.chartColEnd)
	}
}

func TestComputeLayoutShowsLegendAssoonAsThereIsRoom(t *testing.T) {
	// Regression guard: an earlier version also required the terminal be
	// at least 90 columns outright, which hid the legend even when there
	// was plenty of leftover chart space after reserving the legend's own
	// width (e.g. 80 cols: leftover = 80-36-1 = 43, easily enough, but
	// 80 < 90 hid it anyway). The decision must be driven purely by
	// leftover chart space, not an extra fixed total-width floor.
	l := computeLayout(80, 30)
	if !l.showLegend {
		t.Error("showLegend = false at 80 cols, want true (43 cols left over for the chart is plenty)")
	}
}

func TestComputeLayoutWideShowsLegend(t *testing.T) {
	l := computeLayout(120, 30)
	if !l.showLegend {
		t.Fatal("showLegend = false at 120 cols, want true")
	}
	if l.legendWidth != legendWidthCols {
		t.Errorf("legendWidth = %d, want %d", l.legendWidth, legendWidthCols)
	}
	if l.chartColEnd >= l.legendCol {
		t.Errorf("chart pane (ends at %d) overlaps legend (starts at %d)", l.chartColEnd, l.legendCol)
	}
}

func TestComputeLayoutReservesTopAndBottomBars(t *testing.T) {
	l := computeLayout(100, 40)
	if l.contentTop != 1 {
		t.Errorf("contentTop = %d, want 1 (row 0 is the top hotkey bar)", l.contentTop)
	}
	if l.bottomRow != 39 {
		t.Errorf("bottomRow = %d, want 39 (last row)", l.bottomRow)
	}
	if l.contentBottom != 39 {
		t.Errorf("contentBottom = %d, want 39 (exclusive of the bottom bar)", l.contentBottom)
	}
}

func TestComputeLayoutTinyTerminalDropsBottomBar(t *testing.T) {
	l := computeLayout(40, 3)
	if l.bottomRow != -1 {
		t.Errorf("bottomRow = %d, want -1 on a 3-row terminal", l.bottomRow)
	}
}

func TestChartCellBoxSquareFromWideCells(t *testing.T) {
	// Cells twice as tall as they are wide (typical terminal): a wide,
	// short available box should shrink to a square measured in pixels.
	cols, rows := chartCellBox(100, 20, 10, 20)
	pxW := float64(cols) * 10
	pxH := float64(rows) * 20
	if diff := pxW - pxH; diff < -1 || diff > 1 {
		t.Errorf("pixel box %vx%v isn't square (cols=%d rows=%d)", pxW, pxH, cols, rows)
	}
	if cols > 100 || rows > 20 {
		t.Errorf("chartCellBox exceeded available box: got %dx%d cells, avail 100x20", cols, rows)
	}
}

func TestChartCellBoxZeroPixelSizeFallsBackToFullBox(t *testing.T) {
	cols, rows := chartCellBox(50, 20, 0, 0)
	if cols != 50 || rows != 20 {
		t.Errorf("chartCellBox with zero cell size = %dx%d, want full 50x20 fallback", cols, rows)
	}
}
