package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestClaudeLimitsPrintsOnlyCounts pins claude limits prints only counts.
func TestClaudeLimitsPrintsOnlyCounts(t *testing.T) {
	dir := t.TempDir()
	secret := "sk-" + "ant-" + strings.Repeat("Q", 30)
	lines := strings.Join([]string{
		/* testdate: fact: recorded fixture metadata; parsing or exact text comparison, no clock read */ `{"timestamp":"2026-10-13T09:00:00Z","message":"` + secret + `","quotaLimits":{"status":"rejected","rateLimitType":"five_hour","resetsAt":1}}`,
		/* testdate: fact: recorded fixture metadata; parsing or exact text comparison, no clock read */ `{"timestamp":"2026-10-13T09:01:00Z","message":"x","quotaLimits":{"status":"rejected","rateLimitType":"five_hour","resetsAt":1}}`,
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runClaudeLimits([]string{"--claude-projects-dir=" + dir}, &out); err != nil {
		t.Fatal(err)
	}
	want := "files 1, quotaLimits lines 2, overlong lines skipped 0\nrateLimitType five_hour 2\nstatus rejected 2\n"
	if out.String() != want {
		t.Fatalf("output\n%q\nwant\n%q", out.String(), want)
	}
	if err := runClaudeLimits([]string{"--bogus"}, &out); err == nil {
		t.Fatal("an unknown flag must fail")
	}
}
