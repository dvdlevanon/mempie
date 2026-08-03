package procpss

import "testing"

const rollupFixture = `56414d940000-7ffff7478000 ---p 00000000 00:00 0                          [rollup]
Rss:                4192 kB
Pss:                 188 kB
Pss_Dirty:           124 kB
Pss_Anon:            124 kB
Pss_File:             64 kB
Pss_Shmem:             0 kB
Shared_Clean:       4020 kB
`

func TestParsePSS(t *testing.T) {
	bytes, ok := parsePSS(rollupFixture)
	if !ok {
		t.Fatal("parsePSS returned ok=false, want true")
	}
	if want := uint64(188 * 1024); bytes != want {
		t.Errorf("bytes = %d, want %d", bytes, want)
	}
}

func TestParsePSSDoesNotMatchPssDirtyPrefix(t *testing.T) {
	// Regression guard: a naive "starts with Pss" match would pick up
	// Pss_Dirty (124 kB) instead of Pss (188 kB) if Pss_Dirty happened to
	// sort first, or if the exact-prefix check were loosened.
	bytes, ok := parsePSS(rollupFixture)
	if !ok {
		t.Fatal("parsePSS returned ok=false")
	}
	if bytes == 124*1024 {
		t.Error("parsePSS matched Pss_Dirty instead of Pss")
	}
}

func TestParsePSSMissingField(t *testing.T) {
	_, ok := parsePSS("Rss:  100 kB\n")
	if ok {
		t.Error("parsePSS with no Pss line should return ok=false")
	}
}

func TestParsePSSMalformedValue(t *testing.T) {
	_, ok := parsePSS("Pss: not-a-number kB\n")
	if ok {
		t.Error("parsePSS with a malformed value should return ok=false")
	}
}

func TestParsePSSEmptyInput(t *testing.T) {
	_, ok := parsePSS("")
	if ok {
		t.Error("parsePSS on empty input should return ok=false")
	}
}
