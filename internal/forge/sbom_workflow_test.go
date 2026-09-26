package forge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sbomRepo writes files (relative path to content) into a fresh repository directory.
func sbomRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// sbomJob wraps steps into a one-job workflow document.
func sbomJob(steps string) string {
	return "on: [push]\njobs:\n  release:\n    runs-on: ubuntu-latest\n    steps:\n" + steps
}

const goreleaserStep = "      - uses: goreleaser/goreleaser-action@v7\n        with:\n          args: release --clean\n"

// Positive: this repository's release workflow catalogues the archives through GoReleaser's
// sboms block, and no dedicated sbom.yml is needed for the policy to hold.
func TestSBOMWorkflowFindsTheEngineReleaseWorkflow(t *testing.T) {
	name, err := SBOMWorkflow(context.Background(), engineRoot)
	if err != nil || name != "release-binaries.yml" {
		t.Fatalf("SBOMWorkflow(engine) = %q, %v; want release-binaries.yml", name, err)
	}
}

// Positive: a dedicated sbom.yml and an SBOM step inside the release workflow both satisfy
// the policy (#43).
func TestSBOMWorkflowAcceptsDedicatedAndReleaseJobs(t *testing.T) {
	cases := map[string]map[string]string{
		"dedicated sbom.yml running syft": {
			".github/workflows/sbom.yml": sbomJob("      - run: syft dir:dist -o spdx-json=sbom.spdx.json\n"),
		},
		"release job with goreleaser sboms": {
			".github/workflows/release.yml": sbomJob(goreleaserStep),
			".goreleaser.yaml":              "sboms:\n  - artifacts: archive\n",
		},
		"release job with the sbom action": {
			".github/workflows/release.yml": sbomJob("      - uses: anchore/sbom-action@v0.24.2\n"),
		},
		"release job attesting an sbom": {
			".github/workflows/release.yml": sbomJob("      - uses: Actions/Attest-SBOM@v2\n"),
		},
		"release job running praetorctl sbom": {
			".github/workflows/release.yml": sbomJob("      - run: ./bin/praetorctl sbom -out sbom.json\n"),
		},
		"goreleaser run with an explicit config": {
			".github/workflows/release.yml": sbomJob("      - run: goreleaser release --config build/release.yaml\n"),
			"build/release.yaml":            "sboms:\n  - artifacts: binary\n",
		},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := SBOMWorkflow(context.Background(), sbomRepo(t, files))
			if err != nil || got == "" {
				t.Fatalf("SBOMWorkflow = %q, %v; want a workflow", got, err)
			}
		})
	}
}

// Negative: the file name alone, an installer with no generator, a generator only named in
// a comment, and a GoReleaser release without an sboms block are not SBOM evidence.
func TestSBOMWorkflowRejectsWorkflowsThatWriteNoSBOM(t *testing.T) {
	cases := map[string]map[string]string{
		"empty sbom.yml job": {
			".github/workflows/sbom.yml": sbomJob("      - run: echo nothing\n"),
		},
		"syft installed but never run": {
			".github/workflows/sbom.yml": sbomJob("      - uses: anchore/sbom-action/download-syft@v0.24.2\n"),
		},
		"syft named in a comment": {
			".github/workflows/sbom.yml": sbomJob("      - run: |\n          # syft dir:. -o spdx-json=x.json\n          echo skipped\n"),
		},
		"syft printing a table": {
			".github/workflows/sbom.yml": sbomJob("      - run: syft dir:.\n"),
		},
		"goreleaser without sboms": {
			".github/workflows/release.yml": sbomJob(goreleaserStep),
			".goreleaser.yaml":              "builds:\n  - main: .\n",
		},
		"goreleaser check only": {
			".github/workflows/release.yml": sbomJob("      - uses: goreleaser/goreleaser-action@v7\n        with:\n          args: check\n"),
			".goreleaser.yaml":              "sboms:\n  - artifacts: archive\n",
		},
		"no workflows at all": {"README.md": "# none\n"},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := SBOMWorkflow(context.Background(), sbomRepo(t, files))
			if err != nil || got != "" {
				t.Fatalf("SBOMWorkflow = %q, %v; want none", got, err)
			}
		})
	}
}

// Boundary: a config path outside the repository is never read, a malformed workflow or
// GoReleaser file is an error rather than a silent pass, and a nil context is refused.
func TestSBOMWorkflowBoundaries(t *testing.T) {
	escaping := sbomRepo(t, map[string]string{
		".github/workflows/release.yml": sbomJob("      - run: goreleaser release -f ../outside.yaml\n"),
	})
	if got, err := SBOMWorkflow(context.Background(), escaping); err != nil || got != "" {
		t.Errorf("escaping config: %q, %v; want none", got, err)
	}
	broken := map[string]map[string]string{
		"malformed workflow":   {".github/workflows/release.yml": "jobs: [\n"},
		"malformed goreleaser": {".github/workflows/release.yml": sbomJob(goreleaserStep), ".goreleaser.yaml": "sboms: [\n"},
	}
	for name, files := range broken {
		if _, err := SBOMWorkflow(context.Background(), sbomRepo(t, files)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	var fields strings.Builder
	fields.WriteString("      - run: echo")
	for i := 0; i <= maxRunScriptFields; i++ {
		fmt.Fprintf(&fields, " f%d", i)
	}
	oversized := sbomRepo(t, map[string]string{".github/workflows/sbom.yml": sbomJob(fields.String() + "\n")})
	if _, err := SBOMWorkflow(context.Background(), oversized); err == nil {
		t.Error("run script over the field bound accepted")
	}
	var absent context.Context
	if _, err := SBOMWorkflow(absent, engineRoot); err == nil {
		t.Error("nil context accepted")
	}
}
