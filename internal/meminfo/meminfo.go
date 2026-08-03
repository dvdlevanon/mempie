// Package meminfo parses /proc/meminfo and turns a fixed subset of its
// fields into the "kernel/system category" slices mempie's pie chart shows
// alongside per-process PSS slices (see internal/procpss). The category
// list and the meminfo fields each draws from are fixed by the tool's
// spec — not derived or user-configurable.
package meminfo

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Fields holds every parsed /proc/meminfo line, keyed by its field name
// (without the trailing colon). A field written with a "kB" unit in
// /proc/meminfo is stored here already converted to bytes; a field with no
// unit (the HugePages_* counters) is stored as the raw integer /proc/meminfo
// printed. This asymmetry is deliberate and is exactly what Categories
// needs: HugePages_Total (a raw count) times Hugepagesize (auto-converted
// to bytes) is already the total huge-page byte count, with no separate
// unit bookkeeping required at the call site.
type Fields map[string]uint64

// Parse reads /proc/meminfo-formatted text (either "Key:    value kB" or
// "Key:    value" with no unit) into Fields. Malformed lines are skipped
// rather than treated as fatal — /proc/meminfo's format has stayed stable
// for decades, but skipping forward is cheap insurance against a stray
// future field this parser doesn't expect.
func Parse(r io.Reader) (Fields, error) {
	fields := make(Fields)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		key, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		parts := strings.Fields(rest)
		if len(parts) == 0 {
			continue
		}
		value, err := strconv.ParseUint(parts[0], 10, 64)
		if err != nil {
			continue
		}
		if len(parts) >= 2 && parts[1] == "kB" {
			value *= 1024
		}
		fields[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("meminfo: scan: %w", err)
	}
	return fields, nil
}

// ReadFields reads and parses /proc/meminfo.
func ReadFields() (Fields, error) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return nil, fmt.Errorf("meminfo: open: %w", err)
	}
	defer f.Close()
	return Parse(f)
}

// Category is one fixed kernel/system memory category: a byte size derived
// from one or more meminfo fields.
type Category struct {
	Label string
	Bytes uint64
}

// Categories builds the fixed category list from parsed meminfo fields.
// HugePages is only included when HugePages_Total > 0, per spec — an idle
// machine with no huge pages configured shouldn't get a permanent
// zero-byte slice cluttering the ranking.
func Categories(f Fields) []Category {
	cats := []Category{
		{"Free", f["MemFree"]},
		{"Cache", f["Cached"] + f["Buffers"]},
		{"Shmem", f["Shmem"]},
		{"Slab (reclaimable)", f["SReclaimable"]},
		{"Slab (unreclaimable)", f["SUnreclaim"]},
		{"KernelStack", f["KernelStack"]},
		{"PageTables", f["PageTables"]},
		{"Percpu", f["Percpu"]},
		{"VmallocUsed", f["VmallocUsed"]},
		{"SwapCached", f["SwapCached"]},
	}
	if total := f["HugePages_Total"]; total > 0 {
		cats = append(cats, Category{"HugePages", total * f["Hugepagesize"]})
	}
	return cats
}

// ReadCategories reads /proc/meminfo and returns the fixed category list.
func ReadCategories() ([]Category, error) {
	fields, err := ReadFields()
	if err != nil {
		return nil, err
	}
	return Categories(fields), nil
}
