package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestModelsMergeExitCodes checks merge-check exit codes: 0 for a valid merge, 1 for contract problems, 2 for usage or input errors.
func TestModelsMergeExitCodes(t *testing.T) {
	dir := t.TempDir()
	draft := filepath.Join(dir, "draft.json")
	final := filepath.Join(dir, "final.json")
	if err := os.WriteFile(draft, []byte(`{"model":"m","kind":"code","findings":[{"id":"x"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"valid", `{"combiner":"c","verdict":"approve","kept":[{"id":"x","sources":["m"]}]}`, 0},
		{"omitted", `{"combiner":"c","verdict":"approve"}`, 1},
		{"malformed", `{`, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(final, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
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
