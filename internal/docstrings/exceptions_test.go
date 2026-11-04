// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package docstrings

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/cifilter"
)

// TestDocstringExceptions verifies that only a live content-pinned entry excuses a counted file.
func TestDocstringExceptions(t *testing.T) {
	data, err := os.ReadFile("testdata/undocumented.py")
	if err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	today := time.Now().UTC()
	cases := []struct {
		name, fingerprint, expires, status string
		passed                             bool
		counted                            int
	}{
		{"unlisted snapshot fails", "", "", "", false, 1},
		{"listed snapshot passes", hash[:8], today.AddDate(0, 0, 120).Format("2006-01-02"), "excepted", true, 0},
		{"expired snapshot fails", hash[:8], today.AddDate(0, 0, -1).Format("2006-01-02"), "expired", false, 1},
		{"stale snapshot fails", strings.Repeat("0", 8), today.Format("2006-01-02"), "stale", false, 1},
		{"expiry day inclusive", hash, today.Format("2006-01-02"), "excepted", true, 0},
		{"uppercase prefix passes", strings.ToUpper(hash[:8]), today.Format("2006-01-02"), "excepted", true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := writeDocstringExceptionFixture(t, data, tc.fingerprint, tc.expires)
			rep, err := evaluateChangedFiles(t.Context(), root, "", Config{Mode: "error", Threshold: 80}, map[string][]cifilter.LineRange{"snapshot.py": {{Start: 1, End: 10}}})
			if err != nil {
				t.Fatal(err)
			}
			if rep.Passed != tc.passed || rep.Covered != 0 || rep.Counted != tc.counted || len(rep.Uncovered) != tc.counted {
				t.Fatalf("passed=%v counted=%d covered=%d; want passed=%v", rep.Passed, rep.Counted, rep.Covered, tc.passed)
			}
			assertDocstringException(t, rep.Exceptions, tc.status, tc.expires)
		})
	}
}

// TestDocstringExceptions_UncountedFile verifies that entries for untouched declarations stay unreported.
func TestDocstringExceptions_UncountedFile(t *testing.T) {
	data := []byte("def untouched():\n    return 1\n")
	root := writeDocstringExceptionFixture(t, data, fmt.Sprintf("%x", sha256.Sum256(data)), time.Now().UTC().Format("2006-01-02"))
	rep, err := evaluateChangedFiles(t.Context(), root, "", Config{Mode: "error", Threshold: 80}, map[string][]cifilter.LineRange{"snapshot.py": {{Start: 10, End: 11}}})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Passed || rep.Counted != 0 || len(rep.Exceptions) != 0 {
		t.Fatalf("untouched declarations affected report: %+v", rep)
	}
}

// writeDocstringExceptionFixture plants a source file and optionally its manifest exception.
func writeDocstringExceptionFixture(t *testing.T, data []byte, fingerprint, expires string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "snapshot.py"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if fingerprint != "" {
		body := fmt.Sprintf("version: 1\nexceptions:\n  - rule: docstrings\n    path: snapshot.py\n    fingerprint: %s\n    category: D\n    reason: prior rendering fixture\n    expires: %s\n", fingerprint, expires)
		if err := os.WriteFile(filepath.Join(root, ".standards.yaml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// assertDocstringException checks the visible disposition or absence of a file exception.
func assertDocstringException(t *testing.T, entries []ExceptionEntry, status, expires string) {
	t.Helper()
	if status == "" {
		if len(entries) != 0 {
			t.Fatalf("unlisted file acquired exceptions: %+v", entries)
		}
		return
	}
	want := ExceptionEntry{Path: "snapshot.py", Category: "D", Expires: expires, Status: status}
	if len(entries) != 1 || entries[0] != want {
		t.Fatalf("exceptions=%+v, want %+v", entries, want)
	}
}
