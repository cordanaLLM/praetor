package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/buildid"
)

func TestDogfoodSuiteCLIPlanAndFlagErrors(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "suite.json")
	data := `{"version":1,"public_repositories":["https://github.com/spf13/cobra#adbc8813901bba65827259daa8e22ff94ec1f30e"],"transcripts":[]}`
	if err := os.WriteFile(config, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runDogfood([]string{"suite", "--config", config, "--artifacts", filepath.Join(root, "plan")}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"suite"}, {"suite", "--config", config}, {"suite", "--config", config, "--artifacts", filepath.Join(root, "bad"), "extra"}, {"suite", "--config", config, "--artifacts", filepath.Join(root, "bad"), "--stage=promote"}} {
		if err := runDogfood(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}

// engineVersion reads engine_build.version from a persisted dogfood report.
func engineVersion(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Engine map[string]string `json:"engine_build"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return report.Engine["version"]
}

// Positive, negative and boundary: dogfood suite and discovery reports name the build
// `praetorctl version` prints (#689). An injected release is recorded verbatim; an unstamped
// test build without an injection, and a blank injection, record what the binary proves,
// never an empty or invented version.
func TestDogfoodCLIReportsRecordTheBinaryIdentity(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })

	root := t.TempDir()
	config := filepath.Join(root, "suite.json")
	data := `{"version":1,"public_repositories":["https://github.com/spf13/cobra#adbc8813901bba65827259daa8e22ff94ec1f30e"],"transcripts":[]}`
	if err := os.WriteFile(config, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	source, policy, artifacts := writeDiscoveryCLIFixture(t)
	for i, tc := range []struct{ name, injected, want string }{
		{name: "release", injected: "v1.4.2", want: "v1.4.2"},
		{name: "unstamped", injected: "", want: buildid.Running("").String()},
		{name: "blank injection", injected: "   ", want: buildid.Running("").String()},
	} {
		version = tc.injected
		if got := buildVersion(); got != tc.want {
			t.Fatalf("%s: praetorctl version reports %q, want %q", tc.name, got, tc.want)
		}
		suiteRun := filepath.Join(root, "suite-"+string(rune('a'+i)))
		if _, err := captureStdout(t, func() error {
			return runDogfood([]string{"suite", "--config", config, "--artifacts", suiteRun})
		}); err != nil {
			t.Fatalf("%s suite: %v", tc.name, err)
		}
		discoveryRun := filepath.Join(artifacts, "discovery-"+string(rune('a'+i)))
		if _, err := captureStdout(t, func() error {
			return runDogfoodDiscovery(context.Background(), []string{"--path", source, "--policy", policy, "--artifacts", discoveryRun})
		}); err != nil {
			t.Fatalf("%s discovery: %v", tc.name, err)
		}
		for _, path := range []string{filepath.Join(suiteRun, "report.json"), filepath.Join(discoveryRun, "report.json")} {
			if got := engineVersion(t, path); got != tc.want || strings.TrimSpace(got) == "" {
				t.Errorf("%s: %s engine_build.version %q, want %q", tc.name, path, got, tc.want)
			}
		}
	}
}
