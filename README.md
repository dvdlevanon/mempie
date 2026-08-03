# mempie

An interactive, kitty-terminal-only pie chart of physical RAM: how it's
split across processes (by PSS) and fixed kernel/system categories (from
`/proc/meminfo`), drillable one "Remainder" slice at a time. Pure procfs, no
daemons, no persistence — restart it and it starts fresh from a new
snapshot.

Physical + disk-backed memory only. No virtual memory concepts, no
GPU/VRAM accounting (a known blind spot — see "Known limitations").

## Build

```sh
make build   # -> bin/mempie
make run     # build + run
```

or directly with `go build -o mempie ./cmd/mempie`. Requires Go 1.24+
(uses range-over-int and `strings.SplitSeq`). No cgo — the result is a
single static binary.

Other Makefile targets: `make test`, `make vet`, `make fmt` (rewrite),
`make fmt-check` (CI-style check), `make check` (fmt-check + vet + test),
`make tidy` (`go mod tidy`), `make install` (`go install`), `make clean`.

## Running

mempie needs to run as **root** to read other users'
`/proc/<pid>/smaps_rollup` (root is assumed — there's no permission-
fallback logic; a process mempie can't read is silently skipped for that
refresh cycle, which under a non-root invocation means every other user's
processes are simply missing from the chart, not flagged as an error):

```sh
sudo ./bin/mempie
sudo ./bin/mempie -refresh 5s
```

mempie also **requires a kitty-compatible terminal** (kitty itself, or
another emulator implementing its graphics protocol) — this is a hard
requirement, not an optional enhancement, and there is no fallback
renderer for anything else. On startup mempie sends a real capability-
probe escape sequence and waits briefly for the terminal's reply; if
nothing (or garbage) comes back, it exits immediately with a clear error
rather than degrading to a text-only view.

### Flags

| Flag | Default | Meaning |
|---|---|---|
| `-refresh` | `10s` | How often the data is re-collected while unpaused (any `time.Duration` string, e.g. `5s`, `1m`) |

## Hotkeys

| Key | Action |
|---|---|
| `←`/`↑`, `→`/`↓` | move the selection to the previous/next slice |
| `Enter` | activate the selected slice — drills into it if it's **Remainder**, no-op otherwise |
| `Backspace` / `b` | back out one drill level |
| `Space` | manual pause/resume toggle |
| `e` | toggle grouping process slices by executable path — **on by default** (see "Data model") |
| `?` | open/close a dialog listing all of the above |
| `q` / `Ctrl+C` | quit |

The top bar (row 0) shows these hotkeys directly rather than any live
data — the `?` dialog is the actual reference if you forget one, so the
top bar's job is just to remind you it exists. Live state (memory used,
drill depth, slice count) lives in the bottom bar instead — see
"Display".

## Data model

Every refresh cycle, mempie builds a flat list of memory "slices":

- **One per process**, sized by **PSS** (Proportional Set Size) read from
  `/proc/<pid>/smaps_rollup`. PSS splits shared pages (shared libraries,
  etc.) proportionally across every process mapping them, so per-process
  numbers add up to something meaningful — at the cost of double-counting
  against the fixed categories below (see "Known limitations").
- **One per fixed kernel/system category**, sourced from `/proc/meminfo`:

  | Category | meminfo field(s) |
  |---|---|
  | Free | `MemFree` |
  | Cache | `Cached` + `Buffers` |
  | Shmem | `Shmem` |
  | Slab (reclaimable) | `SReclaimable` |
  | Slab (unreclaimable) | `SUnreclaim` |
  | KernelStack | `KernelStack` |
  | PageTables | `PageTables` |
  | Percpu | `Percpu` |
  | VmallocUsed | `VmallocUsed` |
  | HugePages | `HugePages_Total * Hugepagesize` (only if total > 0) |
  | SwapCached | `SwapCached` |

**Grouping process slices by executable path** (`e`, **on by default**)
is a toggleable transform applied to the process slices before ranking:
every process sharing the same resolved `/proc/<pid>/exe` target
(falling back to `comm` if that symlink can't be read) collapses into
one slice — a dozen separate "brave [pid]" entries become one "brave
(12)" entry summing their PSS. It defaults on because a flat per-PID view
of a many-process machine is mostly noise (dozens of near-identical
browser/renderer entries crowding out everything else); `e` still turns
it off for anyone who wants raw per-PID detail. What "top 13" even means
changes once toggled, so flipping it either way always collapses back to
the top-level view (depth 0), the same as a live refresh — see
`internal/tui.App.resetTopLevel`. Fixed categories and Remainder are
untouched by this toggle; it only ever applies to process slices.

All slices (processes + categories) are sorted descending by size; the
**top 13** are shown as individual pie slices, and everything past that is
folded into one synthetic, clickable **Remainder** slice. Drilling into
Remainder re-ranks its folded-in slices the same way (top 13 + a new,
smaller Remainder), recursively — depth can reasonably reach 10-20 levels
on a many-process machine. Backing out restores that level's already-
computed slice set rather than recomputing it, which matters for the
pause behavior below.

Physical RAM size (32G on the reference machine) is read from `MemTotal`,
never hardcoded — mempie works the same on any size machine.

## Pause / freeze behavior

At a 10s (or whatever `-refresh` is set to) refresh interval, processes
near the top-13/Remainder boundary can shuffle in and out between
refreshes — disorienting mid-drill, since a slice you're about to click
could vanish or change meaning before you click it. mempie handles this
with:

- **Auto-pause the instant you drill into a Remainder.** Drilling implies
  "stop moving, let me study this" — refresh stays suppressed at any
  drilled-in depth regardless of the manual toggle's state.
- **Auto-resume only once you've backed all the way out to depth 0.**
  Backing out of one drill level while still at depth > 0 does *not*
  resume refresh; only reaching the top-level view does, and it does so
  unconditionally (clearing the manual toggle too).
- **The manual `Space` toggle works independently at depth 0** — "I just
  want to freeze the top view" without having drilled anywhere.
- A hard-to-miss **`[PAUSED]`** badge (dark text on a solid gold
  background, right-aligned in the top bar) appears whenever refresh is
  suppressed for either reason — not a subtle text change — and a
  quieter **`live`** indicator takes its place otherwise, so pausing and
  resuming both produce a visible, unambiguous change rather than
  resuming just silently removing the only indicator that existed. This
  indicator's column is reserved before the hotkey hints are drawn (not
  drawn only if the hint text happens to leave room), specifically so it
  can never be silently squeezed off by a merely-average-width
  terminal — an earlier version did exactly that, drawing the badge only
  if there was leftover space after the hotkey text, which in practice
  meant it never showed at all below ~74 columns.

## Display

- One pie chart, rendered via the kitty graphics protocol, with **no
  in-chart text labels** — screen space is at a premium in a small pane,
  so labels/sizes/explanations are drawn as ordinary terminal text
  alongside the chart instead (see below). The chart is sized to the
  largest pixel-square that fits its pane (terminal cells are usually
  taller than wide, so this picks whichever of "full available width" or
  "full available height" yields a square without overflowing the other
  dimension — see `chartCellBox` in `internal/tui/layout.go`). No hard
  minimum pixel size is enforced; very small panes just get a very small
  (but still round) chart.
- **Anti-aliased edges, a transparent backdrop, and a subtle per-wedge
  gradient.** The rim, every wedge separator, and the selected-wedge
  outline are all smoothly blended rather than hard-edged (via targeted
  supersampling — see "Rendering" below); everything outside the circle
  is fully transparent rather than a solid fill, so the chart sits
  directly on the terminal's own background with no visible rectangle
  around it; and each wedge gets a light radial highlight (a gentle
  brightening toward its own center, fading to its flat base color at the
  rim) rather than a flat fill. Kitty itself has no role in any of this —
  it only ever displays whatever PNG it's handed, so all three are purely
  `internal/piechart` rasterization choices.
- **Sticky colors for fixed categories.** "Free", "Cache", and the other
  nine fixed `/proc/meminfo` categories each have their own fixed color,
  keyed by name (`categoryColors` in `internal/tui/colors.go`) — they
  look the same across every refresh and every drill level, so they're
  something to build a mental map against over a session. Remainder gets
  its own fixed neutral gray for the same reason (there's always exactly
  zero or one of it per drill level, even though its contents change).
  Process slices are **not** sticky — a given comm+pid combination never
  recurs and PIDs get reused, so there's no stable identity worth being
  sticky about — they cycle through a separate, deliberately muted
  rotating palette instead, by position among the process slices in the
  current view.
- **A legend** (color swatch → label → size) lists every slice in the
  current view, shown alongside the chart whenever there's still enough
  leftover width for a usable chart after reserving the legend's fixed
  36-column width (see `minChartColsForLegend` in
  `internal/tui/layout.go`) — not gated by any additional fixed minimum
  on the terminal's total width. Below that width, the legend is simply
  omitted — the chart itself (color + size, via the sticky/rotating
  palettes above) and the bottom bar's slice count are what's left; there
  is no per-slice tooltip fallback (an earlier version had one, showing
  each fixed category's explanation string on selection — cut because it
  didn't earn its keep; see "Architectural choices"). The legend pane
  itself has a 1-row/1-column inset on every side (`legendPadRows`/
  `legendPadCols` in `internal/tui/app.go`) — the same kind of breathing
  room the pie chart gets from its own pixel-level `marginPx`, just
  reserved as whole cells instead, since plain text rows have no
  pixel-margin equivalent to borrow. Selection is shown two ways at once
  when the legend is visible: a bright outline around
  the wedge itself and a reverse-video legend row, both always agreeing
  since they read `a.selected` directly.
- **Two single-line status bars, top and bottom, each with one job.** The
  top bar (row 0) is hotkey hints plus a right-aligned paused/live state
  indicator (see "Pause / freeze behavior" above) — no other live data.
  The bottom bar (last row) is the reverse: live state, no hotkeys —
  memory used/total, current drill depth, how many slices are in the
  current view, and the most recent collection error if there is one.
- **A `?` dialog** lists every hotkey and what it does, in a bordered box
  centered on screen. It's a simple modal: any key closes it (there's
  nothing to navigate inside it), and opening it deletes the chart's
  kitty image placements outright so the old frame doesn't linger visible
  around the dialog box — `screen.Clear()` only ever resets tcell's own
  text cells each frame, not a separate kitty graphics placement.
- Refresh interval is configurable via `-refresh` (default 10s).

## Known limitations (deliberate, not bugs)

- **The sum of all slices can exceed `MemTotal`.** A page counted in a
  process's PSS can *also* be counted in a fixed category (e.g. a tmpfs
  page shows up in both a process's PSS and the Shmem category). This
  double-counting is an accepted tradeoff of using PSS for meaningful
  per-process numbers, not a bug to fix.
- **No GPU/VRAM accounting.** Out of scope for this tool.
- **No process-level drill-down.** Selecting a process slice is a no-op —
  only the synthetic Remainder slice is ever drillable. This applies
  equally to a grouped exec slice (`e`) — grouping only changes what gets
  summed together before ranking, not what's drillable afterward.
- **Requires root** to see every user's processes; without it, other
  users' processes are silently missing from the chart rather than
  flagged.
- **A process that exits between enumeration and the PSS read is silently
  skipped** for that refresh cycle (not retried, not shown as an error).
- **Group-by-exec falls back to `comm` when `/proc/<pid>/exe` can't be
  resolved.** Two different processes that both hit this fallback and
  happen to share a truncated 15-character `comm` would incorrectly merge
  under it — accepted as a rare edge case rather than plumbing a
  more-precise-but-more-complex identity through.
- **No non-kitty fallback.** Terminals that don't implement the kitty
  graphics protocol (verified by a live capability probe, not by trusting
  `$TERM`) can't run mempie at all.

## Architectural choices

A few choices had a real tradeoff and were confirmed up front rather than
just picked silently:

- **Group-by-exec (`e`) defaults on, not off.** The spec's "no
  process-level drill-down" framing reads as processes being individually
  significant, which argues for showing them ungrouped by default — but
  in practice a many-process machine's top ranks fill up with a dozen
  near-identical browser/renderer/helper entries for the same app,
  crowding out everything else in the chart before you've even had a
  chance to look. Defaulting grouped gives a more immediately useful
  first view; `e` still turns it off for anyone who specifically wants
  raw per-PID detail.
- **Category colors are hand-tuned per category, not an evenly-spaced hue
  wheel indexed by list position.** An earlier version picked a slice's
  color purely from its rank position along 11 evenly-spaced hues, which
  (combined with rank position changing color) meant no stickiness at
  all, and even after switching to sticky-by-name coloring, indexing the
  same wheel by a category's position in a list arbitrarily landed "Free"
  on a bold, alarm-like red. `categoryHSL` in `internal/tui/colors.go`
  hand-picks each category's hue/saturation/lightness instead, aiming for
  a calmer, more cohesive palette with at least a loose semantic fit
  (Free: calm teal-green; Cache: classic blue; the two Slab tiers share a
  violet family at different lightness, since they're conceptually
  related) and lower average saturation than a raw color-wheel rainbow.
  Every slice's label is always available in the legend, so identity
  never depends on color alone even where two hues might read as close
  for some viewers, and none of this has been run through a
  colorblind-safety validator.
- **The per-slice detail/tooltip line was removed outright, not kept
  alongside the new bottom bar.** An earlier version reserved the bottom
  row for the currently-selected slice's label, size, and (for a fixed
  category) its baked-in explanation string — this was cut on direct
  feedback that the explanation text wasn't earning the screen space it
  cost. Rather than keep a narrower version of it, the bottom row was
  repurposed entirely for global state (memory used/total, depth, slice
  count) that has nowhere else to live now that the top bar is
  hotkeys-only — see the next point. `meminfo.Category`/`memslice.Slice`
  no longer carry an explanation field at all, rather than leaving one
  populated and unused.
- **Hotkeys, arrow-key navigation, and the legend width breakpoint** were
  left to this tool's own judgment per the spec (each is a small,
  easily-revisited choice, not a real architectural tradeoff) — see the
  tables above for exactly what was picked.
- **The top bar is hotkeys-only; all live state moved to the bottom
  bar.** An earlier version put memory/refresh/depth/slice-count in the
  top bar (alongside hotkey hints) and the per-slice detail line in the
  bottom row. Once the detail line was cut (see above) and a `?`
  hotkey-reference dialog was added, splitting cleanly — top bar =
  "what can I press," bottom bar = "what's currently true" — read better
  than continuing to interleave both kinds of information in one line.
- **Grouping keys on the resolved exec path (`/proc/<pid>/exe`), not
  `comm`.** `comm` is truncated to 15 characters and two unrelated
  binaries could in principle coincide on it; the exe symlink target is
  the more precise identity "group by executable" actually implies. Comm
  is kept as a fallback for the (rare) case the symlink can't be read,
  rather than dropping the process from grouping entirely.
- **A grouped slice's *label*, however, falls back to `comm` when the
  exec path's base name looks like a bare version number**
  (`looksLikeVersion`, e.g. `2.1.220`, `v1.0`) rather than a real program
  name — discovered live: this author's own Claude Code CLI installs its
  actual binary at `.../versions/2.1.220`, no other name, so the grouped
  label read as the meaningless `2.1.220` instead of `claude`. The
  grouping *key* stays the full exec path regardless (still correct/more
  precise than comm for merging); only the *label* substitutes comm when
  the path's base name doesn't look like a real name. Deliberately narrow
  — `python3.11` (letters mixed with digits/dots) still reads as a real
  name and isn't affected — see `TestLooksLikeVersion` for the exact
  boundary.
- **Byte formatting uses binary (1024-based) units labeled KB/MB/GB/...**,
  the same convention `du`/`ps`/`top` use, not decimal SI units. See
  `formatBytes` in `internal/tui/format.go`.
- **The chart has no background fill at all — it's rendered as
  `*image.NRGBA` with true per-pixel transparency**, not the solid opaque
  dark slate an earlier version used (matching this author's other
  kitty-graphics tool, `diskmon`, which sidesteps transparency's
  correctness risk by staying fully opaque — mempie no longer does).
  Getting this right needs alpha-aware (premultiplied) averaging during
  the anti-aliasing supersampling pass — averaging straight (non-
  premultiplied) RGB and alpha independently produces visible dark
  fringing at every edge, since a half-transparent sample's arbitrary RGB
  bleeds into the blend; see the "Rendering" section below and
  `TestRenderEdgePixelsHaveNoDarkFringing`.
- **Selection resets to the largest slice (index 0)** whenever the
  current view changes — on every live refresh, on drilling in, and on
  backing out — rather than trying to preserve "the same slice" across a
  ranking change where that slice may not even exist in the new view.

## How it works

### Data collection

`internal/meminfo` parses `/proc/meminfo` into a byte-valued field map
(kB-suffixed lines are converted to bytes at parse time; unitless lines
like `HugePages_Total` are kept as raw counts — this asymmetry is exactly
what lets `HugePages_Total * Hugepagesize` fall out as a byte count with
no extra conversion) and builds the fixed category list from it.

`internal/procpss` enumerates `/proc/<pid>`, reads each process's `Pss`
line out of `smaps_rollup` (converted to bytes), its `comm`, and its
resolved `/proc/<pid>/exe` target (`readExe`, falling back to `comm` if
the symlink can't be read — see "Known limitations"). A pid whose
`smaps_rollup` can't be read is skipped; a pid whose `comm` can't be read
(but whose `smaps_rollup` could) gets a `pid <N>` placeholder label
rather than being dropped.

### Ranking and the drill stack (`internal/memslice`)

`Rank` sorts a flat `[]Slice` descending by byte size — with a
label-based tiebreak, since sort ties would otherwise be free to flip
order between calls (the input is typically built from `/proc`
enumeration order, which carries no meaningful ordering of its own) — and
splits it into the top 13 individual slices plus, if more remain, one
`Remainder` slice summing the rest, with those folded-in slices attached
as `Remainder.Children` for later re-ranking.

`GroupProcessesByExec` is a separate transform applied to the flat slice
list *before* `Rank`, when the `e` toggle is on: every `KindProcess` slice
sharing the same `Exec` merges into one, byte counts summed, labeled with
the exec's base name and a `(N)` count suffix once N > 1 — or, when that
base name matches `looksLikeVersion` (a bare version number like
`2.1.220`, no letters), the group's `Comm` instead, since a version
string alone isn't a meaningful label (see "Architectural choices").
Grouping has to run before ranking, not after, since it changes what
"top 13" even means — running it on an already-ranked `View` would rank
the wrong things. Category and Remainder slices pass through untouched.

`DrillStack` is a plain growable stack of already-computed `View`s (each a
`Top []Slice` + `*Remainder`), not a fixed-depth structure — depth 10-20
is expected on a many-process machine. `DrillInto` re-ranks a Remainder's
children and pushes a new level; `Back` pops one level, restoring the
previous level's already-computed view rather than recomputing it (this
is what makes the pause/freeze behavior correct: nothing about backing
out ever re-samples live data).

### Rendering (`internal/piechart`, `internal/kittygfx`, `internal/tui`)

`internal/piechart.Render` rasterizes wedges directly into an
`image.NRGBA` (straight, non-premultiplied alpha — see "Transparency"
below). For any given point, `pieGeometry.classify` computes its polar
angle (clockwise from 12 o'clock, matching the order slices are listed in
the legend) and radius from the pie's center, looks up which wedge's
angular range it falls in, and returns its color: a gradient-lightened
wedge fill (see below), overridden to fully transparent right on a wedge
separator line (suppressed below a small inner radius, since the
separator's angular width blows up close to the center), and overridden
again to a bright, opaque outline color right on the selected wedge's arc
or straight edges. No text is ever drawn into the raster. `marginPx` (14px)
keeps the circle clear of the image's own edges on every side — the
chart's padding — so it doesn't butt directly up against whatever's drawn
immediately above/below/beside its pane.

**Transparency:** everything outside the circle, and the separator lines
between wedges, are fully transparent rather than a solid fill — an
earlier version filled the whole image with a solid dark background,
which showed up as a visible rectangle around the circle against
whatever the terminal's actual background happened to be. Getting this
right without visible fringing needs care: a supersampled edge pixel is
an average of some fully-opaque wedge-fill samples and some fully-
transparent samples, and naively averaging straight RGB and alpha
*independently* darkens the result (a transparent sample's arbitrary,
never-intended-to-be-seen RGB still drags the average down even at
alpha≈0). `Render` instead accumulates each subsample's *premultiplied*
RGB (`R*A/255` etc.) alongside a plain alpha sum, averages those, and
only un-premultiplies (divides back out by the resulting alpha) once, at
the very end, before storing — the standard way to alpha-blend/average
without fringing. `TestRenderEdgePixelsHaveNoDarkFringing` is the
regression guard for this.

**Anti-aliasing** works by calling `classify` at several sub-pixel offsets
per output pixel and averaging the results (per above), rather than one
sample per pixel — a pixel straddling an edge gets a proportional blend
instead of jumping straight from one color/alpha to the other. Doing that
for every pixel would be needlessly slow (an early version supersampled
unconditionally and took ~300ms for a 700x700 chart — noticeable lag on
every arrow-key press, since each redraws the chart), so `Render` first
checks `needsAA`: a point deep inside a single wedge, far from the rim,
any boundary ray, or the selection outline, would classify identically at
every subsample, so it's classified once instead of 16 times. Only the
thin band of pixels actually near a real edge pays the supersampling
cost, which is what makes this fast — `needsAA`'s own boundary-proximity
check is a 2D cross product against each boundary's precomputed unit
direction vector (computed once per `Render` call, not per pixel)
specifically so that check itself never needs `sin`/`atan2`, since it
runs on every interior pixel rather than just the edge band. Net effect:
the same 700x700 chart renders in ~60ms fully anti-aliased, close to two
orders of magnitude of headroom below what would visibly lag a keypress,
and real terminal-pane charts are typically smaller than 700x700 anyway.

**The gradient** is a per-wedge radial highlight: `classify` lightens a
wedge's flat fill color toward white by an amount that's largest at the
wedge's own center and fades to zero at the rim (`gradientStrength`,
eased so the brightening is concentrated near the center rather than
spread evenly across the wedge). This is deliberately a *per-wedge*
gradient, not one global light-source gradient across the whole pie, so
it reads consistently regardless of a wedge's size or position, and
deliberately subtle ("super light") rather than a strong glossy dome.

`internal/kittygfx` is adapted from this author's `diskmon` (same
protocol implementation, same capability-probe `Detect`) — there's no
mature high-level Go library for the kitty graphics protocol, so it's
hand-rolled in both tools. Unlike `diskmon`, mempie's `Detect` failing is
fatal (see "Running" above) rather than a fallback trigger, since there's
only one rendering backend here.

`internal/tui.App` is a single-goroutine-owns-state tcell app: the only
background goroutine (started in `maybeTriggerRefresh`) does nothing but
call `collectSlices` and send the result on a channel — every read or
mutation of `App`'s own fields happens on the main event-loop goroutine,
matching this author's other TUI tools (`deskwatch`, `diskmon`). A
refresh is only ever kicked off while at depth 0 and unpaused; if the
user drills in while one is already in flight, the stale result that
eventually arrives is discarded rather than clobbering the now-frozen
drilled-in view (see `applyRefresh`).

`resetTopLevel` is the one place that rebuilds the depth-0 view from
`a.baseSlices` (applying `GroupProcessesByExec` first if `e` is toggled
on) — both a completed live refresh and the `e` toggle itself call it,
so the two can't drift into applying grouping inconsistently.

The `?` dialog (`drawHelpDialog`) is a genuinely simple modal: `redraw`
checks `a.showHelp` right after the top bar and, if set, draws the dialog
and returns *before* touching the chart/legend/bottom-bar code at all —
there's no partial-overlay logic to keep in sync with the normal view.
`helpEntries` is the dialog's own complete hotkey list; the top bar's
hints (`drawTopBar`) are a separate, shorter, hand-written subset of the
same set — if a new hotkey is ever added, both need updating, there's no
single shared source for the two yet.

## Tests

```sh
go test ./...
```

Covers `/proc/meminfo` parsing and fixed-category construction (including
the `HugePages`-omitted-when-zero case), the PSS-line extraction from a
synthetic `smaps_rollup` fixture, the sort/top-13/remainder ranking logic
and multi-level drill-stack push/pop, `GroupProcessesByExec` (merging,
the single-process no-count-suffix case, base-name extraction, leaving
categories/Remainder untouched, the exe-unresolved comm-fallback case,
and `looksLikeVersion`'s label-fallback boundary — version-like strings
fall back to comm, a real name with digits like `python3.11` doesn't) —
synthetic fixtures throughout, no real `/proc` needed — the pie rasterizer's
angle/boundary math, basic wedge-fill/outline behavior, and the
premultiplied-averaging anti-fringing guard, the legend-vs-chart layout
breakpoint and square-chart-box sizing math, and (via a `tcell`
`SimulationScreen`, no real terminal needed) the top bar's and legend's
actual rendered cells — specifically that the paused/live indicator (see
"Pause / freeze behavior") is visible at every terminal width down to
30 columns and never overlaps the hotkey text, and that the legend's
padding rows/columns are genuinely blank rather than just visually
close to it.
