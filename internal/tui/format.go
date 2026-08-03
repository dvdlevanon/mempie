package tui

import "fmt"

// formatBytes renders a byte count as a short human-readable string using
// binary (1024-based) units labeled KB/MB/GB/... — the same convention
// tools like `du`/`ps`/`top` use, not decimal SI units. This is a
// deliberate, documented choice (see README) rather than an oversight.
func formatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}

	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	units := [...]string{"KB", "MB", "GB", "TB", "PB", "EB"}
	return fmt.Sprintf("%.1f %s", float64(b)/float64(div), units[exp])
}
