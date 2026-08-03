package piechart

import (
	"image/color"
	"testing"
)

func TestClockwiseAngleCardinalPoints(t *testing.T) {
	cases := []struct {
		dx, dy float64
		want   float64
	}{
		{0, -1, 0},   // straight up
		{1, 0, 90},   // right
		{0, 1, 180},  // down
		{-1, 0, 270}, // left
	}
	for _, c := range cases {
		got := clockwiseAngle(c.dx, c.dy)
		if got < c.want-0.01 || got > c.want+0.01 {
			t.Errorf("clockwiseAngle(%v, %v) = %v, want %v", c.dx, c.dy, got, c.want)
		}
	}
}

func TestWedgeIndexBasic(t *testing.T) {
	boundaries := []float64{0, 90, 180, 360}
	if got := wedgeIndex(45, boundaries); got != 0 {
		t.Errorf("wedgeIndex(45) = %d, want 0", got)
	}
	if got := wedgeIndex(90, boundaries); got != 1 {
		t.Errorf("wedgeIndex(90) = %d, want 1", got)
	}
	if got := wedgeIndex(200, boundaries); got != 2 {
		t.Errorf("wedgeIndex(200) = %d, want 2", got)
	}
	if got := wedgeIndex(359.999, boundaries); got != 2 {
		t.Errorf("wedgeIndex(359.999) = %d, want 2", got)
	}
}

func TestAngularDistWraparound(t *testing.T) {
	if d := angularDist(359, 1); d < 1.9 || d > 2.1 {
		t.Errorf("angularDist(359, 1) = %v, want ~2", d)
	}
	if d := angularDist(10, 20); d < 9.9 || d > 10.1 {
		t.Errorf("angularDist(10, 20) = %v, want ~10", d)
	}
}

func TestRenderEmptyWedgesReturnsTransparent(t *testing.T) {
	img := Render(20, 20, nil, -1, Style{})
	if a := img.NRGBAAt(10, 10).A; a != 0 {
		t.Errorf("center pixel alpha = %d, want 0 (fully transparent)", a)
	}
}

func TestRenderZeroTotalReturnsTransparent(t *testing.T) {
	wedges := []Wedge{{Bytes: 0, Color: color.RGBA{255, 0, 0, 255}}}
	img := Render(20, 20, wedges, -1, Style{})
	if a := img.NRGBAAt(10, 10).A; a != 0 {
		t.Errorf("center pixel alpha = %d, want 0 (all-zero wedges)", a)
	}
}

func TestRenderSingleWedgeFillsCircleCenter(t *testing.T) {
	fill := color.RGBA{200, 50, 50, 255}
	wedges := []Wedge{{Bytes: 100, Color: fill}}
	img := Render(40, 40, wedges, -1, Style{})

	// The center gets the gradient's maximum lightening, so it won't be
	// bit-exact to fill — but it should still clearly read as "the red
	// wedge, lightened", not transparent and not some other hue: every
	// channel moves only toward 255, never below its own base value, and
	// it's fully opaque.
	center := img.NRGBAAt(20, 20)
	if center.A != 255 {
		t.Errorf("center pixel alpha = %d, want 255 (opaque)", center.A)
	}
	if !isLightenedVariant(center, fill) {
		t.Errorf("center pixel = %v, want a lightened variant of fill %v", center, fill)
	}
	// A corner pixel should be outside the circle, i.e. fully transparent
	// — there's no visible rectangle/background around the chart.
	if a := img.NRGBAAt(0, 0).A; a != 0 {
		t.Errorf("corner pixel alpha = %d, want 0 (fully transparent, no background square)", a)
	}
}

func TestRenderTwoWedgesOccupyOppositeHalves(t *testing.T) {
	red := color.RGBA{255, 0, 0, 255}
	blue := color.RGBA{0, 0, 255, 255}
	wedges := []Wedge{{Bytes: 1, Color: red}, {Bytes: 1, Color: blue}}
	img := Render(60, 60, wedges, -1, Style{})

	// Center (30,30), radius = min(60,60)/2 - marginPx(14) = 16, so pick
	// points comfortably inside that (offset magnitude ~11) rather than
	// near the old, smaller margin's edge.
	//
	// A point most of the way up (well within the "top half" = first
	// wedge, which spans 0-180 degrees clockwise from top, i.e. the
	// right half of the circle) should read as red — not bit-exact,
	// since the radial gradient and (for red, whose channel is already
	// 255) rounding both nudge it slightly, but G and B must still be
	// far below R.
	topRight := img.NRGBAAt(38, 22)
	if !(topRight.R > 200 && topRight.G < 60 && topRight.B < 60) {
		t.Errorf("top-right pixel = %v, want a red-family color (first wedge, 0-180 deg)", topRight)
	}
	bottomLeft := img.NRGBAAt(22, 38)
	if !(bottomLeft.B > 200 && bottomLeft.R < 60 && bottomLeft.G < 60) {
		t.Errorf("bottom-left pixel = %v, want a blue-family color (second wedge, 180-360 deg)", bottomLeft)
	}
}

func TestRenderSelectedWedgeGetsOutlineOnArc(t *testing.T) {
	fill := color.RGBA{200, 50, 50, 255}
	outline := color.RGBA{255, 255, 255, 255}
	wedges := []Wedge{{Bytes: 100, Color: fill}}
	img := Render(60, 60, wedges, 0, Style{Selected: outline})

	// Just inside the circle's edge, straight up from center, should be
	// (very close to) the outline color, since the whole (only) wedge is
	// selected — "very close" rather than exact since a pixel straddling
	// the outline band's own inner/outer edge will be a supersampled
	// blend, not every pixel along this scan.
	found := false
	for r := 1.0; r < 30; r++ {
		px := img.NRGBAAt(30, int(30-r))
		if px.R > 250 && px.G > 250 && px.B > 250 {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find a near-white outline pixel somewhere along the top radius")
	}
}

func TestRenderEdgePixelsHaveNoDarkFringing(t *testing.T) {
	// Regression guard for the un-premultiplied-averaging bug: a
	// partially-covered edge pixel (mixing an opaque wedge sample with a
	// transparent "outside circle" sample) must blend toward "less
	// opaque", not toward a darker version of the fill color — the rim
	// should fade smoothly to transparent, never through a dark ring.
	fill := color.RGBA{200, 50, 50, 255}
	wedges := []Wedge{{Bytes: 100, Color: fill}}
	img := Render(200, 200, wedges, -1, Style{})

	cx, cy := 100, 100
	for r := range 100 {
		px := img.NRGBAAt(cx, cy-r)
		if px.A == 0 {
			continue // fully outside the circle
		}
		// Un-premultiply isn't needed for the check itself: any pixel
		// that still has meaningful coverage (alpha) must have a red
		// channel at least proportionally as high as alpha implies for a
		// pure-red fill — i.e. R should never sit far below what's
		// expected for its own alpha, which is what a dark-fringe bug
		// would produce (low R despite mid-to-high alpha).
		if px.A > 40 && px.R < 100 {
			t.Errorf("at r=%d: pixel %v looks dark-fringed (alpha=%d but R=%d)", r, px, px.A, px.R)
		}
	}
}

// isLightenedVariant reports whether c looks like base blended toward
// white by some amount in [0, 1] — i.e. every channel is at least base's
// own value (uint8's range makes "at most 255" automatic).
func isLightenedVariant(c color.NRGBA, base color.RGBA) bool {
	return c.R >= base.R && c.G >= base.G && c.B >= base.B
}
