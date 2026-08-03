// Package memslice holds the data model shared by every memory "slice"
// mempie draws — one process's PSS or one fixed kernel/system category —
// plus the sort/rank/remainder logic and the drill-down stack that lets the
// UI descend into (and back out of) a Remainder slice.
package memslice

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Kind distinguishes what a Slice represents, which in turn drives what
// interaction it supports: a Remainder is the only kind that's drillable: a
// Process is always a leaf (see spec: "no process-level drill-down"), and a
// Category is a leaf too — only a Remainder ever carries Children to
// recurse into.
type Kind int

const (
	KindCategory Kind = iota
	KindProcess
	KindRemainder
)

// Slice is one wedge of the pie: a byte size and a label, plus kind-specific
// extras. Exec and Comm are only meaningful for KindProcess: Exec is the
// process's resolved executable path (or its comm name as a fallback —
// see internal/procpss) and is the grouping key GroupProcessesByExec
// merges on; Comm is the process's raw comm name, kept separately so
// GroupProcessesByExec can fall back to it for display when Exec's base
// name doesn't look like a real program name (see looksLikeVersion).
// Children is only meaningful for KindRemainder: the slices folded into
// it, kept around so drilling in doesn't need to recompute anything the
// caller already knows.
type Slice struct {
	Label    string
	Bytes    uint64
	Kind     Kind
	Exec     string
	Comm     string
	Children []Slice
}

// TopN is how many individual slices are shown per drill level before the
// rest are folded into a Remainder slice.
const TopN = 13

// Rank sorts all in descending order by Bytes and splits it into the top
// TopN individual slices plus, if more than TopN remain, a synthetic
// Remainder slice summing the rest (with Children set to those folded-in
// slices, themselves sorted, ready for a later Rank call if the user drills
// into it). remainder is nil when len(all) <= TopN — there's nothing left
// to fold.
//
// Ties are broken by Label after Bytes, giving every input a full, strict
// total order: sort.SliceStable alone isn't enough, since the slice being
// sorted is typically built from map iteration (process enumeration) or
// otherwise carries no inherent original order, so two same-sized entries
// could otherwise silently swap places between calls.
func Rank(all []Slice) (top []Slice, remainder *Slice) {
	sorted := make([]Slice, len(all))
	copy(sorted, all)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Bytes != sorted[j].Bytes {
			return sorted[i].Bytes > sorted[j].Bytes
		}
		return sorted[i].Label < sorted[j].Label
	})

	if len(sorted) <= TopN {
		return sorted, nil
	}

	top = sorted[:TopN]
	rest := sorted[TopN:]

	var sum uint64
	for _, s := range rest {
		sum += s.Bytes
	}
	remainder = &Slice{
		Label:    "Remainder",
		Bytes:    sum,
		Kind:     KindRemainder,
		Children: rest,
	}
	return top, remainder
}

// GroupProcessesByExec merges every KindProcess slice sharing the same
// Exec (executable path, or comm name as a fallback — see
// internal/procpss.readExe) into a single slice summing their bytes, so
// e.g. a dozen separate "brave [pid]" slices collapse into one "brave
// (12)" slice. Category and Remainder slices pass through unchanged —
// grouping only ever applies to processes.
//
// A grouped slice's Label is normally the exec's base name (the last
// path component, or the whole string if it wasn't a path). Some tools
// install their real binary at a path whose filename is a bare version
// string rather than a program name (seen in the wild: Claude Code's own
// binary lives at .../versions/2.1.220, no other name) — labeling a
// slice "2.1.220" is meaningless on its own, so when the base name looks
// like a version number (see looksLikeVersion), the label falls back to
// the process's comm instead (still the more precise Exec path as the
// grouping key, just a more legible label). A "(N)" suffix is added once
// more than one process shared the group; a single ungrouped process
// keeps a bare name, no "(1)" suffix.
//
// Callers apply this to the flat slice list *before* Rank/DrillStack —
// grouping changes what "top TopN" even means, so it has to happen ahead
// of ranking, not as a post-processing step on an already-ranked view.
func GroupProcessesByExec(all []Slice) []Slice {
	out := make([]Slice, 0, len(all))

	type group struct {
		bytes uint64
		count int
		index int // position of this group's placeholder in out
		comm  string
	}
	groups := make(map[string]*group)

	for _, s := range all {
		if s.Kind != KindProcess {
			out = append(out, s)
			continue
		}
		g, ok := groups[s.Exec]
		if !ok {
			out = append(out, Slice{Kind: KindProcess, Exec: s.Exec})
			g = &group{index: len(out) - 1, comm: s.Comm}
			groups[s.Exec] = g
		}
		g.bytes += s.Bytes
		g.count++
		out[g.index].Bytes = g.bytes
	}

	for _, g := range groups {
		label := execBaseName(out[g.index].Exec)
		if looksLikeVersion(label) && g.comm != "" {
			label = g.comm
		}
		if g.count > 1 {
			label = fmt.Sprintf("%s (%d)", label, g.count)
		}
		out[g.index].Label = label
	}

	return out
}

// execBaseName trims a full exec path down to its last component for
// display (e.g. "/usr/bin/brave" -> "brave"); a bare comm-name fallback
// (no slashes) passes through unchanged.
func execBaseName(exec string) string {
	if i := strings.LastIndexByte(exec, '/'); i >= 0 {
		return exec[i+1:]
	}
	return exec
}

// versionLikeRE matches a bare version number, optionally "v"-prefixed:
// "2.1.220", "1.0", "v3.14". Deliberately doesn't match anything with
// letters mixed in (e.g. "python3.11") — those still read as a real
// program name and shouldn't fall back to comm.
var versionLikeRE = regexp.MustCompile(`^v?[0-9]+(\.[0-9]+)*$`)

// looksLikeVersion reports whether s looks like a version number rather
// than a real program name.
func looksLikeVersion(s string) bool {
	return versionLikeRE.MatchString(s)
}

// View is one drill level's already-computed, ranked slice set: the top
// TopN individual slices plus (if applicable) the Remainder folding in
// everything past TopN.
type View struct {
	Top       []Slice
	Remainder *Slice
}

// All returns Top followed by Remainder (if present) as one slice, in the
// order they should be shown/navigated in the pie chart.
func (v View) All() []Slice {
	out := make([]Slice, 0, len(v.Top)+1)
	out = append(out, v.Top...)
	if v.Remainder != nil {
		out = append(out, *v.Remainder)
	}
	return out
}

// DrillStack tracks the stack of drill levels the user has descended
// through: index 0 is always the top-level view (all processes + fixed
// categories); each further entry is one more Remainder drilled into.
// Depth 10-20 is expected on a many-process machine, so this is a plain
// growable slice of already-computed Views, not a fixed-depth structure.
type DrillStack struct {
	views []View
}

// NewDrillStack builds a stack starting at the top-level view over all.
func NewDrillStack(all []Slice) *DrillStack {
	d := &DrillStack{}
	d.Reset(all)
	return d
}

// Reset recomputes the top-level view from a fresh slice list and discards
// any drilled-in levels — used on each live refresh, which per spec only
// ever happens while at depth 0.
func (d *DrillStack) Reset(all []Slice) {
	top, remainder := Rank(all)
	d.views = []View{{Top: top, Remainder: remainder}}
}

// Current returns the view at the current drill depth.
func (d *DrillStack) Current() View {
	return d.views[len(d.views)-1]
}

// Depth returns how many levels have been drilled into; 0 is top-level.
func (d *DrillStack) Depth() int {
	return len(d.views) - 1
}

// DrillInto descends one level into remainder's folded-in children,
// pushing a newly-ranked view onto the stack. It reports false (a no-op)
// if remainder has no children to descend into.
func (d *DrillStack) DrillInto(remainder Slice) bool {
	if len(remainder.Children) == 0 {
		return false
	}
	top, next := Rank(remainder.Children)
	d.views = append(d.views, View{Top: top, Remainder: next})
	return true
}

// Back pops one drill level, restoring the previous level's already-
// computed view rather than recomputing anything. It reports false (a
// no-op) if already at depth 0.
func (d *DrillStack) Back() bool {
	if len(d.views) <= 1 {
		return false
	}
	d.views = d.views[:len(d.views)-1]
	return true
}
