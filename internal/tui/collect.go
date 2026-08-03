package tui

import (
	"fmt"

	"mempie/internal/meminfo"
	"mempie/internal/memslice"
	"mempie/internal/procpss"
)

// collectResult is one completed refresh cycle's data, sent back from the
// background collection goroutine over a channel — see App.Run.
// totalBytes/usedBytes are MemTotal/(MemTotal-MemFree), shown for context
// in the bottom status bar; neither plays any part in the ranking/
// remainder logic.
type collectResult struct {
	slices     []memslice.Slice
	totalBytes uint64
	usedBytes  uint64
	err        error
}

// collectSlices builds the flat list of memory slices for one refresh
// cycle: one slice per fixed /proc/meminfo category, plus one per process
// (sized by PSS). It also returns MemTotal/used for the bottom status bar.
func collectSlices() collectResult {
	fields, err := meminfo.ReadFields()
	if err != nil {
		return collectResult{err: fmt.Errorf("read /proc/meminfo: %w", err)}
	}

	var slices []memslice.Slice
	for _, c := range meminfo.Categories(fields) {
		slices = append(slices, memslice.Slice{
			Label: c.Label,
			Bytes: c.Bytes,
			Kind:  memslice.KindCategory,
		})
	}

	procs, err := procpss.Collect()
	if err != nil {
		return collectResult{err: fmt.Errorf("collect process PSS: %w", err)}
	}
	for _, p := range procs {
		slices = append(slices, memslice.Slice{
			Label: fmt.Sprintf("%s [%d]", p.Comm, p.PID),
			Bytes: p.PSSBytes,
			Kind:  memslice.KindProcess,
			Exec:  p.Exe,
			Comm:  p.Comm,
		})
	}

	total := fields["MemTotal"]
	used := total - fields["MemFree"]
	return collectResult{slices: slices, totalBytes: total, usedBytes: used}
}
