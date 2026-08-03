// Command mempie is an interactive, kitty-terminal-only pie chart of
// physical RAM usage, split across processes (by PSS) and fixed
// kernel/system categories (from /proc/meminfo), with a drillable
// "Remainder" slice for everything past the top N (see
// internal/memslice.TopN).
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"mempie/internal/kittygfx"
	"mempie/internal/tui"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "mempie:", err)
		os.Exit(1)
	}
}

func run() error {
	refresh := flag.Duration("refresh", 10*time.Second, "refresh interval (e.g. 5s, 10s, 1m)")
	flag.Parse()

	if *refresh <= 0 {
		return fmt.Errorf("-refresh must be positive, got %s", refresh)
	}

	// Detect must run before tcell.Screen.Init() (inside tui.NewApp) takes
	// over the terminal — it needs its own brief raw-mode read of the
	// terminal's reply to a capability query. mempie has no non-kitty
	// fallback renderer, so a negative result is a fatal startup error,
	// not a degrade path.
	if !kittygfx.Detect() {
		return fmt.Errorf("kitty graphics protocol not detected on this terminal — mempie requires a kitty-compatible terminal (e.g. kitty itself, or another emulator implementing its graphics protocol) and won't run without one")
	}

	kw, err := kittygfx.OpenTTYWriter()
	if err != nil {
		return fmt.Errorf("open terminal for graphics output: %w", err)
	}
	defer kw.Close()

	app, err := tui.NewApp(kw, *refresh)
	if err != nil {
		return fmt.Errorf("start UI: %w", err)
	}
	return app.Run()
}
