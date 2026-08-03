package tui

import (
	"os"

	"golang.org/x/sys/unix"
)

// cellPixelSize queries the controlling terminal's TIOCGWINSZ ioctl to
// determine how many screen pixels each terminal cell occupies. tcell's own
// Screen.Size() only reports the size in character cells; TIOCGWINSZ is the
// one portable-on-Linux way to also learn the pixel dimensions, which is
// what lets the kitty graphics backend size its raster to fill exactly the
// cells it's given rather than guessing.
//
// ok is false if the ioctl fails, or if the terminal reports zero pixel
// dimensions (some multiplexers/terminals leave Xpixel/Ypixel unset even
// while otherwise forwarding kitty graphics escapes correctly) — mempie has
// no fallback for this case, so the caller should treat it as fatal.
func cellPixelSize() (cellW, cellH float64, ok bool) {
	tty, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	if err != nil {
		return 0, 0, false
	}
	defer tty.Close()

	ws, err := unix.IoctlGetWinsize(int(tty.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return 0, 0, false
	}
	if ws.Col == 0 || ws.Row == 0 || ws.Xpixel == 0 || ws.Ypixel == 0 {
		return 0, 0, false
	}
	return float64(ws.Xpixel) / float64(ws.Col), float64(ws.Ypixel) / float64(ws.Row), true
}
