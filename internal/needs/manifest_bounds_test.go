package needs

import (
	"bufio"
	"strings"
	"testing"
)

func TestScanBoundedLines(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		visited int
		wantErr bool
	}{
		{"empty", "", 0, false},
		{"raw line", "  require example.com/mod v1.0.0  \n", 1, false},
		{"at limit", strings.Repeat("x\n", MaxScannedLines), MaxScannedLines, false},
		{"over limit", strings.Repeat("x\n", MaxScannedLines+1), MaxScannedLines, true},
		{"oversize token", strings.Repeat("x", bufio.MaxScanTokenSize+1), 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			visited := 0
			err := scanBoundedLines(bufio.NewScanner(strings.NewReader(tc.input)), func(line string) {
				visited++
				if tc.name == "raw line" && line != strings.TrimSuffix(tc.input, "\n") {
					t.Errorf("line was altered: %q", line)
				}
			})
			if (err != nil) != tc.wantErr || visited != tc.visited {
				t.Fatalf("visited %d, err %v; want visited %d, error %v", visited, err, tc.visited, tc.wantErr)
			}
		})
	}
}
