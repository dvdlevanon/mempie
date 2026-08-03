package memslice

import "testing"

func makeSlices(n int, base uint64) []Slice {
	out := make([]Slice, n)
	for i := range n {
		out[i] = Slice{
			Label: labelFor(i),
			Bytes: base + uint64(n-i), // descending-friendly, all distinct
			Kind:  KindProcess,
		}
	}
	return out
}

func labelFor(i int) string {
	// Letters wrap (and collide) past i=25 — harmless here, since
	// makeSlices already gives every entry a distinct Bytes value, so
	// Rank's label tiebreak never actually has to fire between two of
	// these.
	return string(rune('a' + i%26))
}

func TestRankFewerThanTopNHasNoRemainder(t *testing.T) {
	all := makeSlices(5, 100)
	top, remainder := Rank(all)
	if len(top) != 5 {
		t.Fatalf("len(top) = %d, want 5", len(top))
	}
	if remainder != nil {
		t.Fatalf("remainder = %+v, want nil", remainder)
	}
}

func TestRankExactlyTopNHasNoRemainder(t *testing.T) {
	all := makeSlices(TopN, 100)
	top, remainder := Rank(all)
	if len(top) != TopN {
		t.Fatalf("len(top) = %d, want %d", len(top), TopN)
	}
	if remainder != nil {
		t.Fatalf("remainder = %+v, want nil", remainder)
	}
}

func TestRankMoreThanTopNFoldsRestIntoRemainder(t *testing.T) {
	const extra = 5
	all := makeSlices(TopN+extra, 100)
	top, remainder := Rank(all)
	if len(top) != TopN {
		t.Fatalf("len(top) = %d, want %d", len(top), TopN)
	}
	if remainder == nil {
		t.Fatal("remainder = nil, want non-nil")
	}
	if remainder.Kind != KindRemainder {
		t.Errorf("remainder.Kind = %v, want KindRemainder", remainder.Kind)
	}
	if len(remainder.Children) != extra {
		t.Fatalf("len(remainder.Children) = %d, want %d", len(remainder.Children), extra)
	}

	var wantSum uint64
	for _, s := range remainder.Children {
		wantSum += s.Bytes
	}
	if remainder.Bytes != wantSum {
		t.Errorf("remainder.Bytes = %d, want %d (sum of children)", remainder.Bytes, wantSum)
	}
}

func TestRankSortsDescendingByBytes(t *testing.T) {
	all := []Slice{
		{Label: "small", Bytes: 10},
		{Label: "big", Bytes: 1000},
		{Label: "medium", Bytes: 500},
	}
	top, _ := Rank(all)
	if top[0].Label != "big" || top[1].Label != "medium" || top[2].Label != "small" {
		t.Fatalf("top = %+v, want big, medium, small order", top)
	}
}

func TestRankTiesBreakByLabelDeterministically(t *testing.T) {
	all := []Slice{
		{Label: "zebra", Bytes: 100},
		{Label: "apple", Bytes: 100},
		{Label: "mango", Bytes: 100},
	}
	// Run multiple times: since Rank always copies+sorts fresh, a stable
	// tiebreak should give the identical order every time regardless of
	// input order or how many times it's called.
	for range 5 {
		top, _ := Rank(all)
		if top[0].Label != "apple" || top[1].Label != "mango" || top[2].Label != "zebra" {
			t.Fatalf("top = %+v, want deterministic apple,mango,zebra order", top)
		}
	}
}

func TestRankDoesNotMutateInput(t *testing.T) {
	all := []Slice{
		{Label: "a", Bytes: 1},
		{Label: "b", Bytes: 2},
	}
	orig := append([]Slice(nil), all...)
	Rank(all)
	for i := range all {
		if all[i].Label != orig[i].Label || all[i].Bytes != orig[i].Bytes {
			t.Fatalf("Rank mutated its input: got %+v, want %+v", all, orig)
		}
	}
}

func TestDrillStackStartsAtDepthZero(t *testing.T) {
	all := makeSlices(3, 100)
	d := NewDrillStack(all)
	if d.Depth() != 0 {
		t.Fatalf("Depth() = %d, want 0", d.Depth())
	}
}

func TestDrillStackDrillIntoAndBack(t *testing.T) {
	// total -> top(TopN) + remainder(TopN+5) -> drill -> top(TopN) + remainder(5)
	total := TopN*2 + 5
	all := makeSlices(total, 0)
	d := NewDrillStack(all)

	view0 := d.Current()
	if view0.Remainder == nil {
		t.Fatalf("expected a remainder at depth 0 with %d slices", total)
	}

	ok := d.DrillInto(*view0.Remainder)
	if !ok {
		t.Fatal("DrillInto returned false, want true")
	}
	if d.Depth() != 1 {
		t.Fatalf("Depth() = %d, want 1", d.Depth())
	}
	view1 := d.Current()
	if len(view1.Top) != TopN {
		t.Fatalf("len(view1.Top) = %d, want %d", len(view1.Top), TopN)
	}
	if view1.Remainder == nil || len(view1.Remainder.Children) != 5 {
		t.Fatalf("view1.Remainder = %+v, want 5 children", view1.Remainder)
	}

	if !d.Back() {
		t.Fatal("Back() returned false, want true")
	}
	if d.Depth() != 0 {
		t.Fatalf("Depth() after Back() = %d, want 0", d.Depth())
	}
	// Backing out must restore the exact same (already-computed) view, not
	// a recomputation — compare by value.
	restored := d.Current()
	if len(restored.Top) != len(view0.Top) || restored.Remainder.Bytes != view0.Remainder.Bytes {
		t.Fatalf("restored view %+v != original view0 %+v", restored, view0)
	}
}

func TestDrillStackBackAtDepthZeroIsNoOp(t *testing.T) {
	d := NewDrillStack(makeSlices(3, 0))
	if d.Back() {
		t.Fatal("Back() at depth 0 returned true, want false (no-op)")
	}
	if d.Depth() != 0 {
		t.Fatalf("Depth() = %d, want 0", d.Depth())
	}
}

func TestDrillStackDrillIntoNonRemainderIsNoOp(t *testing.T) {
	d := NewDrillStack(makeSlices(3, 0))
	leaf := Slice{Label: "leaf", Bytes: 1, Kind: KindProcess} // no Children
	if d.DrillInto(leaf) {
		t.Fatal("DrillInto on a childless slice returned true, want false")
	}
	if d.Depth() != 0 {
		t.Fatalf("Depth() = %d, want 0 (no-op should not push a level)", d.Depth())
	}
}

func TestDrillStackMultiLevelDepth(t *testing.T) {
	// Build enough slices to require several drill levels: each level folds
	// everything past TopN into one remainder; drilling repeatedly should
	// let us reach depth > 1.
	// total -> top+remainder(2*TopN+5) -> top+remainder(TopN+5) -> top+remainder(5) -> top(5)+nil
	total := TopN*3 + 5
	all := makeSlices(total, 0)
	d := NewDrillStack(all)

	for depth := 1; depth <= 3; depth++ {
		view := d.Current()
		if view.Remainder == nil {
			t.Fatalf("expected remainder at depth %d", depth-1)
		}
		if !d.DrillInto(*view.Remainder) {
			t.Fatalf("DrillInto failed at depth %d", depth-1)
		}
		if d.Depth() != depth {
			t.Fatalf("Depth() = %d, want %d", d.Depth(), depth)
		}
	}

	final := d.Current()
	if final.Remainder != nil {
		t.Fatalf("final level should have no remainder (only 5 children left), got %+v", final.Remainder)
	}
	if len(final.Top) != 5 {
		t.Fatalf("len(final.Top) = %d, want 5", len(final.Top))
	}

	// Back out all the way and confirm depth 0 restores the true original.
	for d.Depth() > 0 {
		if !d.Back() {
			t.Fatal("Back() unexpectedly returned false before reaching depth 0")
		}
	}
	if d.Depth() != 0 {
		t.Fatalf("Depth() = %d, want 0 after backing out fully", d.Depth())
	}
}

func TestDrillStackResetDiscardsDrilledLevels(t *testing.T) {
	all := makeSlices(TopN+5, 0)
	d := NewDrillStack(all)
	view0 := d.Current()
	d.DrillInto(*view0.Remainder)
	if d.Depth() != 1 {
		t.Fatalf("Depth() = %d, want 1", d.Depth())
	}

	d.Reset(makeSlices(3, 0))
	if d.Depth() != 0 {
		t.Fatalf("Depth() after Reset = %d, want 0", d.Depth())
	}
	if len(d.Current().Top) != 3 {
		t.Fatalf("len(Current().Top) after Reset = %d, want 3", len(d.Current().Top))
	}
}

func TestViewAllOrdersTopThenRemainder(t *testing.T) {
	view := View{
		Top:       []Slice{{Label: "a", Bytes: 3}, {Label: "b", Bytes: 2}},
		Remainder: &Slice{Label: "Remainder", Bytes: 1, Kind: KindRemainder},
	}
	all := view.All()
	if len(all) != 3 {
		t.Fatalf("len(all) = %d, want 3", len(all))
	}
	if all[2].Label != "Remainder" {
		t.Fatalf("all[2].Label = %q, want Remainder", all[2].Label)
	}
}

func TestViewAllOmitsNilRemainder(t *testing.T) {
	view := View{Top: []Slice{{Label: "a", Bytes: 1}}}
	all := view.All()
	if len(all) != 1 {
		t.Fatalf("len(all) = %d, want 1", len(all))
	}
}

func TestGroupProcessesByExecMergesSameExec(t *testing.T) {
	all := []Slice{
		{Label: "brave [1]", Bytes: 100, Kind: KindProcess, Exec: "/usr/bin/brave"},
		{Label: "brave [2]", Bytes: 200, Kind: KindProcess, Exec: "/usr/bin/brave"},
		{Label: "brave [3]", Bytes: 50, Kind: KindProcess, Exec: "/usr/bin/brave"},
	}
	grouped := GroupProcessesByExec(all)
	if len(grouped) != 1 {
		t.Fatalf("len(grouped) = %d, want 1", len(grouped))
	}
	g := grouped[0]
	if g.Bytes != 350 {
		t.Errorf("Bytes = %d, want 350 (sum of all three)", g.Bytes)
	}
	if g.Label != "brave (3)" {
		t.Errorf("Label = %q, want %q", g.Label, "brave (3)")
	}
	if g.Kind != KindProcess {
		t.Errorf("Kind = %v, want KindProcess", g.Kind)
	}
}

func TestGroupProcessesByExecSingleProcessNoCountSuffix(t *testing.T) {
	all := []Slice{
		{Label: "sshd [1]", Bytes: 100, Kind: KindProcess, Exec: "/usr/sbin/sshd"},
	}
	grouped := GroupProcessesByExec(all)
	if len(grouped) != 1 {
		t.Fatalf("len(grouped) = %d, want 1", len(grouped))
	}
	if grouped[0].Label != "sshd" {
		t.Errorf("Label = %q, want %q (no count suffix for a single process)", grouped[0].Label, "sshd")
	}
}

func TestGroupProcessesByExecKeepsDifferentExecsSeparate(t *testing.T) {
	all := []Slice{
		{Label: "brave [1]", Bytes: 100, Kind: KindProcess, Exec: "/usr/bin/brave"},
		{Label: "chrome [2]", Bytes: 200, Kind: KindProcess, Exec: "/usr/bin/chrome"},
	}
	grouped := GroupProcessesByExec(all)
	if len(grouped) != 2 {
		t.Fatalf("len(grouped) = %d, want 2", len(grouped))
	}
}

func TestGroupProcessesByExecUsesBaseNameForLabel(t *testing.T) {
	all := []Slice{
		{Label: "brave [1]", Bytes: 100, Kind: KindProcess, Exec: "/opt/brave.org/brave"},
	}
	grouped := GroupProcessesByExec(all)
	if grouped[0].Label != "brave" {
		t.Errorf("Label = %q, want %q (base name of the exec path)", grouped[0].Label, "brave")
	}
}

func TestGroupProcessesByExecPassesThroughNonProcessSlices(t *testing.T) {
	all := []Slice{
		{Label: "Free", Bytes: 1000, Kind: KindCategory},
		{Label: "brave [1]", Bytes: 100, Kind: KindProcess, Exec: "/usr/bin/brave"},
		{Label: "brave [2]", Bytes: 100, Kind: KindProcess, Exec: "/usr/bin/brave"},
	}
	grouped := GroupProcessesByExec(all)
	if len(grouped) != 2 {
		t.Fatalf("len(grouped) = %d, want 2 (1 category + 1 merged process group)", len(grouped))
	}
	var sawCategory bool
	for _, g := range grouped {
		if g.Kind == KindCategory {
			sawCategory = true
			if g.Label != "Free" || g.Bytes != 1000 {
				t.Errorf("category slice altered: %+v", g)
			}
		}
	}
	if !sawCategory {
		t.Error("category slice was dropped")
	}
}

func TestGroupProcessesByExecFallsBackToCommWhenExecUnresolved(t *testing.T) {
	// procpss falls back to comm (no slashes) when /proc/<pid>/exe can't
	// be resolved — execBaseName must pass a slash-free string through
	// unchanged rather than mangling it.
	all := []Slice{
		{Label: "sh [1]", Bytes: 100, Kind: KindProcess, Exec: "sh"},
		{Label: "sh [2]", Bytes: 100, Kind: KindProcess, Exec: "sh"},
	}
	grouped := GroupProcessesByExec(all)
	if len(grouped) != 1 {
		t.Fatalf("len(grouped) = %d, want 1", len(grouped))
	}
	if grouped[0].Label != "sh (2)" {
		t.Errorf("Label = %q, want %q", grouped[0].Label, "sh (2)")
	}
}

func TestGroupProcessesByExecFallsBackToCommForVersionLikeBasename(t *testing.T) {
	// Real-world case: Claude Code's own binary lives at a path whose
	// filename is a bare version string ("2.1.220"), not a program name —
	// labeling the group "2.1.220" would be meaningless, so it should
	// fall back to comm ("claude") instead.
	all := []Slice{
		{Label: "claude [1]", Bytes: 100, Kind: KindProcess, Exec: "/home/david/.local/share/claude/versions/2.1.220", Comm: "claude"},
		{Label: "claude [2]", Bytes: 100, Kind: KindProcess, Exec: "/home/david/.local/share/claude/versions/2.1.220", Comm: "claude"},
	}
	grouped := GroupProcessesByExec(all)
	if len(grouped) != 1 {
		t.Fatalf("len(grouped) = %d, want 1", len(grouped))
	}
	if grouped[0].Label != "claude (2)" {
		t.Errorf("Label = %q, want %q", grouped[0].Label, "claude (2)")
	}
}

func TestGroupProcessesByExecKeepsRealNameEvenWithDigits(t *testing.T) {
	// python3.11 (letters mixed with digits/dots) is a real program name
	// and must not be mistaken for a bare version number.
	all := []Slice{
		{Label: "python3.11 [1]", Bytes: 100, Kind: KindProcess, Exec: "/usr/bin/python3.11", Comm: "python3.11"},
	}
	grouped := GroupProcessesByExec(all)
	if grouped[0].Label != "python3.11" {
		t.Errorf("Label = %q, want %q (a real name, not version-like)", grouped[0].Label, "python3.11")
	}
}

func TestGroupProcessesByExecVersionLikeFallsBackToExecBaseNameWithoutComm(t *testing.T) {
	// If Comm is empty (shouldn't normally happen, but don't crash or
	// produce a blank label), keep the version-like base name rather
	// than falling back to nothing.
	all := []Slice{
		{Label: "[1]", Bytes: 100, Kind: KindProcess, Exec: "/opt/app/versions/9.9.9"},
	}
	grouped := GroupProcessesByExec(all)
	if grouped[0].Label != "9.9.9" {
		t.Errorf("Label = %q, want %q (no comm to fall back to)", grouped[0].Label, "9.9.9")
	}
}

func TestLooksLikeVersion(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"2.1.220", true},
		{"1.0", true},
		{"v1.2.3", true},
		{"9", true},
		{"brave", false},
		{"python3.11", false},
		{"sh", false},
		{"", false},
		{"2.1.220-beta", false},
	}
	for _, c := range cases {
		if got := looksLikeVersion(c.s); got != c.want {
			t.Errorf("looksLikeVersion(%q) = %v, want %v", c.s, got, c.want)
		}
	}
}
