package main

import (
	"strings"
	"testing"
)

// TestModelsMergeExitCodes checks merge-check exit codes: 0 for a valid merge, 1 for contract problems, 2 for usage or input errors.
func TestModelsMergeExitCodes(t *testing.T) {
	draft := mergeReviewFile(t, "draft.json", `{"model":"m","kind":"code","findings":[{"id":"x","claim":"defect","pointer":"x:1","evidence":"proof"}]}`)
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"valid", `{"combiner":"c","verdict":"approve","kept":[{"id":"x","claim":"defect","pointer":"x:1","evidence":"proof","sources":["m"]}]}`, 0},
		{"omitted", `{"combiner":"c","verdict":"approve"}`, 1},
		{"malformed", `{`, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			final := mergeReviewFile(t, "final.json", tc.body)
			out, _, err := runOutcomeCLI(t, []string{"merge-check", "--draft", draft, "--final", final})
			if got := exitCode(err); got != tc.want {
				t.Fatalf("exit %d want %d: %v %s", got, tc.want, err, out)
			}
			if tc.name == "omitted" && !strings.Contains(out, "x") {
				t.Fatal(out)
			}
		})
	}
	_, _, err := runOutcomeCLI(t, []string{"merge-check"})
	if exitCode(err) != 2 {
		t.Fatalf("usage: %v", err)
	}
}
