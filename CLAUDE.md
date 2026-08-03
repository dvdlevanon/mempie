# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

mempie is a Go, **kitty-terminal-only** TUI that shows physical RAM usage as an interactive, drillable pie chart: one wedge per process (sized by PSS) plus one wedge per fixed `/proc/meminfo` category (Free, Cache, Shmem, ...), top 13 by size with everything else folded into a clickable "Remainder" wedge you can drill into recursively. Pure procfs, no daemons, no persistence — restart it and it starts fresh from a new snapshot. Physical + disk-backed memory only, no virtual-memory concepts, no GPU/VRAM accounting.

The kitty graphics protocol requirement is a hard requirement, not a progressive enhancement — there is no non-kitty fallback renderer anywhere in this codebase, and there shouldn't be one added without a real conversation about the tradeoff first (see "Privilege and terminal requirements" below).

## Commands

### Build
- `make build` / `make` — `go build -o bin/mempie ./cmd/mempie`. No cgo, no non-Go runtime dependencies — single static binary.
- `make run` — build + run.
- `make install` — `go install ./cmd/mempie`.
- `make clean` — remove `bin/`.

### Test / lint
- `go build ./...`
- `go vet ./...`
- `go test ./...` — full suite, all synthetic-fixture-based, no real `/proc` or real terminal required (see "Testing approach" below).
- `go test ./... -run TestName -v` — single test.
- `gofmt -l -w .` — this repo is expected to stay gofmt-clean; run after edits.
- `make check` — `fmt-check` (CI-style, doesn't rewrite) + `vet` + `test`, the pre-flight bundle.
- `make tidy` — `go mod tidy`.

### Running
`sudo ./bin/mempie` (or `sudo ./bin/mempie -refresh 5s`). Root is required to read other users' `/proc/<pid>/smaps_rollup` — there is deliberately no permission-fallback logic; run as your own user and you'll just silently see only your own processes (see "Privilege and terminal requirements"). Needs a kitty-compatible terminal — running it anywhere else exits immediately with a clear error (see the same section). Most dev sandboxes have neither passwordless sudo nor a kitty session, so a live interactive run typically can't be verified end-to-end by an agent in-session — the fallback used throughout this project's development was rendering the pie chart to a PNG via `internal/piechart.Render` directly and inspecting/sending that image, plus `tcell.SimulationScreen`-backed tests for the text-cell UI (see "Testing approach"). Say so rather than claiming a live run works.

## Architecture

### Data flow, one direction, mostly single-threaded

1. **Collection** (`internal/meminfo`, `internal/procpss`, `internal/tui/collect.go`): `collectSlices()` calls `meminfo.ReadFields`/`Categories` (parses `/proc/meminfo`) and `procpss.Collect` (enumerates `/proc/<pid>`, reads `smaps_rollup`'s `Pss` field, `comm`, and the resolved `/proc/<pid>/exe` target) and flattens both into one `[]memslice.Slice`. This is the one place PSS/meminfo actually get read from disk.
2. **Grouping** (`internal/memslice.GroupProcessesByExec`, `internal/tui/app.go`'s `resetTopLevel`): optionally (on by default, `e` toggles it), process slices sharing the same resolved exec path get merged into one slice before ranking — see "Sharp edges" below for exactly why this has to happen *before* ranking, and why the grouped label sometimes isn't the exec path's own base name.
3. **Ranking / drill stack** (`internal/memslice.Rank`, `DrillStack`): sorts descending by size, top 13 + a synthetic Remainder folding the rest, with the folded slices attached to `Remainder.Children` so drilling in doesn't need to re-touch `/proc`. `DrillStack` is a growable stack of already-computed `View`s; `Back()` pops and restores, never recomputes.
4. **Rendering** (`internal/piechart`, `internal/kittygfx`, `internal/tui`): `App.redraw()` (single-goroutine-owns-state, see below) builds wedge colors via `sliceColors`, rasterizes via `piechart.Render` into an `*image.NRGBA`, and transmits it via `kittygfx.Writer.TransmitAndDisplay`. Legend/detail/status text is drawn directly via `tcell.Screen.SetContent`.

### Package map

- `internal/meminfo` — `/proc/meminfo` parser + fixed category list (`Categories`). Pure, no I/O side effects beyond `ReadFields`'s file open.
- `internal/procpss` — per-pid PSS/comm/exe reader (`Collect`). A pid whose `smaps_rollup` can't be read is silently skipped for that cycle (expected/constant on a live system, not an error worth surfacing); a pid whose `comm`/`exe` can't be read falls back gracefully rather than dropping the process.
- `internal/memslice` — the data model (`Slice`, `Kind`), `Rank`/`View`/`DrillStack`, and `GroupProcessesByExec`. No `/proc` access, no rendering — pure logic, the most heavily unit-tested package in the repo.
- `internal/piechart` — the rasterizer (`Render`). No terminal/tcell knowledge, no `/proc` access — takes wedges + a selected index, returns an `*image.NRGBA`. Deliberately independently testable/previewable (render straight to a PNG file) without any terminal at all.
- `internal/kittygfx` — hand-rolled kitty graphics protocol client (`Writer`) plus a real capability probe (`Detect`), adapted from this author's `diskmon`. No mature high-level Go library for this protocol exists.
- `internal/tui` — wires everything together: `App` (tcell event loop, single-goroutine-owns-state), `collect.go` (glues meminfo+procpss into `collectSlices`), `colors.go` (sticky category palette, rotating muted process palette), `layout.go` (screen-region math), `format.go` (`formatBytes`), `winsize.go` (`TIOCGWINSZ` cell-pixel-size query).
- `cmd/mempie` — thin entrypoint: flag parsing, `kittygfx.Detect()` (must run before tcell takes over the terminal), `tui.NewApp`, `Run`.

### Single-goroutine-owns-state discipline

`App`'s fields are only ever read or mutated from the main goroutine running `Run`'s event loop (`select` over input events, the refresh ticker, and `refreshResults`). The *only* background goroutine (`maybeTriggerRefresh`) does nothing but call `collectSlices()` and send the result on a channel — it never touches `App` itself. This mirrors this author's other TUI tools (`deskwatch`, `diskmon`): if you find yourself wanting a mutex anywhere in `internal/tui`, something's architecturally off. The one race this discipline has to actively guard against: the ticker can fire (kicking off a background collection) *while* the user drills into a Remainder before that collection finishes. `applyRefresh` handles this by checking `a.stack.Depth() == 0` before applying the result — if the user drilled in mid-flight, the now-stale full-refresh result is discarded rather than clobbering their frozen, drilled-in view. The next tick at depth 0 fetches a fresh one anyway.

### Privilege and terminal requirements

- **Root**: assumed, not checked or enforced at startup, and there's deliberately no permission-fallback path — a process mempie can't read is just skipped for that cycle. Don't add a root check; don't add fallback logic for reading other users' `smaps_rollup`. If asked to make mempie work without root, that's a real scope conversation, not a small patch.
- **Kitty graphics protocol**: detected via a real capability-probe escape sequence and reply wait (`kittygfx.Detect`), not `$TERM`/`$KITTY_WINDOW_ID` sniffing (those say nothing about whether the protocol actually reaches the terminal through an SSH/multiplexer chain). `Detect` must run *before* `tcell.Screen.Init()` — it needs its own brief raw-mode read of the terminal's reply. A negative result is fatal (`cmd/mempie/main.go` exits with a clear error), not a fallback trigger — there is no other rendering backend in this codebase.

## Sharp edges worth knowing before touching this code

These are real bugs that shipped and were found (mostly via direct user testing, not caught by the test suite ahead of time) — the fixes and their regression tests are the load-bearing parts of the packages below; don't casually revert them.

- **Averaging colors during anti-aliasing must happen in premultiplied-alpha space, or transparent edges get a dark fringe.** `piechart.Render`'s supersampling averages several subsamples' colors per pixel; naively averaging straight (non-premultiplied) RGB and alpha *independently* darkens the result, because a fully-transparent sample's arbitrary, never-meant-to-be-seen RGB still drags the average down even at alpha≈0. `Render` premultiplies each subsample (`R*A/255` etc.) before summing, and only un-premultiplies once at the very end, right before storing into the `*image.NRGBA` (which itself stores straight, not premultiplied — Go's `color.RGBA` is premultiplied by its own doc contract, `color.NRGBA` is straight; the raster deliberately uses `NRGBA` throughout, not `RGBA`, matching what PNG itself stores). `TestRenderEdgePixelsHaveNoDarkFringing` is the regression guard — don't remove it, and don't reintroduce a plain `(a+b)/2` style average anywhere near an alpha channel here.
- **Full-image supersampling is 5-8x too slow for interactive redraws — only pixels near a real edge should ever get supersampled.** An early version supersampled every pixel unconditionally: ~300ms for a 700x700 chart, which is a real, felt lag on every arrow-key press (every key press triggers a full redraw). `Render`'s `needsAA` check (rim proximity, wedge-boundary proximity via a precomputed-unit-vector cross product — deliberately trig-free since it runs on *every* pixel, not just the edge band — outline-band proximity, gap-inner-radius proximity) decides per-pixel whether supersampling could actually change the result; if not, one `classify()` call replaces sixteen. Brought the same 700x700 case to ~60ms. If you touch this fast path, re-benchmark — it's easy to accidentally make `needsAA` too conservative (over-triggering supersampling everywhere) without noticing in a screenshot, only in wall-clock time.
- **A right-aligned status element drawn "only if there's room after the other text" will silently vanish on any terminal that isn't unusually wide.** The `[PAUSED]`/`live` indicator in `drawTopBar` used to be drawn conditionally (`if bx > x`) after the hotkey hints — the hotkey text alone is ~63 columns, so the badge only ever showed above ~74 columns, i.e. essentially never on a normal terminal. Fixed by reserving the indicator's column range *first* (`badgeStart := max(cols-len(badge), 0)`) and clipping the hotkey text to stop before it, so the indicator is unconditionally visible and the hotkey text degrades gracefully instead. `TestDrawTopBarPausedBadgeAlwaysVisibleRegardlessOfWidth`/`TestDrawTopBarLiveIndicatorAlsoAlwaysVisible` (via `tcell.SimulationScreen`, checking actual rendered cells across a range of widths down to 30 columns) are the regression guard — any new always-should-be-visible UI element needs the same "reserve space first" pattern and the same kind of width-sweep test, not just a visual check at whatever width happens to be open in a terminal at the time.
- **A layout breakpoint gated on two conditions where one implies the other will hide things it shouldn't.** The legend used to require `cols >= 90 (legendBreakpointCols) && cols-legendWidthCols >= 20` — the first clause was pure noise once the second exists, and it actively hid the legend on terminals (e.g. 80 columns) that had 40+ columns of genuinely spare chart width. Fixed by deleting the redundant fixed-width floor and driving the decision purely off actual leftover space (`minChartColsForLegend`). If you're tempted to add a "minimum terminal width" style guard alongside a "minimum leftover space" guard anywhere in `internal/tui/layout.go`, ask whether the leftover-space check alone already covers it — it usually does.
- **A grouped label built from an exec path's basename can be meaningless, and process identity has to be layered carefully.** `GroupProcessesByExec` groups by the *resolved* `/proc/<pid>/exe` target (not `comm`, which truncates to 15 chars and could theoretically collide across unrelated binaries) — but some real-world tools install their actual binary at a path whose filename is a bare version string (discovered live: this author's own Claude Code CLI lives at `.../versions/2.1.220`, no other name), so labeling the group `2.1.220` is meaningless on its own. `looksLikeVersion` (`^v?[0-9]+(\.[0-9]+)*$`, deliberately not matching anything with letters mixed in — `python3.11` must still read as a real name) gates a label-only fallback to `Comm`; the *grouping key* stays the exec path regardless, since it's still the more precise identity for merging. If you touch this, keep the key/label distinction: never make grouping itself key off `Comm`, only the display fallback.
- **Grouping must run *before* ranking, never after.** `GroupProcessesByExec` is applied to the flat slice list in `resetTopLevel`, ahead of `Rank`/`DrillStack.Reset` — because grouping changes what "top 13" even means (a dozen small per-PID entries can out-rank a handful of large single-process entries once summed, or vice versa). Running it as a post-processing step on an already-ranked `View` would rank the wrong things and there'd be no way to fix it after the fact without re-ranking anyway.
- **Toggling grouping, or completing a live refresh, must both collapse the drill stack back to depth 0 — via the same code path.** `resetTopLevel()` is the *only* place that calls `stack.Reset`; both the `e` hotkey handler and `applyRefresh` call it, rather than each doing their own `if a.groupByExec { grouped := ... }; a.stack.Reset(...)` inline. This is deliberate, not just DRY-for-its-own-sake: if a Remainder's children were computed under one grouping mode and you drill into it after the mode changed, "drilling into old children under a new grouping" is meaningless — collapsing to depth 0 is the only coherent response, and having one function do it guarantees both trigger paths agree.

## Pause/resume semantics (a common source of "wait, why didn't it refresh" confusion)

`isPaused()` is `a.manualPaused || a.stack.Depth() > 0` — read that as: **drilling always forces paused, full stop, regardless of the manual toggle's state.** The two interact asymmetrically on the way back out:

- Backing out one level while still at depth > 1 does **not** resume anything — still paused, because still drilled in.
- Reaching depth 0 via `Back()` unconditionally clears `manualPaused` too (`popDrill`), even if the user had manually paused *before* ever drilling in. This is intentional: "auto-resume once you're back at the top level" per the original spec, not "restore whatever the manual toggle happened to be."
- At depth 0, the manual `Space` toggle works completely independently — "I just want to freeze the top view" without having drilled anywhere.

If you're debugging "refresh isn't happening," check `a.stack.Depth()` before assuming `a.manualPaused` is the culprit — it usually is depth, not the toggle.

## Testing approach

No real `/proc` and no real terminal anywhere in the test suite:

- `internal/meminfo`, `internal/procpss`, `internal/memslice`, `internal/piechart` tests all use synthetic fixtures (literal `/proc/meminfo`/`smaps_rollup` text blocks, hand-built `Slice`/`Wedge` values) — see any `_test.go` in those packages for the pattern.
- `internal/tui`'s UI-chrome tests (`app_test.go`) use `tcell.NewSimulationScreen("")`, which renders into an in-memory cell grid inspectable via `Screen.Get(x, y)` — this is what caught (and now guards against) the paused-badge-invisible-below-74-columns bug above. Prefer this over eyeballing a screenshot for anything claiming "X is always visible" or "X and Y never overlap."
- There's no unit test harness for `piechart.Render`'s actual visual output beyond pixel-level assertions (center/corner/edge color checks) — when in doubt about whether a chart *looks* right (color harmony, gradient subtlety, layout spacing), render straight to a PNG via a throwaway `cmd/previewgen`-style `main.go` (build it, run it against real `/proc` data, inspect the PNG, delete it before committing — this repo has no such tool checked in on purpose, several were built and torn down during development) rather than trying to assert on it in Go.
