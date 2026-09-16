package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const discoveryTestPolicy = `{
  "version": 1,
  "rules": [{
    "key": "hiss:go",
    "title": "Go scanner",
    "kind": "scanner_extension",
    "matches": [".go"],
    "analyzer": "hiss"
  }]
}
`

func writeDiscoveryCLIFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join(root, "source")
	artifacts := filepath.Join(root, "artifacts")
	if err := os.MkdirAll(source, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(artifacts, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	policy := filepath.Join(root, "policy.json")
	if err := os.WriteFile(policy, []byte(discoveryTestPolicy), 0o600); err != nil {
		t.Fatal(err)
	}
	return source, policy, artifacts
}

func TestDogfoodDiscoveryCLIPlanAndObservePersistReports(t *testing.T) {
	source, policy, artifacts := writeDiscoveryCLIFixture(t)
	for _, stage := range []string{"plan", "observe"} {
		runDir := filepath.Join(artifacts, stage)
		args := []string{"--path", source, "--policy", policy, "--artifacts", runDir, "--stage", stage}
		if err := runDogfoodDiscovery(context.Background(), args); err != nil {
			t.Fatalf("%s: %v", stage, err)
		}
		data, err := os.ReadFile(filepath.Join(runDir, "report.json"))
		if err != nil {
			t.Fatalf("%s report: %v", stage, err)
		}
		var report struct {
			Status   string `json:"status"`
			Complete bool   `json:"complete"`
			Cases    []struct {
				Status string `json:"status"`
			} `json:"cases"`
		}
		if err := json.Unmarshal(data, &report); err != nil {
			t.Fatalf("%s report JSON: %v", stage, err)
		}
		if stage == "plan" {
			if report.Status != "planned" || report.Complete || len(report.Cases) != 1 || report.Cases[0].Status != "planned" {
				t.Fatalf("unexpected plan report: %+v", report)
			}
			continue
		}
		if report.Status != "observed" || !report.Complete || len(report.Cases) != 1 || report.Cases[0].Status != "observed" {
			t.Fatalf("unexpected observe report: %+v", report)
		}
		if _, err := os.Stat(filepath.Join(runDir, "case-001", "report.json")); err != nil {
			t.Fatalf("case evidence missing: %v", err)
		}
	}
}

func TestDogfoodDiscoveryCLIRejectsInvalidArguments(t *testing.T) {
	source, policy, artifacts := writeDiscoveryCLIFixture(t)
	valid := []string{"--path", source, "--policy", policy, "--artifacts", filepath.Join(artifacts, "valid")}
	for _, tc := range []struct {
		name string
		args []string
		want string
		// is names a sentinel for cases whose message comes from the operating system.
		// The OS text is locale- and platform-specific -- Windows reports a missing path as
		// "Das System kann die angegebene Datei nicht finden." on a German host -- so
		// matching "no such file or directory" tested the host's language, not the refusal.
		is error
	}{
		{"unknown flag", append(append([]string{}, valid...), "--unknown"), "flag provided but not defined", nil},
		{"positional argument", append(append([]string{}, valid...), "extra"), "dogfood discover accepts flags only", nil},
		{"invalid stage", append(append([]string{}, valid...), "--stage", "execute"), "discovery stage must be plan or observe", nil},
		{"missing artifact parent", []string{"--path", source, "--policy", policy, "--artifacts", filepath.Join(t.TempDir(), "nested", "run")}, "", fs.ErrNotExist},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := runDogfoodDiscovery(context.Background(), tc.args)
			if err == nil {
				t.Fatal("invalid arguments accepted")
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err, tc.want)
			}
			if tc.is != nil && !errors.Is(err, tc.is) {
				t.Fatalf("error %q is not %v", err, tc.is)
			}
		})
	}
}
