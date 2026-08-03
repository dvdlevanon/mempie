package kittygfx

import (
	"bytes"
	"os"
	"time"

	"golang.org/x/term"
)

// queryImageID is an arbitrary, fixed image id used only for the
// capability probe in Detect, chosen to be unlikely to collide with any id
// a real caller picks for actual graph placements.
const queryImageID = 999999

// Detect reports whether the terminal mempie is attached to actually
// understands the kitty graphics protocol. It does this by sending a
// real 1x1-pixel query escape sequence and waiting briefly for the
// terminal's reply — not by trusting $TERM or $KITTY_WINDOW_ID, since
// those say nothing about whether the protocol is actually reaching the
// terminal (e.g. it's been stripped somewhere in an SSH/multiplexer
// chain). If nothing (or garbage) comes back within the timeout, Detect
// returns false. mempie has no non-kitty fallback renderer — the caller
// should treat false as a fatal startup error, not a degrade path.
//
// Detect must be called before the TUI takes over stdin for its own input
// handling (i.e. before tcell's Screen.Init), since it briefly puts the
// terminal into raw mode itself to read the reply byte-for-byte.
func Detect() bool {
	if !term.IsTerminal(int(os.Stdout.Fd())) || !term.IsTerminal(int(os.Stdin.Fd())) {
		return false // not an interactive terminal at all (piped/redirected)
	}

	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return false
	}
	defer tty.Close()

	fd := int(tty.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return false
	}
	defer term.Restore(fd, oldState)

	// a=q: query only, don't actually display or retain anything.
	// t=d,f=24,s=1,v=1: a trivial 1x1 24-bit-RGB direct payload (one
	// black pixel, base64 of 3 zero bytes).
	// i=<queryImageID>: lets us recognize our own reply.
	query := "\x1b_Gi=" + itoa(queryImageID) + ",s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\"
	if _, err := tty.WriteString(query); err != nil {
		return false
	}

	deadline := time.Now().Add(300 * time.Millisecond)
	if err := tty.SetReadDeadline(deadline); err != nil {
		// Deadlines aren't supported on this fd/platform; safer to assume
		// no kitty graphics support than to risk hanging forever.
		return false
	}
	defer tty.SetReadDeadline(time.Time{})

	want := []byte("_Gi=" + itoa(queryImageID) + ";OK")
	var buf bytes.Buffer
	tmp := make([]byte, 256)
	for time.Now().Before(deadline) {
		n, err := tty.Read(tmp)
		if n > 0 {
			buf.Write(tmp[:n])
			if bytes.Contains(buf.Bytes(), want) {
				return true
			}
		}
		if err != nil {
			break
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}
