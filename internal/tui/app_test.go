package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"mempie/internal/memslice"
)

// newSimApp builds an App backed by a tcell SimulationScreen sized
// cols x rows, so drawTopBar/drawBottomBar can be exercised and their
// actual rendered cells inspected without a real terminal. Callers must
// call screen.Fini() when done (the returned function does it).
func newSimApp(t *testing.T, cols, rows int) (*App, func()) {
	t.Helper()
	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen.Init: %v", err)
	}
	screen.SetSize(cols, rows)
	a := &App{
		screen: screen,
		stack:  memslice.NewDrillStack(nil), // isPaused() reads Depth(); needs a real (non-nil) stack
	}
	return a, screen.Fini
}

func rowText(screen tcell.SimulationScreen, row, cols int) string {
	var sb strings.Builder
	for x := range cols {
		s, _, _ := screen.Get(x, row)
		sb.WriteString(s)
	}
	return sb.String()
}

func TestDrawTopBarPausedBadgeAlwaysVisibleRegardlessOfWidth(t *testing.T) {
	// Regression guard: an earlier version only drew the [PAUSED] badge
	// if the hotkey text happened to leave room for it, which silently
	// failed to show at all on any terminal narrower than ~74 columns —
	// a very ordinary width, not an edge case. The badge must be visible
	// at every width down to something reasonably small.
	for _, cols := range []int{30, 40, 60, 63, 70, 73, 74, 80, 100, 150} {
		a, done := newSimApp(t, cols, 5)
		a.manualPaused = true
		a.drawTopBar(cols)
		text := rowText(a.screen.(tcell.SimulationScreen), 0, cols)
		if !strings.Contains(text, "PAUSED") {
			t.Errorf("cols=%d: PAUSED badge not visible, row = %q", cols, text)
		}
		done()
	}
}

func TestDrawTopBarShowsLiveIndicatorWhenNotPaused(t *testing.T) {
	a, done := newSimApp(t, 80, 5)
	defer done()
	a.drawTopBar(80)
	text := rowText(a.screen.(tcell.SimulationScreen), 0, 80)
	if !strings.Contains(text, "live") {
		t.Errorf("expected a live indicator when not paused, row = %q", text)
	}
	if strings.Contains(text, "PAUSED") {
		t.Errorf("did not expect PAUSED badge when not paused, row = %q", text)
	}
}

func TestDrawTopBarLiveIndicatorAlsoAlwaysVisible(t *testing.T) {
	for _, cols := range []int{30, 40, 60, 70, 80, 150} {
		a, done := newSimApp(t, cols, 5)
		a.drawTopBar(cols)
		text := rowText(a.screen.(tcell.SimulationScreen), 0, cols)
		if !strings.Contains(text, "live") {
			t.Errorf("cols=%d: live indicator not visible, row = %q", cols, text)
		}
		done()
	}
}

func TestDrawTopBarIndicatorNeverOverlapsHotkeyText(t *testing.T) {
	// The badge/indicator is drawn into its own reserved column range;
	// hotkey text must be clipped before it, never drawn on top of it.
	a, done := newSimApp(t, 70, 5)
	defer done()
	a.manualPaused = true
	a.drawTopBar(70)
	text := rowText(a.screen.(tcell.SimulationScreen), 0, 70)
	if !strings.HasSuffix(strings.TrimRight(text, " "), "[PAUSED]") {
		t.Errorf("expected the badge to be the trailing content on the row, got %q", text)
	}
}

func testLegendSlices() []memslice.Slice {
	return []memslice.Slice{
		{Label: "Free", Bytes: 1000, Kind: memslice.KindCategory},
		{Label: "Cache", Bytes: 500, Kind: memslice.KindCategory},
		{Label: "brave [123]", Bytes: 200, Kind: memslice.KindProcess},
	}
}

func TestDrawLegendHasTopAndBottomRowPadding(t *testing.T) {
	// The legend pane should get the same kind of breathing room the
	// pie chart gets from its own marginPx: entries shouldn't start on
	// the very first content row (butting the top bar) or extend onto
	// the very last content row (butting the bottom bar).
	cols, rows := 120, 20
	a, done := newSimApp(t, cols, rows)
	defer done()

	slices := testLegendSlices()
	colors := sliceColors(slices)
	l := computeLayout(cols, rows)
	if !l.showLegend {
		t.Fatal("expected legend to be shown at 120 cols")
	}
	a.drawLegend(l, slices, colors)

	screen := a.screen.(tcell.SimulationScreen)
	topRow := rowText(screen, l.contentTop, cols)[l.legendCol:]
	if strings.TrimSpace(topRow) != "" {
		t.Errorf("expected the legend's first content row (%d) to be blank padding, got %q", l.contentTop, topRow)
	}
	bottomRow := rowText(screen, l.contentBottom-1, cols)[l.legendCol:]
	if strings.TrimSpace(bottomRow) != "" {
		t.Errorf("expected the legend's last content row (%d) to be blank padding, got %q", l.contentBottom-1, bottomRow)
	}
}

func TestDrawLegendHasLeftAndRightColumnPadding(t *testing.T) {
	cols, rows := 120, 20
	a, done := newSimApp(t, cols, rows)
	defer done()

	slices := testLegendSlices()
	colors := sliceColors(slices)
	l := computeLayout(cols, rows)
	if !l.showLegend {
		t.Fatal("expected legend to be shown at 120 cols")
	}
	a.drawLegend(l, slices, colors)

	screen := a.screen.(tcell.SimulationScreen)
	// The first row an entry actually gets drawn on (contentTop+padding).
	entryRow := l.contentTop + legendPadRows

	leftEdge, _, _ := screen.Get(l.legendCol, entryRow)
	if leftEdge != " " {
		t.Errorf("expected the legend's leftmost column to be blank padding, got %q", leftEdge)
	}
	rightEdge, _, _ := screen.Get(l.legendCol+l.legendWidth-1, entryRow)
	if rightEdge != " " {
		t.Errorf("expected the legend's rightmost column to be blank padding, got %q", rightEdge)
	}
}
