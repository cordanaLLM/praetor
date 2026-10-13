package main

import (
	"strings"
	"testing"
)

// #1279: flavors plan names a declared tag_pattern as ignored, once per flavor, and plans the
// flavors as before; the repository's own config prints no note.
func TestRunFlavorsPlan_Negative_NamesAnIgnoredTagPattern(t *testing.T) {
	f := newFlavorFixture(t)
	config := writeFixtureFile(t, t.TempDir(), "flavors.yaml", strings.Replace(flavorFixtureConfig,
		`edge:     {source_ref: "refs/heads/main"}`, `edge:     {source_ref: "refs/heads/main", tag_pattern: "v{{version}}-rc.{{rc_number}}"}`, 1))
	out, err := captureStdout(t, func() error { return runFlavors([]string{"plan", "--config=" + config, "--dir=" + f.repo}) })
	if err != nil {
		t.Fatalf("plan: %v\n%s", err, out)
	}
	if strings.Count(out, "Note: flavor \"edge\" declares tag_pattern, which is ignored") != 1 || strings.Count(out, "Note:") != 1 {
		t.Fatalf("want one note for edge:\n%s", out)
	}
	if !strings.Contains(out, "] edge ") {
		t.Fatalf("edge is no longer planned:\n%s", out)
	}
	plain, err := captureStdout(t, func() error { return runFlavors([]string{"plan", "--config=" + f.config, "--dir=" + f.repo}) })
	if err != nil || strings.Contains(plain, "Note:") {
		t.Fatalf("a config without tag_pattern prints a note (err %v):\n%s", err, plain)
	}
}
