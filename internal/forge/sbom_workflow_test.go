package forge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hiss"
	"time"
)

// sbomRepo writes files (relative path to content) into a fresh repository directory.
func sbomRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a directory the test writes files into needs the owner execute bit; 0o700 is least privilege
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
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
		"praetorctl sbom as the last field": {
			".github/workflows/sbom.yml": sbomJob("      - run: ./bin/praetorctl sbom\n"),
		},
		"notices check followed by a real sbom run": {
			".github/workflows/release.yml": sbomJob("      - run: praetorctl sbom notices --check && praetorctl sbom --out sbom.json\n"),
		},
		"notices named after a flag is the generator": {
			".github/workflows/release.yml": sbomJob("      - run: praetorctl sbom --out sbom.json notices\n"),
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
// a comment, a GoReleaser release without an sboms block, and this tool's sbom notices
// subcommand are not SBOM evidence.
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
		// sbom notices renders THIRD-PARTY-NOTICES.md tables and writes no SBOM document.
		"praetorctl sbom notices check": {
			".github/workflows/sbom.yml": sbomJob("      - run: ./bin/praetorctl sbom notices --check\n"),
		},
		"go run standardsctl sbom notices": {
			".github/workflows/sbom.yml": sbomJob("      - run: go run ./cmd/standardsctl sbom notices\n"),
		},
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

// What never runs generates no SBOM (review of #330). Negative: a generator an echo prints, and
// one in a step or job whose if: is the literal false. Positive: a generator after an echo in the
// same script runs, and a condition that may hold is read as running.
func TestSBOMWorkflowSkipsWhatNeverRuns(t *testing.T) {
	syft := "run: syft dir:. -o spdx-json=sbom.spdx.json\n"
	cases := map[string]struct {
		workflow  string
		generates bool
	}{
		"a generator an echo prints":  {sbomJob("      - run: echo syft dir:. -o spdx-json=sbom.spdx.json\n"), false},
		"a step with if: false":       {sbomJob("      - if: false\n        " + syft), false},
		"a job with if: ${{ false }}": {"on: push\njobs:\n  sbom:\n    if: ${{ false }}\n    runs-on: ubuntu-latest\n    steps:\n      - " + syft, false},
		"a generator after an echo":   {sbomJob("      - run: echo cataloguing; syft dir:. -o spdx-json=sbom.spdx.json\n"), true},
		"a condition that may hold":   {sbomJob("      - if: github.event_name == 'push'\n        " + syft), true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := SBOMWorkflow(context.Background(), sbomRepo(t, map[string]string{".github/workflows/sbom.yml": tc.workflow}))
			if err != nil || (got != "") != tc.generates {
				t.Fatalf("SBOMWorkflow = %q, %v; want generates=%t", got, err, tc.generates)
			}
		})
	}
}

// A step holding nested quotes in a double-quoted command substitution is read cleanly.
func TestSBOMWorkflowAcceptsNestedQuotesCommandSubstitution(t *testing.T) {
	step := "      - run: |\n          token=\"$(printf '%s' \"$token_response\" | sed -n 's/.*\"token\":\"\\([^\"]*\\)\".*/\\1/p')\"\n          syft dir:. -o spdx-json=sbom.spdx.json\n"
	got, err := SBOMWorkflow(context.Background(), sbomRepo(t, map[string]string{".github/workflows/release-build.yml": sbomJob(step)}))
	if err != nil || got != "release-build.yml" {
		t.Fatalf("SBOMWorkflow = %q, %v; want release-build.yml", got, err)
	}
}

// TestSBOMWorkflowHereDocs distinguishes executed bodies from data and malformed input.
func TestSBOMWorkflowHereDocs(t *testing.T) {
	for _, tc := range []struct {
		name, command   string
		want, wantError bool
	}{
		{"piped shell", "cat <<'EOF' | bash", true, false},
		{"direct shell", "bash <<'EOF'", true, false},
		{"stdin shell", "sh -s <<EOF", true, false},
		{"absolute shell", "cat <<'EOF' | /bin/sh -s", true, false},
		{"assigned shell", "cat <<'EOF' | FOO=1 dash", true, false},
		{"ash shell", "cat <<'EOF' | ash", true, false},
		{"file data", "cat > notes.txt <<'EOF'", false, false},
		{"printed data", "cat <<'EOF'", false, false},
		{"non interpreter pipe", "cat <<'EOF' | tee file", false, false},
		{"redirected output", "cat > notes.txt <<'EOF' | bash", false, false},
		{"shell command argument", "cat <<'EOF' | bash -c true", false, false},
		{"shell script argument", "cat <<'EOF' | bash script.sh", false, false},
		{"cat file argument", "cat notes.txt <<'EOF' | bash", false, false},
		{"forge output", "gh pr comment 9 --body-file - <<'EOF' | bash", false, false},
		{"unrelated shell", "cat <<'EOF'; true | bash", false, false},
		{"unterminated", "cat <<'EOF' | bash", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := tc.command + "\nsyft dir:. -o spdx-json=sbom.json\n"
			if !tc.wantError {
				script += "EOF\n"
			}
			steps := "      - run: |\n          " + strings.ReplaceAll(strings.TrimSuffix(script, "\n"), "\n", "\n          ") + "\n"
			root := sbomRepo(t, map[string]string{".github/workflows/sbom.yml": sbomJob(steps)})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			name, err := SBOMWorkflow(ctx, root)
			if (err != nil) != tc.wantError || (name != "") != tc.want {
				t.Fatalf("SBOMWorkflow = %q, %v; want generation %v, error %v", name, err, tc.want, tc.wantError)
			}
		})
	}
}

// TestSBOMReaderHereDocs sees an executing generator and ignores data.
func TestSBOMReaderHereDocs(t *testing.T) {
	checkHereDocReader(t, "syft dir:. -o sbom.json", func(script string) bool {
		fields, err := scriptFields(script)
		if err != nil {
			t.Fatal(err)
		}
		return runInvokesSBOMGenerator(fields)
	})
}

// TestSBOMWorkflowRound3Unproven withholds generation credit for unknown stdin ownership.
func TestSBOMWorkflowRound3Unproven(t *testing.T) {
	fields, err := scriptFields("mystery <<'EOF'\nsyft dir:. -o sbom.json\nEOF\n")
	if err != nil || runInvokesSBOMGenerator(fields) {
		t.Fatalf("unproven generator credited: %q, %v", fields, err)
	}
	_, docs, splitErr := hiss.SplitHereDocs("mystery <<'EOF'\nbody\nEOF\n")
	if splitErr != nil || len(docs) != 1 || docs[0].Reason != "unproven here-document owner: mystery" {
		t.Fatalf("missing unproven reason: %#v, %v", docs, splitErr)
	}

}

// TestSBOMWorkflowRound3Proof pins generator evidence through the shared ownership and file walker.
func TestSBOMWorkflowRound3Proof(t *testing.T) {
	generator := "syft dir:. -o sbom.json\n"
	for _, tc := range []struct {
		name, script string
		want         bool
	}{
		{"T3 quoted plus", "cat > 'build+sbom.sh' <<'A'\n" + generator + "A\nbash 'build+sbom.sh'\n", true},
		{"T4 pipeline cd", "cat > run.sh <<'A'\n" + generator + "A\ncd sub | cat\nbash run.sh\n", true},
		{"T5 overwritten generator", "cat > run.sh <<'A'\n" + generator + "A\ncat > run.sh <<'B'\ntrue\nB\nbash run.sh\n", false},
		{"T5 appended generator", "cat > run.sh <<'A'\n" + generator + "A\ncat >> run.sh <<'B'\ntrue\nB\nbash run.sh\n", true},
		{"T6 command string", "bash -c true <<'A'\n" + generator + "A\n", false},
		{"T6 stdin shell", "bash -s <<'A'\n" + generator + "A\n", true},
		{"T7 discarded generator", "cat <<'A' <<'B' | bash\n" + generator + "A\ntrue\nB\n", false},
		{"T7 effective generator", "cat <<'A' <<'B' | bash\ntrue\nA\n" + generator + "B\n", true},
		{"T8 trailing comment", "cat <<'A' | bash # note\n" + generator + "A\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fields, err := scriptFields(tc.script)
			if err != nil || runInvokesSBOMGenerator(fields) != tc.want {
				t.Fatalf("generator evidence: %q, %v; want %v", fields, err, tc.want)
			}
		})
	}
}
