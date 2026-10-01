package forge

import (
	"strings"
	"testing"
)

// directive127 is go 1.27 as the components every pin is compared against.
var directive127 = []int{1, 27}

// Positive: every `||` alternative is judged, not only the first lower bound the text shows.
// A union admits whatever any alternative admits, so one stale, unbounded or unreadable
// alternative is the finding, wherever it stands.
func TestPinReason_Positive_JudgesEveryUnionAlternative(t *testing.T) {
	cases := map[string]ToolchainReason{
		">=1.28 || ~1.24": ToolchainRangeBelow,
		"~1.24 || >=1.28": ToolchainRangeBelow,
		"^1.27 || <1.20":  ToolchainUpperBoundOnly,
		"^1.27 || bogus":  ToolchainUnparseable,
		"1.26 <2":         ToolchainRangeBelow,
		"<=1.30":          ToolchainUpperBoundOnly,
		"oldstable":       ToolchainAlias,
	}
	for pin, want := range cases {
		if got := pinReason(pin, directive127); got != want {
			t.Errorf("pinReason(%q) = %q, want %q", pin, got, want)
		}
	}
}

// Negative: a range whose every alternative is held at or above the directive is not a
// finding, however its comparators are spaced. Within one alternative all comparators hold,
// so the highest lower bound is the one that counts.
func TestPinReason_Negative_AcceptsRangesHeldAtTheDirective(t *testing.T) {
	for _, pin := range []string{
		"^1.27 || ^1.28", ">=1.24 >=1.28", ">= 1.27", ">=\t1.27", ">=   1.27", "1.27 <2",
		">=1.28 <1.30 || >=1.27", "1.27.0 - 1.28.0", "=1.27", "stable", "1.27.x", ">=1.27.x",
	} {
		if got := pinReason(pin, directive127); got != pinSatisfies {
			t.Errorf("pinReason(%q) = %q, want no finding", pin, got)
		}
	}
}

// Boundary: a range up to maxRangeClauses alternatives, and an alternative up to
// maxRangeClauses comparators, is read whole; one more is reported unparseable rather than
// cut short, so a stale clause past the bound cannot pass as clean. A doubled operator, a
// dangling dash, an empty alternative and a version longer than the comparison holds are
// unreadable too, while a four-component version is still compared.
func TestPinReason_Boundary_RefusesRangesBeyondTheClauseBound(t *testing.T) {
	alternatives := strings.Repeat("^1.27 || ", maxRangeClauses-1) + "^1.27"
	comparators := strings.Repeat(">=1.27 ", maxRangeClauses-1) + ">=1.27"
	cases := map[string]ToolchainReason{
		alternatives:                   pinSatisfies,
		alternatives + " || ^1.20":     ToolchainUnparseable,
		comparators:                    pinSatisfies,
		comparators + " >=1.20":        ToolchainUnparseable,
		">= >= 1.27":                   ToolchainUnparseable,
		strings.Repeat("^1.27 || ", 2): ToolchainUnparseable,
		"1.27 - ":                      ToolchainUnparseable,
		"1.27.0.0":                     pinSatisfies,
		"1.27.0.0.0":                   ToolchainUnparseable,
		"^1.27.0.0.0":                  ToolchainUnparseable,
	}
	for pin, want := range cases {
		if got := pinReason(pin, directive127); got != want {
			t.Errorf("pinReason(%q) = %q, want %q", pin, got, want)
		}
	}
}
