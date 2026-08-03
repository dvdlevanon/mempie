// Package piechart rasterizes a single pie chart into an *image.RGBA for
// transmission over the kitty graphics protocol. It deliberately draws no
// text of any kind — mempie's spec calls for label-free wedges, since
// screen space for a small pane is at a premium; labels/sizes/explanations
// are the caller's job (a legend or tooltip drawn as ordinary terminal
// text alongside the chart, see internal/tui).
package piechart

import (
	"image"
	"image/color"
	"math"
)

// Wedge is one colored slice of the pie, sized proportionally to Bytes
// among all wedges passed to Render.
type Wedge struct {
	Bytes uint64
	Color color.RGBA
}

// Style bundles the chart's non-data colors.
type Style struct {
	// Selected is the outline color drawn around the currently
	// highlighted wedge (its two straight edges plus its arc), if any.
	// Chosen to contrast against every wedge color, not just some.
	Selected color.RGBA
}

const (
	// marginPx keeps the circle clear of the image edge on all sides —
	// this is the pie chart's own padding, so it doesn't butt directly
	// up against whatever's drawn immediately above/below/beside its
	// pane (status bar, detail line, legend).
	marginPx = 14
	// gapPixels is the on-screen width, in pixels, of the separator line
	// drawn between adjacent wedges (and, for a single-wedge pie, the one
	// cut at the top). Held constant in pixels (not degrees) so it looks
	// equally thin regardless of the pie's radius.
	gapPixels = 1.5
	// gapInnerRadiusPx is the radius below which separator lines are
	// suppressed — without this, the gap's angular width (gapPixels/r)
	// blows up as r→0 and would paint the whole center as background.
	gapInnerRadiusPx = 8
	// outlineWidthPx is the thickness of the selected-wedge highlight
	// along its arc and straight edges.
	outlineWidthPx = 2

	// supersample is the per-axis subsample count used to anti-alias
	// every edge (circle rim, wedge separators, selection outline) —
	// supersample*supersample sample points are classified per output
	// pixel and averaged. This is what makes an edge blend smoothly
	// between two colors instead of jumping straight from one to the
	// other; there's no separate analytic anti-aliasing formula for any
	// one edge kind; averaging enough samples of the same exact-edge test
	// classify() already does gets all of them at once.
	supersample = 4

	// gradientStrength is the maximum amount (as a 0-1 blend-toward-white
	// factor) a wedge's fill is lightened at its own center point,
	// tapering to 0 (the wedge's plain flat color) at the outer rim —
	// a deliberately subtle radial highlight per wedge, not a single
	// global light-source gradient across the whole pie, so it reads
	// consistently regardless of a wedge's size or position.
	gradientStrength = 0.16

	// aaMarginPx is how close (in pixels) a point needs to be to a real
	// edge — the circle's rim, a wedge boundary ray, the selected-wedge
	// outline's inner edge, or the gap-suppression radius near the center
	// — before Render bothers supersampling it. Comfortably larger than
	// half of gapPixels/outlineWidthPx so no part of either transition
	// falls outside the margin and gets rendered hard-edged.
	aaMarginPx = 2.5
)

// Render draws wedges (sized proportionally to their Bytes) as a pie chart
// filling a width x height image, centered, with a radius as large as fits
// within the margin. selected is the index into wedges to draw an outline
// around, or -1 for no highlight. Wedges start at 12 o'clock and proceed
// clockwise in the order given, matching the order they're listed in the
// caller's legend.
//
// Everything outside the circle (and the thin separator line between
// wedges) is fully transparent, not a solid fill — the chart is meant to
// sit directly on the terminal's own background with no visible
// rectangle around it, so it's rasterized as *image.NRGBA (straight,
// non-premultiplied alpha, matching what PNG itself stores) rather than
// *image.RGBA.
func Render(width, height int, wedges []Wedge, selected int, style Style) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	// A freshly-allocated NRGBA is already all-zero, i.e. fully
	// transparent everywhere — there's nothing to pre-fill.
	if width <= 0 || height <= 0 || len(wedges) == 0 {
		return img
	}

	var total uint64
	for _, w := range wedges {
		total += w.Bytes
	}
	if total == 0 {
		return img
	}

	cx := float64(width) / 2
	cy := float64(height) / 2
	radius := float64(min(width, height))/2 - marginPx
	if radius <= 0 {
		return img
	}

	boundaries := make([]float64, len(wedges)+1)
	acc := 0.0
	for i, w := range wedges {
		boundaries[i] = acc
		acc += 360 * float64(w.Bytes) / float64(total)
	}
	boundaries[len(wedges)] = 360

	geo := newPieGeometry(cx, cy, radius, wedges, boundaries, selected, style)

	const n = supersample
	const samples = n * n
	for y := range height {
		for x := range width {
			px := float64(x) + 0.5
			py := float64(y) + 0.5
			dx := px - cx
			dy := py - cy
			r := math.Hypot(dx, dy)

			if r > radius+aaMarginPx {
				continue // far outside the circle: leave fully transparent
			}

			if !geo.needsAA(dx, dy, r) {
				// Deep inside a single wedge (or far enough outside it) that
				// every one of the n*n subsamples below would classify
				// identically anyway — one sample gives the exact same
				// result as averaging n*n of them, for 1/(n*n) the cost.
				// This is what keeps Render fast: only a thin band of
				// pixels around each real edge (rim, wedge boundary,
				// selection outline) ever pays the supersampling cost.
				c := geo.classify(px, py)
				if c.A > 0 {
					img.SetNRGBA(x, y, c)
				}
				continue
			}

			// Average in premultiplied space so a half-transparent edge
			// pixel blends toward "nothing" instead of toward whatever
			// stray RGB a fully-transparent sample happens to carry —
			// otherwise a mostly-transparent sample would darken the
			// result (classic un-premultiplied-averaging fringing).
			var prSum, pgSum, pbSum, aSum float64
			for sy := range n {
				for sx := range n {
					spx := float64(x) + (float64(sx)+0.5)/n
					spy := float64(y) + (float64(sy)+0.5)/n
					c := geo.classify(spx, spy)
					a := float64(c.A)
					prSum += float64(c.R) * a / 255
					pgSum += float64(c.G) * a / 255
					pbSum += float64(c.B) * a / 255
					aSum += a
				}
			}
			avgA := aSum / samples
			if avgA < 0.5 {
				continue // net-transparent pixel: leave it as-is
			}
			// Un-premultiply: NRGBA wants straight, not premultiplied,
			// channel values.
			img.SetNRGBA(x, y, color.NRGBA{
				R: clamp255(prSum / samples / (avgA / 255)),
				G: clamp255(pgSum / samples / (avgA / 255)),
				B: clamp255(pbSum / samples / (avgA / 255)),
				A: clamp255(avgA),
			})
		}
	}

	return img
}

// clamp255 rounds and clamps a float channel value into uint8 range —
// un-premultiplying can overshoot 255 slightly on floating-point rounding
// at a fully-opaque edge.
func clamp255(v float64) uint8 {
	v += 0.5
	if v <= 0 {
		return 0
	}
	if v >= 255 {
		return 255
	}
	return uint8(v)
}

// pieGeometry bundles one render pass's fixed geometry/data so classify
// can be called once per subsample without re-threading the same dozen
// parameters through every call.
type pieGeometry struct {
	cx, cy       float64
	radius       float64
	wedges       []Wedge
	boundaries   []float64
	boundaryDirs [][2]float64 // precomputed unit direction vectors, one per boundaries[i] — see needsAA
	selected     int
	style        Style
}

// newPieGeometry precomputes each boundary's unit direction vector once
// per Render call, so needsAA's per-pixel boundary-proximity check (called
// far more often than the number of boundaries) never needs to call sin
// or atan2 itself.
func newPieGeometry(cx, cy, radius float64, wedges []Wedge, boundaries []float64, selected int, style Style) pieGeometry {
	dirs := make([][2]float64, len(boundaries))
	for i, b := range boundaries {
		theta := b * math.Pi / 180
		// clockwiseAngle's own convention (0deg = straight up = (dx,dy) =
		// (0,-1)) inverted: a boundary at angle theta points in direction
		// (sin theta, -cos theta).
		dirs[i] = [2]float64{math.Sin(theta), -math.Cos(theta)}
	}
	return pieGeometry{
		cx: cx, cy: cy, radius: radius,
		wedges: wedges, boundaries: boundaries, boundaryDirs: dirs,
		selected: selected, style: style,
	}
}

// transparent is the "nothing here" color: outside the circle, and on a
// wedge separator line. Its R/G/B don't matter (they're discarded by
// premultiplication during averaging, and ignored outright by the
// single-sample fast path), but are zeroed for clarity.
var transparent = color.NRGBA{}

// classify decides the exact color at one (sub-pixel-precision) point:
// transparent outside the circle, a wedge's gradient-lit fill inside it,
// transparent again right on a separator line, or (for the selected
// wedge) Style.Selected right on its outline. Render calls this at
// several sub-pixel offsets per output pixel and averages the results,
// which is what anti-aliases all three of those edges at once.
func (g *pieGeometry) classify(px, py float64) color.NRGBA {
	dx := px - g.cx
	dy := py - g.cy
	r := math.Hypot(dx, dy)
	if r > g.radius {
		return transparent
	}

	angle := clockwiseAngle(dx, dy)
	idx := wedgeIndex(angle, g.boundaries)
	col := lighten(g.wedges[idx].Color, gradientStrength*sq(1-r/g.radius))

	if r > gapInnerRadiusPx {
		gapHalfDeg := (gapPixels / 2) / r * (180 / math.Pi)
		if nearAnyBoundary(angle, g.boundaries, gapHalfDeg) {
			col = transparent
		}
	}

	if idx == g.selected {
		edgeHalfDeg := (outlineWidthPx / 2) / max(r, 1) * (180 / math.Pi)
		onEdge := r > gapInnerRadiusPx &&
			(angularDist(angle, g.boundaries[idx]) < edgeHalfDeg ||
				angularDist(angle, g.boundaries[idx+1]) < edgeHalfDeg)
		onArc := r > g.radius-outlineWidthPx
		if onEdge || onArc {
			s := g.style.Selected
			col = color.NRGBA{R: s.R, G: s.G, B: s.B, A: s.A}
		}
	}

	return col
}

// needsAA reports whether the point (dx, dy offset from center, at radius
// r — both already computed by the caller) is close enough to a real edge
// that supersampling it could actually change the result: the circle's
// rim, a wedge boundary ray (the same lines the separator gap and the
// selected wedge's straight-edge outline both live on), the outline
// band's inner edge, or the small zone around gapInnerRadiusPx where gap
// suppression itself switches on. Everywhere else, every subsample would
// classify identically, so Render's caller can skip straight to a single
// classify() call instead of n*n of them.
//
// This is called once per output pixel (not per subsample), so it
// deliberately avoids any trig of its own: the boundary check is a 2D
// cross product against each boundary's precomputed unit direction
// vector (see newPieGeometry), which is the perpendicular distance from
// the point to the *infinite line* through that boundary ray — a
// superset of "close to the ray itself" (it also flags points near the
// mirror-image ray on the opposite side of the center), but that only
// costs a few extra supersampled pixels, never a missed edge.
func (g *pieGeometry) needsAA(dx, dy, r float64) bool {
	if math.Abs(r-g.radius) < aaMarginPx {
		return true
	}
	if r < gapInnerRadiusPx+aaMarginPx {
		return true
	}
	if g.selected >= 0 && math.Abs(r-(g.radius-outlineWidthPx)) < aaMarginPx {
		return true
	}
	for _, d := range g.boundaryDirs {
		if math.Abs(dx*d[1]-dy*d[0]) < aaMarginPx {
			return true
		}
	}
	return false
}

// lighten blends c toward white by amount (0 = c unchanged, 1 = white),
// converting from the caller-facing color.RGBA (Wedge.Color) to the
// color.NRGBA classify works in internally.
func lighten(c color.RGBA, amount float64) color.NRGBA {
	if amount <= 0 {
		return color.NRGBA{R: c.R, G: c.G, B: c.B, A: c.A}
	}
	blend := func(ch uint8) uint8 {
		return uint8(float64(ch) + (255-float64(ch))*amount)
	}
	return color.NRGBA{R: blend(c.R), G: blend(c.G), B: blend(c.B), A: c.A}
}

func sq(x float64) float64 { return x * x }

// clockwiseAngle converts an (dx, dy) offset from the pie's center (image
// coordinates: y grows downward) into a compass-style angle in degrees,
// [0, 360), measured clockwise from straight up.
func clockwiseAngle(dx, dy float64) float64 {
	deg := math.Atan2(dx, -dy) * 180 / math.Pi
	if deg < 0 {
		deg += 360
	}
	return deg
}

// wedgeIndex returns i such that boundaries[i] <= angle < boundaries[i+1].
func wedgeIndex(angle float64, boundaries []float64) int {
	n := len(boundaries) - 1
	for i := range n {
		if angle >= boundaries[i] && angle < boundaries[i+1] {
			return i
		}
	}
	return n - 1 // angle == 360 (floating rounding at the seam)
}

// angularDist returns the smallest absolute difference between two angles
// on a 360-degree circle (so 359 and 1 are 2 apart, not 358).
func angularDist(a, b float64) float64 {
	d := math.Abs(a - b)
	if d > 180 {
		d = 360 - d
	}
	return d
}

// nearAnyBoundary reports whether angle is within halfWidthDeg of any
// boundary (including the wraparound seam at 0/360, since boundaries[0]
// and boundaries[len-1] are the same physical line).
func nearAnyBoundary(angle float64, boundaries []float64, halfWidthDeg float64) bool {
	for _, b := range boundaries {
		if angularDist(angle, b) < halfWidthDeg {
			return true
		}
	}
	return false
}
