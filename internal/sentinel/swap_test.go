// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package sentinel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Positive: SwapTotal and SwapFree are read in bytes, beside the memory fields.
func TestParseSwap_Positive_ReadsBothFields(t *testing.T) {
	total, free, measured, err := parseSwap(strings.NewReader("MemTotal: 64 kB\nSwapTotal: 2048 kB\nSwapFree: 1024 kB\n"))
	if err != nil || !measured || total != 2048*1024 || free != 1024*1024 {
		t.Fatalf("total %d, free %d, measured %v, err %v", total, free, measured, err)
	}
}

// Negative: a meminfo without SwapTotal is no swap reading, and an absent meminfo is unmeasured
// rather than an error or an invented figure.
func TestReadMeminfoSwap_Negative_NoSourceIsUnmeasured(t *testing.T) {
	if _, _, measured, err := parseSwap(strings.NewReader("MemTotal: 64 kB\nSwapFree: 1024 kB\n")); err != nil || measured {
		t.Fatalf("meminfo without SwapTotal: measured %v, err %v", measured, err)
	}
	original := meminfoPath
	t.Cleanup(func() { meminfoPath = original })
	meminfoPath = filepath.Join(t.TempDir(), "absent-meminfo")
	total, free, measured, err := readMeminfoSwap()
	if err != nil || measured || total != 0 || free != 0 {
		t.Fatalf("absent meminfo: total %d, free %d, measured %v, err %v", total, free, measured, err)
	}
}

// Boundary: a host with no swap reports SwapTotal 0, which is a measurement, and the real source
// still parses where it exists.
func TestReadMeminfoSwap_Boundary_NoSwapIsMeasured(t *testing.T) {
	total, free, measured, err := parseSwap(strings.NewReader("SwapTotal: 0 kB\nSwapFree: 0 kB\n"))
	if err != nil || !measured || total != 0 || free != 0 {
		t.Fatalf("no swap: total %d, free %d, measured %v, err %v", total, free, measured, err)
	}
	if _, err := os.Stat(defaultMeminfoPath); err != nil {
		t.Skip("no /proc/meminfo on this host")
	}
	if _, _, measured, err := readMeminfoSwap(); err != nil || !measured {
		t.Fatalf("real meminfo: measured %v, err %v", measured, err)
	}
}
