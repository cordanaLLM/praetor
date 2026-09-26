package adr

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// forbiddenSample returns a tracked path a forbidden-path pattern is meant to catch: a file
// inside a directory pattern, the file itself otherwise, with any glob star filled in.
func forbiddenSample(pattern string) string {
	sample := strings.ReplaceAll(pattern, "*", "x")
	if strings.HasSuffix(sample, "/") {
		sample += "sample.yaml"
	}
	return sample
}

// Every forbidden-path pattern a shipped decision record declares must be able to fire. A
// pattern no path can match (a typo, a backslash, a stray anchor) reports the decision as
// enforced while nothing could ever trip it. `praetorctl adr verify` only shows the clean
// direction on this repository; this replays the other one for each shipped pattern.
func TestShippedForbiddenPathPatternsFire(t *testing.T) {
	constraints, records, err := loadConstraints(context.Background(), filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("loading shipped decision records: %v", err)
	}
	if records == 0 {
		t.Fatal("no shipped decision records were read")
	}
	checked := 0
	for _, constraint := range constraints {
		if constraint.Kind != KindForbiddenPath {
			continue
		}
		for _, pattern := range constraint.Forbids {
			single := constraint
			single.Forbids = []string{pattern}
			// Positive: the path the pattern names is reported.
			sample := forbiddenSample(pattern)
			if got := checkConstraint(single, map[string]string{sample: ""}); len(got) != 1 {
				t.Errorf("%s [%s]: pattern %q does not fire on %q", constraint.Record, constraint.ID, pattern, sample)
			}
			// Boundary: a sibling file that only shares the directory's name is not.
			if strings.HasSuffix(pattern, "/") {
				sibling := strings.TrimSuffix(pattern, "/") + ".md"
				if got := checkConstraint(single, map[string]string{sibling: ""}); len(got) != 0 {
					t.Errorf("%s [%s]: directory pattern %q fires on sibling file %q", constraint.Record, constraint.ID, pattern, sibling)
				}
			}
			checked++
		}
	}
	// The loader is proven by the record count above; a shipped set with no forbidden-path
	// clause is a valid state with nothing to replay, reported as a skip rather than a pass.
	if checked == 0 {
		t.Skip("no shipped decision record declares a forbidden-path clause")
	}
}
