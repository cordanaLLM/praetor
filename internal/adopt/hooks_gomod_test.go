package adopt

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"github.com/cordanaLLM/praetor/internal/util"
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

// Positive: with a root go.mod, both Go jobs delegate to the engine and failures still block.
func TestGoModuleJobs_Positive_RunWhereTheRootHoldsAModule(t *testing.T) {
	stubs, work := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(work, "go.mod"), "module example.com/demo\n")
	log := filepath.Join(work, "calls.log")
	recordingStub(t, stubs, "go", log, 3)
	recordingStub(t, stubs, "govulncheck", log, 5)
	recordingStub(t, stubs, util.PraetorCLI, log, 4)
	if _, code := runHookLine(t, "sh", goJobRun(t, "pre-commit", "govet"), stubs, work); code != 4 {
		t.Errorf("go vet engine failure exited %d, want 4", code)
	}
	if _, code := runHookLine(t, "sh", goJobRun(t, "pre-push", "security"), stubs, work); code != 4 {
		t.Errorf("vulnerability gate failure exited %d, want 4", code)
	}
	if got := mustRead(t, log); got != util.PraetorCLI+" go vet\n"+util.PraetorCLI+" security govuln\n" {
		t.Errorf("calls = %q", got)
	}
}

// Negative: without a tracked module neither engine subcommand runs a Go tool and neither job
// fails; each says why it skipped instead of blocking every push (#242).
func TestGoModuleJobs_Negative_SkipWithoutARootModule(t *testing.T) {
	stubs, work := t.TempDir(), t.TempDir()
	log := filepath.Join(work, "calls.log")
	recordingStub(t, stubs, "go", log, 3)
	recordingStub(t, stubs, "govulncheck", log, 4)
	recordingStub(t, stubs, util.PraetorCLI, log, 0)
	for _, job := range [][2]string{{"pre-commit", "govet"}, {"pre-push", "security"}} {
		out, code := runHookLine(t, "sh", goJobRun(t, job[0], job[1]), stubs, work)
		if code != 0 {
			t.Errorf("%s/%s without go.mod: exit %d, output %q", job[0], job[1], code, out)
		}
	}
	if got := mustRead(t, log); got != util.PraetorCLI+" go vet\n"+util.PraetorCLI+" security govuln\n" {
		t.Errorf("engine calls without a tracked module = %q", got)
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

// Boundary: a nested go.mod is discovered by the engine; a module without govulncheck installed
// still fails the push with an install instruction, and no praetorctl fails closed.
func TestGoModuleJobs_Boundary_NestedModuleAndMissingScanner(t *testing.T) {
	stubs, work := t.TempDir(), t.TempDir()
	log := filepath.Join(work, "calls.log")
	recordingStub(t, stubs, util.PraetorCLI, log, 0)
	mustWrite(t, filepath.Join(work, "tooling", "go.mod"), "module example.com/tooling\n")
	scannerStubs := t.TempDir()
	recordingStub(t, scannerStubs, util.PraetorCLI, log, 0)
	recordingStub(t, scannerStubs, "govulncheck", log, 0)
	if out, code := runHookLine(t, "sh", goJobRun(t, "pre-push", "security"), scannerStubs, work); code != 0 {
		t.Errorf("untracked nested module: exit %d, output %q", code, out)
	}
	called := mustRead(t, log)
	initGitTestRepo(t, work)
	gitRunCmd(t, work, "add", "tooling/go.mod")
	gitRunCmd(t, work, "commit", "-m", "nested module")
	mustWrite(t, filepath.Join(work, "go.mod"), "module example.com/demo\n")
	if out, code := runHookLine(t, "sh", goJobRun(t, "pre-push", "security"), stubs, work); code != 1 || !strings.Contains(out, "govulncheck is not installed") || !strings.Contains(out, "go install") {
		t.Errorf("missing scanner: exit %d, output %q", code, out)
	}
	if got := mustRead(t, log); got != called {
		t.Errorf("the gate ran without govulncheck: %s", got)
	}
	scannerOnly := t.TempDir()
	recordingStub(t, scannerOnly, "govulncheck", log, 0)
	if out, code := runHookLine(t, "sh", goJobRun(t, "pre-push", "security"), scannerOnly, work); code != 1 || !strings.Contains(out, "neither praetorctl nor standardsctl") {
		t.Errorf("missing praetorctl: exit %d, output %q", code, out)
	}
}

// goJobRunFor returns the run line of one job of the configuration rendered for shape.
func goJobRunFor(t *testing.T, shape lefthookShape, hook, job string) string {
	t.Helper()
	var parsed map[string]map[string]any
	if err := yamlUnmarshal([]byte(buildLefthookYAMLFor(shape, false)), &parsed); err != nil {
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

// Positive (#1315): when the repository tool module pins govulncheck, the pre-push security job
// runs the gate with go tool -modfile=tools/go/go.mod govulncheck without a PATH check.
func TestGoModuleJobs_Positive_ToolModuleGovulncheck(t *testing.T) {
	stubs, work := t.TempDir(), t.TempDir()
	mustWrite(t, filepath.Join(work, "go.mod"), "module example.com/demo\n")
	log := filepath.Join(work, "calls.log")
	recordingStub(t, stubs, util.PraetorCLI, log, 7)
	run := goJobRunFor(t, lefthookShape{languages: hisscatalog.LanguageGo, toolGovuln: true}, "pre-push", "security")
	if strings.Contains(run, "command -v govulncheck") {
		t.Errorf("tool module rendering checks PATH: %q", run)
	}
	if !strings.Contains(run, govulnToolScanner) {
		t.Errorf("tool module rendering lacks scanner: %q", run)
	}
	if _, code := runHookLine(t, "sh", run, stubs, work); code != 7 {
		t.Errorf("gate execution exited %d, want 7", code)
	}
	want := util.PraetorCLI + " " + govulnGateArgs + " -- " + govulnToolScanner + "\n"
	if got := mustRead(t, log); got != want {
		t.Errorf("calls = %q, want %q", got, want)
	}
}

// Positive (#1315): adoption inspects tools/go/go.mod and renders the go tool scanner form when
// govulncheck is pinned, and the requiredToolGuard form otherwise.
func TestAdopt_Positive_ToolModuleRendersGoToolSecurity(t *testing.T) {
	cases := []struct {
		name      string
		toolGoMod string
		wantTool  bool
	}{
		{
			name:      "requires govuln",
			toolGoMod: "module example.com/tools\n\nrequire golang.org/x/vuln v1.8.0\n",
			wantTool:  true,
		},
		{
			name:      "tool block govulncheck",
			toolGoMod: "module example.com/tools\n\ntool (\n\tgolang.org/x/vuln/cmd/govulncheck\n)\n",
			wantTool:  true,
		},
		{
			name:      "unrelated tool",
			toolGoMod: "module example.com/tools\n\nrequire github.com/golangci/golangci-lint v1.60.0\n",
			wantTool:  false,
		},
		{
			name:      "no tool module",
			toolGoMod: "",
			wantTool:  false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newTestRepo(t, "toolmod-"+strings.ReplaceAll(tc.name, " ", "-"))
			mustWrite(t, filepath.Join(repo, "go.mod"), "module example.com/demo\n")
			if tc.toolGoMod != "" {
				mustWrite(t, filepath.Join(repo, filepath.FromSlash(toolGoModFile)), tc.toolGoMod)
			}
			rep := adoptWithSource(t, repo, newAdoptLockSource(t), false)
			if rep == nil {
				t.Fatal("adopt returned nil report")
			}
			lefthookPath := filepath.Join(repo, lefthookFile)
			content := mustRead(t, lefthookPath)
			if tc.wantTool {
				if !strings.Contains(content, "go tool") || !strings.Contains(content, "govulncheck") {
					t.Errorf("expected %q in lefthook.yml, got:\n%s", govulnToolScanner, content)
				}
				if strings.Contains(content, "command -v govulncheck") {
					t.Errorf("unexpected PATH check in lefthook.yml:\n%s", content)
				}
			} else {
				if strings.Contains(content, "go tool") && strings.Contains(content, "govulncheck") {
					t.Errorf("unexpected %q in lefthook.yml:\n%s", govulnToolScanner, content)
				}
				if !strings.Contains(content, "command -v govulncheck") || !strings.Contains(content, "exit 1") {
					t.Errorf("expected failing PATH check in lefthook.yml:\n%s", content)
				}
			}
		})
	}
}

// Table test of toolModuleRequiresGovuln across various directives and comments.
func TestToolModuleRequiresGovuln(t *testing.T) {
	cases := []struct {
		name string
		data string
		want bool
	}{
		{"empty", "", false},
		{"require single", "module x\nrequire golang.org/x/vuln v1.8.0\n", true},
		{"require block", "module x\nrequire (\n\tgolang.org/x/vuln v1.8.0 // indirect\n)\n", true},
		{"tool single", "module x\ntool golang.org/x/vuln/cmd/govulncheck\n", true},
		{"tool block", "module x\ntool (\n\tgolang.org/x/vuln/cmd/govulncheck\n)\n", true},
		{"unrelated requirement", "module x\nrequire golang.org/x/tools v0.30.0\n", false},
		{"unrelated tool", "module x\ntool github.com/golangci/golangci-lint/cmd/golangci-lint\n", false},
		{"commented require", "module x\n// require golang.org/x/vuln v1.8.0\n", false},
		{"commented tool", "module x\n// tool golang.org/x/vuln/cmd/govulncheck\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := toolModuleRequiresGovuln([]byte(tc.data)); got != tc.want {
				t.Errorf("toolModuleRequiresGovuln(%q) = %v, want %v", tc.data, got, tc.want)
			}
		})
	}
}
