// Package procpss reads each process's PSS (Proportional Set Size) from
// /proc/<pid>/smaps_rollup. PSS is the deliberate choice for per-process
// memory slices: it splits shared pages (shared libs, etc.) proportionally
// across every process mapping them, so the numbers add up to something
// meaningful — at the cost of double-counting against the fixed
// /proc/meminfo-derived categories (e.g. a tmpfs page counted in both a
// process's PSS and the Shmem category). That tradeoff is accepted, not a
// bug — see the README.
//
// Reading another user's smaps_rollup requires root; this package assumes
// it's running as root and doesn't attempt any permission-fallback logic.
// A process whose smaps_rollup can't be read (raced exit, insufficient
// privilege, etc.) is silently skipped for that refresh cycle rather than
// treated as an error, since that's an expected, constant condition on a
// live system, not a one-off failure worth surfacing.
package procpss

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Process is one process's identity and current PSS.
type Process struct {
	PID      int
	Comm     string
	Exe      string // resolved /proc/<pid>/exe target; falls back to Comm if unreadable — see readExe
	PSSBytes uint64
}

// Collect enumerates every numeric /proc/<pid> directory and returns a
// Process for each one whose smaps_rollup could be read. It returns an
// error only if /proc itself can't be listed — a single process's read
// failure is expected and handled by omission, not surfaced here.
func Collect() ([]Process, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("procpss: read /proc: %w", err)
	}

	procs := make([]Process, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue // not a pid directory (self, net, sys, ...)
		}
		pss, ok := readPSS(pid)
		if !ok {
			continue
		}
		comm := readComm(pid)
		procs = append(procs, Process{
			PID:      pid,
			Comm:     comm,
			Exe:      readExe(pid, comm),
			PSSBytes: pss,
		})
	}
	return procs, nil
}

// readPSS reads the Pss field (in kB, converted to bytes) out of one
// process's smaps_rollup. ok is false if the file can't be read (the
// process may have already exited).
func readPSS(pid int) (bytes uint64, ok bool) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/smaps_rollup", pid))
	if err != nil {
		return 0, false
	}
	return parsePSS(string(data))
}

// parsePSS extracts the Pss field (in kB, converted to bytes) out of
// smaps_rollup content. Split out from readPSS so it's unit-testable
// against a synthetic fixture without needing a real /proc.
func parsePSS(smapsRollup string) (bytes uint64, ok bool) {
	for line := range strings.SplitSeq(smapsRollup, "\n") {
		// "Pss:" (not "Pss_Dirty:"/"Pss_Anon:"/etc — those share the
		// "Pss" prefix but not the exact "Pss:" field name).
		rest, found := strings.CutPrefix(line, "Pss:")
		if !found {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return 0, false
		}
		kb, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return 0, false
		}
		return kb * 1024, true
	}
	return 0, false
}

// readComm reads a process's command name. If it can't be read (raced
// exit between enumeration and this read), a placeholder derived from the
// pid is used instead so the process still gets a legible label rather
// than being dropped — the pid was already confirmed to have a readable
// smaps_rollup a moment ago, so an outright drop here would be surprising.
func readComm(pid int) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		return fmt.Sprintf("pid %d", pid)
	}
	return strings.TrimSpace(string(data))
}

// readExe resolves the /proc/<pid>/exe symlink to the process's full
// executable path — used as the grouping key for "group by exec path"
// (see internal/memslice.GroupProcessesByExec), since it's more precise
// than comm (two unrelated binaries could coincidentally share a 15-char
// truncated comm name, but never the same exe path). Falls back to comm
// if the symlink can't be read (permission edge case, or the process
// exiting between enumeration and this read) so grouping still degrades
// gracefully rather than losing the process from its group entirely.
func readExe(pid int, comm string) string {
	target, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return comm
	}
	return target
}
