package meminfo

import (
	"strings"
	"testing"
)

const fixture = `MemTotal:       32782728 kB
MemFree:         3191072 kB
MemAvailable:    8055648 kB
Buffers:          563856 kB
Cached:          4840624 kB
SwapCached:            0 kB
Shmem:            123456 kB
SReclaimable:     234567 kB
SUnreclaim:       345678 kB
KernelStack:       45056 kB
PageTables:        67890 kB
Percpu:             8192 kB
VmallocUsed:       12345 kB
HugePages_Total:       4
HugePages_Free:        4
HugePages_Rsvd:        0
HugePages_Surp:        0
Hugepagesize:       2048 kB
Hugetlb:               0 kB
`

func TestParse(t *testing.T) {
	f, err := Parse(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// kB fields must be converted to bytes.
	if got, want := f["MemFree"], uint64(3191072*1024); got != want {
		t.Errorf("MemFree = %d, want %d", got, want)
	}
	if got, want := f["Cached"], uint64(4840624*1024); got != want {
		t.Errorf("Cached = %d, want %d", got, want)
	}

	// Unitless fields must be stored raw (not multiplied).
	if got, want := f["HugePages_Total"], uint64(4); got != want {
		t.Errorf("HugePages_Total = %d, want %d", got, want)
	}

	// Hugepagesize does carry a kB unit and must be converted, since
	// Categories relies on HugePages_Total * Hugepagesize already being
	// bytes with no further conversion.
	if got, want := f["Hugepagesize"], uint64(2048*1024); got != want {
		t.Errorf("Hugepagesize = %d, want %d", got, want)
	}
}

func TestParseSkipsMalformedLines(t *testing.T) {
	input := "NotAKeyValue\nMemFree: not-a-number kB\nMemFree:      100 kB\n"
	f, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, want := f["MemFree"], uint64(100*1024); got != want {
		t.Errorf("MemFree = %d, want %d (malformed line should be skipped, not fatal)", got, want)
	}
}

func TestCategoriesCacheCombinesBuffersAndCached(t *testing.T) {
	f, err := Parse(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cats := Categories(f)

	byLabel := make(map[string]Category)
	for _, c := range cats {
		byLabel[c.Label] = c
	}

	cache, ok := byLabel["Cache"]
	if !ok {
		t.Fatal("Cache category missing")
	}
	want := f["Cached"] + f["Buffers"]
	if cache.Bytes != want {
		t.Errorf("Cache.Bytes = %d, want %d (Cached+Buffers)", cache.Bytes, want)
	}
}

func TestCategoriesHugePagesIncludedWhenNonzero(t *testing.T) {
	f, err := Parse(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cats := Categories(f)

	var found *Category
	for i := range cats {
		if cats[i].Label == "HugePages" {
			found = &cats[i]
		}
	}
	if found == nil {
		t.Fatal("HugePages category should be present when HugePages_Total > 0")
	}
	want := f["HugePages_Total"] * f["Hugepagesize"]
	if found.Bytes != want {
		t.Errorf("HugePages.Bytes = %d, want %d", found.Bytes, want)
	}
}

func TestCategoriesHugePagesOmittedWhenZero(t *testing.T) {
	input := strings.ReplaceAll(fixture, "HugePages_Total:       4", "HugePages_Total:       0")
	f, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cats := Categories(f)
	for _, c := range cats {
		if c.Label == "HugePages" {
			t.Error("HugePages category should be omitted when HugePages_Total == 0")
		}
	}
}

func TestCategoriesCount(t *testing.T) {
	f, err := Parse(strings.NewReader(fixture))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cats := Categories(f)
	if len(cats) != 11 {
		t.Fatalf("len(cats) = %d, want 11 (10 always-present + HugePages)", len(cats))
	}
}
