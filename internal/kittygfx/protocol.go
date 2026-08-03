// Package kittygfx implements just enough of the kitty terminal graphics
// protocol (https://sw.kovidgoyal.net/kitty/graphics-protocol/) to transmit
// a rasterized RGBA image and place it at a specific screen cell each
// redraw tick, plus a capability probe (Detect). There is no mature,
// widely-used high-level Go library for this protocol, so it's implemented
// directly here rather than pulled in as a dependency.
package kittygfx

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
)

// chunkSize is the maximum number of base64 payload bytes per escape-code
// chunk. The protocol requires transmissions be split into chunks no
// larger than 4096 bytes of base64 data each.
const chunkSize = 4096

// Writer emits kitty graphics protocol escape sequences to a terminal.
type Writer struct {
	out io.Writer
	// closer is non-nil when Writer opened its own handle on /dev/tty and
	// is responsible for closing it.
	closer io.Closer
}

// NewWriter wraps an existing output handle (e.g. one already shared with
// the TUI layer) for emitting graphics protocol sequences.
func NewWriter(out io.Writer) *Writer {
	return &Writer{out: out}
}

// OpenTTYWriter opens /dev/tty directly for graphics output. Writing
// straight to /dev/tty (rather than os.Stdout) guarantees the bytes land on
// the actual controlling terminal even if a TUI library elsewhere holds its
// own separate handle to the same device — both refer to the same physical
// terminal, so sequential (non-concurrent) writes from each are safe and
// visible to each other. Call Close when done.
func OpenTTYWriter() (*Writer, error) {
	f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0)
	if err != nil {
		return nil, fmt.Errorf("kittygfx: open /dev/tty: %w", err)
	}
	return &Writer{out: f, closer: f}, nil
}

// Close releases any resources opened by OpenTTYWriter. It's a no-op for a
// Writer created with NewWriter.
func (w *Writer) Close() error {
	if w.closer != nil {
		return w.closer.Close()
	}
	return nil
}

// MoveCursor positions the terminal cursor at the given 0-indexed cell
// (col, row), which is where the next transmitted image will be anchored
// (kitty places images with their top-left corner at the current cursor
// cell).
func (w *Writer) MoveCursor(col, row int) error {
	_, err := fmt.Fprintf(w.out, "\x1b[%d;%dH", row+1, col+1)
	return err
}

// Delete removes a previously displayed image (and frees its terminal-side
// pixel data) by id. Callers should delete an id before re-transmitting at
// that id each redraw so that a shrinking image (e.g. after a terminal
// resize) doesn't leave stale pixels from the previous, larger placement
// peeking out from behind the new one.
func (w *Writer) Delete(id uint32) error {
	_, err := fmt.Fprintf(w.out, "\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", id)
	return err
}

// TransmitAndDisplay PNG-encodes img and sends it to the terminal as image
// id, displayed with its top-left corner at the cursor's current position
// (see MoveCursor). PNG is used over the wire (rather than raw RGBA/f=32)
// specifically to keep payload size down over slow links like SSH — these
// are mostly-flat/gradient area-fill graphs, which PNG compresses well.
//
// q=2 suppresses the terminal's success/failure acknowledgement, so it
// never ends up as stray bytes on stdin for the TUI's key-event reader to
// choke on; callers that want to detect a real transmission failure should
// rely on this returning an error from the write itself instead.
func (w *Writer) TransmitAndDisplay(id uint32, img image.Image) error {
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		return fmt.Errorf("kittygfx: png encode: %w", err)
	}

	b64 := base64.StdEncoding.EncodeToString(pngBuf.Bytes())

	if len(b64) <= chunkSize {
		_, err := fmt.Fprintf(w.out, "\x1b_Ga=T,f=100,i=%d,q=2;%s\x1b\\", id, b64)
		return err
	}

	for offset := 0; offset < len(b64); offset += chunkSize {
		end := min(offset+chunkSize, len(b64))
		chunk := b64[offset:end]
		more := 1
		if end == len(b64) {
			more = 0
		}

		var err error
		if offset == 0 {
			_, err = fmt.Fprintf(w.out, "\x1b_Ga=T,f=100,i=%d,q=2,m=%d;%s\x1b\\", id, more, chunk)
		} else {
			_, err = fmt.Fprintf(w.out, "\x1b_Gm=%d;%s\x1b\\", more, chunk)
		}
		if err != nil {
			return fmt.Errorf("kittygfx: write chunk at offset %d: %w", offset, err)
		}
	}
	return nil
}
