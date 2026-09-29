package adopt

import (
	"path/filepath"
	"strings"
	"testing"
)

// goJobRun returns the run line of one pre-commit or pre-push job of the generated
// configuration, as lefthook reads it.
func goJobRun(t *testing.T, hook, job string) string {
	t.Helper()
	var parsed map[string]map[string]any
	if err := yamlUnmarshal([]byte(buildLefthookYAML()), &parsed); err != nil {
		t.Fatalf("generated lefthook.yml does not parse: %v", err)
	}
	commands, ok := parsed[hook]["commands"].(map[string]any)
	if !ok {
		t.Fatalf("%s has no commands", hook)
	}
	body, ok := commands[job].(map[string]any)
	if !ok {
		t.Fatalf("%s has no job %s", hook, job)
	}
	run, ok := body["run"].(string)
	if !ok {
		t.Fatalf("%s/%s has no run line", hook, job)
	}
	return run
}

// Positive: with a root go.mod, go vet and govulncheck run and their failures still block.
func TestGoModuleJobs_Positive_RunWhereTheRootHoldsAModule(t *testing.T) {
	stubs, work := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(work, "go.mod"), "module example.com/demo\n")
	log := filepath.Join(work, "calls.log")
	recordingStub(t, stubs, "go", log, 3)
	recordingStub(t, stubs, "govulncheck", log, 4)
	if _, code := runHookLine(t, "sh", goJobRun(t, "pre-commit", "govet"), stubs, work); code != 3 {
		t.Errorf("go vet failure exited %d, want 3", code)
	}
	if _, code := runHookLine(t, "sh", goJobRun(t, "pre-push", "security"), stubs, work); code != 4 {
		t.Errorf("govulncheck failure exited %d, want 4", code)
	}
	if got := mustRead(t, log); got != "go vet ./...\ngovulncheck ./...\n" {
		t.Errorf("calls = %q", got)
	}
}

// Negative: without a root go.mod neither tool runs and neither job fails; each says why it
// skipped instead of blocking every push of a repository whose module is elsewhere (#242).
func TestGoModuleJobs_Negative_SkipWithoutARootModule(t *testing.T) {
	stubs, work := t.TempDir(), t.TempDir()
	log := filepath.Join(work, "calls.log")
	recordingStub(t, stubs, "go", log, 3)
	recordingStub(t, stubs, "govulncheck", log, 4)
	for _, job := range [][2]string{{"pre-commit", "govet"}, {"pre-push", "security"}} {
		out, code := runHookLine(t, "sh", goJobRun(t, job[0], job[1]), stubs, work)
		if code != 0 || !strings.Contains(out, "no go.mod at the repository root, skipping") {
			t.Errorf("%s/%s without go.mod: exit %d, output %q", job[0], job[1], code, out)
		}
	}
	if fileExists(log) {
		t.Errorf("a Go tool ran without a root module: %s", mustRead(t, log))
	}
}

// Positive (#568): with a root Cargo.toml, the Rust pre-commit jobs run cargo fmt in check mode
// and the gate's clippy command, and a failure of either blocks the commit.
func TestCargoJobs_Positive_RunWhereTheRootHoldsACargoManifest(t *testing.T) {
	stubs, work := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(work, "Cargo.toml"), "[package]\nname = \"demo\"\n")
	log := filepath.Join(work, "calls.log")
	recordingStub(t, stubs, "cargo", log, 5)
	for _, job := range []string{"rustfmt", "clippy"} {
		if _, code := runHookLine(t, "sh", goJobRun(t, "pre-commit", job), stubs, work); code != 5 {
			t.Errorf("%s failure exited %d, want 5", job, code)
		}
	}
	if got := mustRead(t, log); got != "cargo fmt --all --check\ncargo clippy --workspace --all-targets -- -D warnings\n" {
		t.Errorf("calls = %q", got)
	}
}

// Negative: without a root Cargo.toml neither Rust job runs cargo or fails; each says why it
// skipped, as the Go jobs do without a root go.mod.
func TestCargoJobs_Negative_SkipWithoutARootCargoManifest(t *testing.T) {
	stubs, work := t.TempDir(), t.TempDir()
	log := filepath.Join(work, "calls.log")
	recordingStub(t, stubs, "cargo", log, 5)
	for _, job := range []string{"rustfmt", "clippy"} {
		out, code := runHookLine(t, "sh", goJobRun(t, "pre-commit", job), stubs, work)
		if code != 0 || !strings.Contains(out, "no Cargo.toml at the repository root, skipping cargo") {
			t.Errorf("%s without Cargo.toml: exit %d, output %q", job, code, out)
		}
	}
	if fileExists(log) {
		t.Errorf("cargo ran without a root manifest: %s", mustRead(t, log))
	}
}

// Boundary: a Cargo.toml in a subdirectory is not a root manifest, and each Rust job runs only
// for staged Rust files (glob *.rs), as the Go jobs run only for staged Go files.
func TestCargoJobs_Boundary_NestedManifestAndRustGlob(t *testing.T) {
	stubs, work := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(work, "crates", "core", "Cargo.toml"), "[package]\nname = \"core\"\n")
	if out, code := runHookLine(t, "sh", goJobRun(t, "pre-commit", "clippy"), stubs, work); code != 0 || !strings.Contains(out, "no Cargo.toml") {
		t.Errorf("nested manifest: exit %d, output %q", code, out)
	}
	var parsed map[string]map[string]any
	if err := yamlUnmarshal([]byte(buildLefthookYAML()), &parsed); err != nil {
		t.Fatal(err)
	}
	commands, isMap := parsed["pre-commit"]["commands"].(map[string]any)
	if !isMap {
		t.Fatal("pre-commit has no commands map")
	}
	for _, job := range []string{"rustfmt", "clippy"} {
		body, isJob := commands[job].(map[string]any)
		if glob := body["glob"]; !isJob || glob != "*.rs" {
			t.Errorf("%s glob = %v, want *.rs", job, glob)
		}
	}
}

// Boundary: a go.mod in a subdirectory is not a root module, and a root module without
// govulncheck installed still skips the scan rather than failing the push.
func TestGoModuleJobs_Boundary_NestedModuleAndMissingScanner(t *testing.T) {
	stubs, work := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(work, "tooling", "go.mod"), "module example.com/tooling\n")
	if out, code := runHookLine(t, "sh", goJobRun(t, "pre-push", "security"), stubs, work); code != 0 || !strings.Contains(out, "no go.mod") {
		t.Errorf("nested module: exit %d, output %q", code, out)
	}
	mustWrite(t, filepath.Join(work, "go.mod"), "module example.com/demo\n")
	if out, code := runHookLine(t, "sh", goJobRun(t, "pre-push", "security"), stubs, work); code != 0 || !strings.Contains(out, "govulncheck is not installed") {
		t.Errorf("missing scanner: exit %d, output %q", code, out)
	}
}
