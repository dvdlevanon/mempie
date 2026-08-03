package tui

import "testing"

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		bytes uint64
		want  string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{412 * 1024 * 1024, "412.0 MB"},
		{uint64(1.5 * 1024 * 1024 * 1024), "1.5 GB"},
		{32 * 1024 * 1024 * 1024, "32.0 GB"},
	}
	for _, c := range cases {
		if got := formatBytes(c.bytes); got != c.want {
			t.Errorf("formatBytes(%d) = %q, want %q", c.bytes, got, c.want)
		}
	}
}
