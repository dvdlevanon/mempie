// Package tui wires together /proc collection (internal/meminfo,
// internal/procpss), the ranking/drill-stack logic (internal/memslice),
// and the kitty-graphics pie chart (internal/piechart) into an interactive
// terminal app built on tcell for input handling and cell-based layout.
//
// State discipline: App's fields are only ever read or mutated from the
// single goroutine running Run's event loop. The one background goroutine
// this package starts (in triggerRefresh) does no more than call
// collectSlices and send the result on a channel — it never touches App
// itself. This mirrors this author's other TUI tools (deskwatch, diskmon):
// if you find yourself wanting a mutex here, something's architecturally
// off.
package tui

import (
	"fmt"
	"image/color"
	"strconv"
	"time"

	"github.com/gdamore/tcell/v2"

	"mempie/internal/kittygfx"
	"mempie/internal/memslice"
	"mempie/internal/piechart"
)

// Each redraw double-buffers between two kitty image ids, alternating
// which one is (re)transmitted each frame — see diskmon's app.go for the
// full rationale: deleting-then-retransmitting a *visible* id leaves a
// brief real gap with nothing displayed, which reads as flicker. Since
// mempie shows exactly one chart (not two side by side), it only needs one
// pair of ids.
var chartImageIDs = [2]uint32{1, 2}

// App is the running mempie TUI.
type App struct {
	screen tcell.Screen
	kw     *kittygfx.Writer

	refreshInterval time.Duration

	baseSlices []memslice.Slice // full flat list from the most recent completed refresh, ungrouped
	totalBytes uint64           // MemTotal
	usedBytes  uint64           // MemTotal - MemFree
	stack      *memslice.DrillStack
	lastErr    error // most recent collection error, if any, shown in the bottom status bar

	manualPaused bool
	refreshing   bool // a background collectSlices call is in flight
	groupByExec  bool // fold same-executable process slices into one — see resetTopLevel
	showHelp     bool // '?' dialog listing all hotkeys is open

	selected int // index into the current view's All() slices
	frame    int // toggles which kitty image id is targeted this redraw

	refreshResults chan collectResult

	quit bool
}

// NewApp constructs the TUI and takes over the terminal. kw must already
// be open (see kittygfx.OpenTTYWriter); mempie has no non-kitty fallback,
// so the caller is expected to have already confirmed kitty support (via
// kittygfx.Detect) and failed the whole program otherwise.
func NewApp(kw *kittygfx.Writer, refreshInterval time.Duration) (*App, error) {
	screen, err := tcell.NewScreen()
	if err != nil {
		return nil, fmt.Errorf("tui: new screen: %w", err)
	}
	if err := screen.Init(); err != nil {
		return nil, fmt.Errorf("tui: init screen: %w", err)
	}
	screen.HideCursor()

	return &App{
		screen:          screen,
		kw:              kw,
		refreshInterval: refreshInterval,
		stack:           memslice.NewDrillStack(nil),
		refreshResults:  make(chan collectResult, 1),
		// On by default: a flat per-PID view of a many-process machine is
		// mostly noise (dozens of near-identical browser/renderer
		// entries) — grouped is the more immediately useful default, and
		// `e` still toggles it off for anyone who wants raw per-PID
		// detail.
		groupByExec: true,
	}, nil
}

// Close tears down the kitty image placements and restores the terminal.
func (a *App) Close() {
	for _, id := range chartImageIDs {
		_ = a.kw.Delete(id)
	}
	a.screen.Fini()
}

// Run performs the first (synchronous) collection, then blocks handling
// input and periodic refreshes until the user quits.
func (a *App) Run() error {
	defer a.Close()

	if res := collectSlices(); res.err != nil {
		a.lastErr = res.err
	} else {
		a.baseSlices = res.slices
		a.totalBytes = res.totalBytes
		a.usedBytes = res.usedBytes
		a.resetTopLevel()
	}
	a.redraw()

	events := make(chan tcell.Event, 8)
	go func() {
		for {
			ev := a.screen.PollEvent()
			if ev == nil {
				return // screen was finalized
			}
			events <- ev
		}
	}()

	ticker := time.NewTicker(a.refreshInterval)
	defer ticker.Stop()

	for !a.quit {
		select {
		case ev := <-events:
			a.handleEvent(ev)
		case <-ticker.C:
			a.maybeTriggerRefresh()
		case res := <-a.refreshResults:
			a.applyRefresh(res)
		}
		if a.quit {
			break
		}
		a.redraw()
	}
	return nil
}

// resetTopLevel rebuilds the depth-0 view from a.baseSlices, applying the
// group-by-exec transform first if it's currently toggled on. Used both
// after a live refresh and when the 'e' toggle itself changes — either
// way, the drill stack collapses back to depth 0, since a stale drilled-in
// Remainder's children belong to whichever grouping was in effect when it
// was computed and stop matching once that changes.
func (a *App) resetTopLevel() {
	effective := a.baseSlices
	if a.groupByExec {
		effective = memslice.GroupProcessesByExec(a.baseSlices)
	}
	a.stack.Reset(effective)
	a.clampSelected()
}

// isPaused reports whether live refresh is currently suppressed: either
// the user manually paused, or — regardless of the manual toggle — the
// view is drilled into a Remainder at all. Drilling always forces paused;
// only returning all the way to depth 0 (see popDrill) re-arms the manual
// toggle's effect.
func (a *App) isPaused() bool {
	return a.manualPaused || a.stack.Depth() > 0
}

// maybeTriggerRefresh kicks off a background collectSlices call if one
// isn't already in flight and refresh isn't currently paused. The result
// arrives later via a.refreshResults and is applied in applyRefresh.
func (a *App) maybeTriggerRefresh() {
	if a.isPaused() || a.refreshing {
		return
	}
	a.refreshing = true
	go func() {
		a.refreshResults <- collectSlices()
	}()
}

// applyRefresh incorporates a completed background collection. If the
// user drilled into a Remainder while the collection was in flight (a
// race between the 10s ticker firing and the user pressing Enter), the
// now-stale full-refresh result is discarded rather than clobbering their
// frozen, drilled-in view — the next tick at depth 0 will fetch a fresh
// one anyway.
func (a *App) applyRefresh(res collectResult) {
	a.refreshing = false
	if res.err != nil {
		a.lastErr = res.err
		return
	}
	a.lastErr = nil
	a.baseSlices = res.slices
	a.totalBytes = res.totalBytes
	a.usedBytes = res.usedBytes
	if a.stack.Depth() == 0 {
		a.resetTopLevel()
	}
}

func (a *App) handleEvent(ev tcell.Event) {
	switch e := ev.(type) {
	case *tcell.EventResize:
		a.screen.Sync()
	case *tcell.EventKey:
		a.handleKey(e)
	}
}

func (a *App) handleKey(e *tcell.EventKey) {
	if e.Key() == tcell.KeyCtrlC {
		a.quit = true
		return
	}
	if a.showHelp {
		// The help dialog is a simple modal: any other key dismisses it
		// rather than acting on the chart underneath, so there's no need
		// to reason about which keys should "pass through" a dialog the
		// user just asked to see.
		a.toggleHelp()
		return
	}

	switch e.Key() {
	case tcell.KeyLeft, tcell.KeyUp:
		a.moveSelection(-1)
		return
	case tcell.KeyRight, tcell.KeyDown:
		a.moveSelection(1)
		return
	case tcell.KeyEnter:
		a.activateSelection()
		return
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		a.popDrill()
		return
	}
	switch e.Rune() {
	case 'q', 'Q':
		a.quit = true
	case ' ':
		a.manualPaused = !a.manualPaused
	case 'b':
		a.popDrill()
	case '?':
		a.toggleHelp()
	case 'e', 'E':
		a.toggleGroupByExec()
	}
}

// toggleGroupByExec flips whether process slices are folded together by
// executable path (see internal/memslice.GroupProcessesByExec) and
// rebuilds the top-level view accordingly — always collapsing back to
// depth 0, since a Remainder drilled into under the old grouping mode no
// longer corresponds to anything meaningful under the new one.
func (a *App) toggleGroupByExec() {
	a.groupByExec = !a.groupByExec
	a.resetTopLevel()
}

// toggleHelp opens or closes the '?' hotkey-reference dialog. Opening it
// deletes the chart's kitty image placements outright (redraw skips
// re-transmitting them while the dialog is open, see redraw) so the old
// chart frame doesn't linger visible underneath/around the dialog box —
// tcell text is drawn fresh each frame via screen.Clear(), but a kitty
// graphics placement is a separate, independent layer that Clear() has no
// power over.
func (a *App) toggleHelp() {
	a.showHelp = !a.showHelp
	if a.showHelp {
		for _, id := range chartImageIDs {
			_ = a.kw.Delete(id)
		}
	}
}

func (a *App) currentSlices() []memslice.Slice {
	return a.stack.Current().All()
}

func (a *App) clampSelected() {
	n := len(a.currentSlices())
	switch {
	case n == 0:
		a.selected = 0
	case a.selected >= n:
		a.selected = n - 1
	case a.selected < 0:
		a.selected = 0
	}
}

func (a *App) moveSelection(delta int) {
	n := len(a.currentSlices())
	if n == 0 {
		return
	}
	a.selected = ((a.selected+delta)%n + n) % n
}

// activateSelection handles Enter on the currently selected slice: a
// no-op unless it's the Remainder, in which case it drills in and forces
// a pause (see isPaused). Selection resets to the largest slice in the
// freshly-drilled view, matching the reset done everywhere else the view
// changes.
func (a *App) activateSelection() {
	slices := a.currentSlices()
	if a.selected < 0 || a.selected >= len(slices) {
		return
	}
	s := slices[a.selected]
	if s.Kind != memslice.KindRemainder {
		return // processes and categories are leaves in v1
	}
	if a.stack.DrillInto(s) {
		a.selected = 0
	}
}

// popDrill backs out one drill level. Per spec, reaching depth 0 this way
// auto-resumes live refresh regardless of the manual pause toggle's prior
// state — the toggle only starts mattering again once the user is back at
// the top level.
func (a *App) popDrill() {
	if !a.stack.Back() {
		return
	}
	a.selected = 0
	if a.stack.Depth() == 0 {
		a.manualPaused = false
	}
}

// redraw renders one full frame: the top hotkey bar, then either the '?'
// help dialog (which takes over the whole content area while open) or
// the normal view — the pie chart (via kitty graphics), the legend (if
// the terminal is wide enough), and the bottom status bar.
func (a *App) redraw() {
	cols, rows := a.screen.Size()
	a.screen.Clear()

	a.drawTopBar(cols)

	if a.showHelp {
		a.drawHelpDialog(cols, rows)
		a.screen.Show()
		return
	}

	view := a.stack.Current()
	slices := view.All()
	a.clampSelected()

	// Computed once and shared by the chart and legend, so a slice's
	// swatch always matches its wedge exactly (see sliceColors — sticky
	// by name for categories/Remainder, rotating for processes).
	colors := sliceColors(slices)

	l := computeLayout(cols, rows)
	availCols := l.chartColEnd - l.chartColStart
	availRows := l.contentBottom - l.contentTop
	if availCols > 0 && availRows > 0 {
		a.drawChart(l, slices, colors)
	}
	if l.showLegend {
		a.drawLegend(l, slices, colors)
	}
	if l.bottomRow >= 0 {
		a.drawBottomBar(l.bottomRow, cols, len(slices))
	}

	a.screen.Show()
}

// drawChart rasterizes the pie via internal/piechart and transmits it
// with the kitty graphics protocol, sized to the largest square that fits
// the chart pane's available cells.
func (a *App) drawChart(l layout, slices []memslice.Slice, colors []color.RGBA) {
	availCols := l.chartColEnd - l.chartColStart
	availRows := l.contentBottom - l.contentTop
	cellW, cellH, ok := cellPixelSize()
	if !ok {
		return
	}
	chartCols, chartRows := chartCellBox(availCols, availRows, cellW, cellH)
	if chartCols < 1 || chartRows < 1 {
		return
	}

	// Center the (possibly non-square-in-cells) chart box within the
	// available pane.
	colOffset := l.chartColStart + (availCols-chartCols)/2
	rowOffset := l.contentTop + (availRows-chartRows)/2

	pxW := int(float64(chartCols) * cellW)
	pxH := int(float64(chartRows) * cellH)
	if pxW < 1 || pxH < 1 {
		return
	}

	wedges := make([]piechart.Wedge, len(slices))
	for i, s := range slices {
		wedges[i] = piechart.Wedge{Bytes: s.Bytes, Color: colors[i]}
	}
	style := piechart.Style{Selected: chartSelected}
	img := piechart.Render(pxW, pxH, wedges, a.selected, style)

	buf := a.frame % 2
	a.frame++
	id := chartImageIDs[buf]
	_ = a.kw.Delete(id)
	_ = a.kw.MoveCursor(colOffset, rowOffset)
	_ = a.kw.TransmitAndDisplay(id, img)
}

// legendPadCols/legendPadRows give the legend pane the same kind of
// internal breathing room the pie chart gets from its own marginPx (see
// internal/piechart) — entries start inset from the pane's own edges
// rather than flush against them (row 0 butting the top bar, the last
// column butting the terminal's right edge, etc.), even though the
// legend has no pixel-level margin concept of its own to borrow that
// from.
const (
	legendPadCols = 1
	legendPadRows = 1
)

// drawLegend lists every slice in the current view — swatch, label, size
// — in the legend pane, only shown when the terminal is wide enough (see
// computeLayout).
func (a *App) drawLegend(l layout, slices []memslice.Slice, colors []color.RGBA) {
	top := l.contentTop + legendPadRows
	bottom := l.contentBottom - legendPadRows
	col := l.legendCol + legendPadCols
	width := l.legendWidth - 2*legendPadCols

	row := top
	for i, s := range slices {
		if row >= bottom {
			break
		}
		style := legendLabelStyle
		if i == a.selected {
			style = legendSelectedStyle
		}
		a.screen.SetContent(col, row, '█', nil, tcell.StyleDefault.Foreground(tcellColorFromRGBA(colors[i])))
		label := truncate(s.Label, width-12)
		a.drawText(row, col+2, width-2, label, style)
		size := formatBytes(s.Bytes)
		a.drawTextRight(row, col+width-len(size), len(size), size, legendSizeStyle)
		row++
	}
}

// drawTopBar draws row 0: hotkey hints, plus a paused/live state
// indicator that is *always* visible — right-aligned, but with its
// column reserved before the hotkey text is drawn (not drawn only if the
// hotkey text happens to leave room), so it can never silently get
// pushed off by a long hotkey line on a merely-average-width terminal. A
// hard-to-miss `[PAUSED]` badge (reverse-video gold) shows when refresh
// is suppressed; a quieter `live` indicator shows otherwise, so pausing
// and resuming both produce a visible, unambiguous change rather than
// resuming just silently removing the only indicator that existed.
func (a *App) drawTopBar(cols int) {
	type seg struct {
		text  string
		style tcell.Style
	}
	segs := []seg{
		{" <space>", statusHotkey}, {" pause/resume", statusLabel},
		{"   <?>", statusHotkey}, {" help", statusLabel},
		{"   <e>", statusHotkey}, {" group by exec", statusLabel},
	}
	if a.groupByExec {
		segs = append(segs, seg{" [on]", statusValue})
	}
	segs = append(segs, seg{"   <q>", statusHotkey}, seg{" quit", statusLabel})

	badge, badgeStyle := " live ", liveBadgeStyle
	if a.isPaused() {
		badge, badgeStyle = " [PAUSED] ", pausedBadgeStyle
	}
	badgeStart := max(cols-len(badge), 0)

	x := 0
	for _, s := range segs {
		avail := badgeStart - x
		if avail <= 0 {
			break
		}
		x += a.drawText(0, x, avail, s.text, s.style)
	}

	if badgeStart < cols {
		a.drawText(0, badgeStart, cols-badgeStart, badge, badgeStyle)
	}
}

// drawBottomBar draws the last row: overall memory usage, current drill
// depth, how many slices are in the current view, and (if present) the
// most recent collection error — global state, not anything tied to the
// current selection.
func (a *App) drawBottomBar(row, cols, numSlices int) {
	type seg struct {
		text  string
		style tcell.Style
	}
	segs := []seg{
		{" " + formatBytes(a.usedBytes) + " / " + formatBytes(a.totalBytes) + " used", statusValue},
		{"   depth:", statusLabel}, {" " + strconv.Itoa(a.stack.Depth()), statusValue},
		{"   slices:", statusLabel}, {" " + strconv.Itoa(numSlices), statusValue},
	}
	if a.lastErr != nil {
		segs = append(segs, seg{"   error: " + a.lastErr.Error(), tcell.StyleDefault.Foreground(tcell.ColorRed)})
	}

	x := 0
	for _, s := range segs {
		x += a.drawText(row, x, cols-x, s.text, s.style)
	}
}

// helpEntries is the single source of truth for every hotkey — both the
// '?' dialog and drawTopBar's hints ultimately describe the same set, so
// there's exactly one place this list is written down.
var helpEntries = []struct{ key, desc string }{
	{"←/→, ↑/↓", "navigate slices"},
	{"Enter", "drill into Remainder"},
	{"Backspace, b", "back out one level"},
	{"Space", "pause / resume"},
	{"e", "group processes by exec path"},
	{"?", "toggle this help"},
	{"q, Ctrl+C", "quit"},
}

// drawHelpDialog draws a bordered modal box, centered on screen, listing
// every hotkey and what it does — dismissed by any key (see handleKey).
func (a *App) drawHelpDialog(cols, rows int) {
	const title = "Keyboard shortcuts"

	keyWidth := len(title)
	for _, e := range helpEntries {
		if len(e.key) > keyWidth {
			keyWidth = len(e.key)
		}
	}
	descWidth := 0
	for _, e := range helpEntries {
		if len(e.desc) > descWidth {
			descWidth = len(e.desc)
		}
	}

	innerWidth := max(keyWidth+2+descWidth, len(title))
	width := min(innerWidth+4, cols) // 4 = borders + 1 space padding each side
	height := min(len(helpEntries)+4, rows)
	if width < 4 || height < 3 {
		return // terminal far too small for any dialog to be legible
	}
	left := (cols - width) / 2
	top := (rows - height) / 2

	// Border.
	a.screen.SetContent(left, top, '┌', nil, helpBorderStyle)
	a.screen.SetContent(left+width-1, top, '┐', nil, helpBorderStyle)
	a.screen.SetContent(left, top+height-1, '└', nil, helpBorderStyle)
	a.screen.SetContent(left+width-1, top+height-1, '┘', nil, helpBorderStyle)
	for x := left + 1; x < left+width-1; x++ {
		a.screen.SetContent(x, top, '─', nil, helpBorderStyle)
		a.screen.SetContent(x, top+height-1, '─', nil, helpBorderStyle)
	}
	for y := top + 1; y < top+height-1; y++ {
		a.screen.SetContent(left, y, '│', nil, helpBorderStyle)
		a.screen.SetContent(left+width-1, y, '│', nil, helpBorderStyle)
		for x := left + 1; x < left+width-1; x++ {
			a.screen.SetContent(x, y, ' ', nil, helpDescStyle)
		}
	}

	titleCol := left + (width-len(title))/2
	a.drawText(top, titleCol, width-2, title, helpTitleStyle)

	row := top + 2
	for _, e := range helpEntries {
		if row >= top+height-1 {
			break
		}
		a.drawText(row, left+2, keyWidth, e.key, helpKeyStyle)
		a.drawText(row, left+2+keyWidth+2, width-(keyWidth+4), e.desc, helpDescStyle)
		row++
	}
}

// drawText writes s starting at (col, row), clipped to at most width
// cells, and returns how many cells it actually advanced.
func (a *App) drawText(row, col, width int, s string, style tcell.Style) int {
	if width <= 0 {
		return 0
	}
	x := col
	limit := col + width
	n := 0
	for _, r := range s {
		if x >= limit {
			break
		}
		a.screen.SetContent(x, row, r, nil, style)
		x++
		n++
	}
	return n
}

// drawTextRight writes s right-aligned within [col, col+width).
func (a *App) drawTextRight(row, col, width int, s string, style tcell.Style) {
	if width <= 0 {
		return
	}
	r := []rune(s)
	if len(r) > width {
		r = r[len(r)-width:]
	}
	start := col + width - len(r)
	for i, ch := range r {
		a.screen.SetContent(start+i, row, ch, nil, style)
	}
}

// truncate clips s to at most n runes, appending an ellipsis if it had to.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}
