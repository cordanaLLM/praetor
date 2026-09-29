package needs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The pre-migration epic scopes its tasks by what the scan found (#296). These tests pin that a
// repository is never handed work that does not fit it, and that the work its signals do call
// for is still there.

// epicTaskBody returns the body of the planned task in slot n, failing when the slot was
// omitted or planned twice.
func epicTaskBody(t *testing.T, epic *PreMigrationEpic, n int) string {
	t.Helper()
	prefix := fmt.Sprintf("[TASK %d/%d] ", n, totalEpicTasks)
	var bodies []string
	for _, child := range epic.ChildIssues {
		if strings.HasPrefix(child.Title, prefix) {
			bodies = append(bodies, child.Body)
		}
	}
	if len(bodies) != 1 {
		t.Fatalf("task %d planned %d times: %+v", n, len(bodies), epic.ChildIssues)
	}
	return bodies[0]
}

// writeRustWorkspaceRepo builds the reported case: a Cargo workspace beside a build.zig, with
// no Kubernetes manifests, no root go.mod and no runner routing.
func writeRustWorkspaceRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	writeRepoFile(t, filepath.Join(repo, "Cargo.toml"), "[workspace]\nmembers = [\"engine\"]\n\n[workspace.dependencies]\nserde = \"1\"\n")
	writeRepoFile(t, filepath.Join(repo, "build.zig"), "const std = @import(\"std\");\n")
	return repo
}

// Negative: a Rust workspace with no framework, no Kubernetes manifests and no runner routing is
// handed none of the Go, cluster, framework or runner work the fixed task bodies used to carry.
// The substitution task is omitted with its reason and the chain skips it.
func TestGeneratePreMigrationEpic_Negative_RustWorkspaceGetsNoForeignWork(t *testing.T) {
	epic, err := GeneratePreMigrationEpic(t.Context(), writeRustWorkspaceRepo(t), FrameworkSource{}, nil)
	if err != nil {
		t.Fatalf("epic generation failed: %v", err)
	}
	if len(epic.ChildIssues) != totalEpicTasks-1 || len(epic.OmittedTasks) != 1 {
		t.Fatalf("want 4 planned tasks and 1 omitted, got %d and %+v", len(epic.ChildIssues), epic.OmittedTasks)
	}
	omitted := epic.OmittedTasks[0]
	if omitted.Task != 3 || !strings.HasPrefix(omitted.Title, "[TASK 3/5] Framework Dependency Substitution") || omitted.Reason != nothingToRewrite {
		t.Errorf("omitted task = %+v", omitted)
	}
	foreign := []string{"race", "Vault", "in-cluster", "svc.cluster.local", "ARC", "webhook", "strict-zero-debt", "runner routing", "Go:", "not configured"}
	for _, child := range epic.ChildIssues {
		for _, word := range foreign {
			if strings.Contains(child.Body, word) && (word != "race" || strings.HasPrefix(child.Title, "[TASK 1/5]")) {
				t.Errorf("%s prescribes %q:\n%s", child.Title, word, child.Body)
			}
		}
	}
	hygiene := epicTaskBody(t, epic, 1)
	for _, want := range []string{"Rust: add 3D unit tests (positive, negative, boundary) and run them with `cargo test --workspace`.", "Rust: eliminate `.unwrap()`"} {
		if !strings.Contains(hygiene, want) {
			t.Errorf("task 1 lacks %q:\n%s", want, hygiene)
		}
	}
	if gate := epicTaskBody(t, epic, 4); !strings.Contains(gate, "run the task 1 tests and audits for Rust outside the gate") {
		t.Errorf("task 4 does not say the gate leaves Rust untested:\n%s", gate)
	}
	if got := epic.ChildIssues[2].DependsOn; len(got) != 1 || got[0] != taskAnchor(2) {
		t.Errorf("task 4 depends on %v, want the planned task 2", got)
	}
}

// Negative: the checklist names the omitted slot with its reason and no open item, keeps every
// "[TASK n/5]" marker `praetorctl audit` requires of a written epic, and states diff-aware CI
// as intent rather than as something the target's CI already does.
func TestGeneratePreMigrationEpic_Negative_ChecklistNamesOmittedTask(t *testing.T) {
	epic, err := GeneratePreMigrationEpic(t.Context(), writeRustWorkspaceRepo(t), FrameworkSource{}, nil)
	if err != nil {
		t.Fatalf("epic generation failed: %v", err)
	}
	md := epic.ChecklistMarkdown
	for _, want := range []string{
		"- **Detected Languages**: Rust\n",
		"- **Task 3**: [TASK 3/5] Framework Dependency Substitution",
		"  - *Omitted*: " + nothingToRewrite + ".\n",
		"- [ ] **Task 4**: [TASK 4/5] Gated Verification & Ed25519 Receipt",
		"does not claim the filter already runs",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("checklist lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "- [ ] **Task 3**") || strings.Contains(md, "CI runs targeted gates") {
		t.Errorf("checklist opens the omitted task or claims unobserved CI:\n%s", md)
	}
	document := renderEpicDocument(epic)
	for n := 1; n <= totalEpicTasks; n++ {
		if marker := fmt.Sprintf("[TASK %d/5]", n); !strings.Contains(document, marker) {
			t.Errorf("written epic lacks %s, which `praetorctl audit` requires", marker)
		}
	}
}

// writeSignalledGoRepo builds a Go service whose source imports a module the framework contract
// replaces, with a Helm chart at its root and runner routing declared in its fleet tier.
func writeSignalledGoRepo(t *testing.T) string {
	t.Helper()
	repo := writeEpicFixtureRepo(t)
	writeRepoFile(t, filepath.Join(repo, "main.go"), "package main\n\nimport _ \"github.com/jackc/pgx/v5\"\n\nfunc main() {}\n")
	writeRepoFile(t, filepath.Join(repo, "Chart.yaml"), "apiVersion: v2\nname: epic-target\nversion: 0.1.0\n")
	writeRepoFile(t, filepath.Join(repo, ".config", "fleet.yaml"), "runners:\n  default: example-runner-set-linux-amd64\n")
	return repo
}

// Positive: every signal the scan finds brings its step - Go tests under the race detector, the
// in-cluster DNS step for a chart, the substitution task for a proposed substitution and the
// runner routing check for declared routing - and a Go module at the root needs no note that the
// gate leaves a language untested.
func TestGeneratePreMigrationEpic_Positive_SignalsBringTheirSteps(t *testing.T) {
	epic, err := GeneratePreMigrationEpic(t.Context(), writeSignalledGoRepo(t), acmeSource(""), nil)
	if err != nil {
		t.Fatalf("epic generation failed: %v", err)
	}
	if len(epic.ChildIssues) != totalEpicTasks || len(epic.OmittedTasks) != 0 {
		t.Fatalf("want all 5 tasks planned, got %d and omitted %+v", len(epic.ChildIssues), epic.OmittedTasks)
	}
	wants := map[int][]string{
		1: {"Go: add 3D unit tests (positive, negative, boundary) and run them with `go test -race ./...`."},
		2: {"`<service>.<namespace>.svc.cluster.local`"},
		3: {"Review 1 proposed import substitutions and 1 dropped module requirements targeting `" + acmeKit + "`."},
		5: {"Check the declared runner routing", "`praetorctl sync --remote`", "`receipt.public_key`"},
	}
	for n, lines := range wants {
		body := epicTaskBody(t, epic, n)
		for _, want := range lines {
			if !strings.Contains(body, want) {
				t.Errorf("task %d lacks %q:\n%s", n, want, body)
			}
		}
	}
	if body := epicTaskBody(t, epic, 2); strings.Contains(body, "circular") {
		t.Errorf("task 2 asks a Go-only repository to break import cycles the compiler rejects:\n%s", body)
	}
	if body := epicTaskBody(t, epic, 4); strings.Contains(body, "outside the gate") {
		t.Errorf("task 4 says the gate leaves a root Go module untested:\n%s", body)
	}
}

// Boundary: a configured framework the scan proposes no substitution against omits the
// substitution task too, and publishing creates only the planned tasks, the task after the
// omitted slot chaining onto the real number of the task before it.
func TestGeneratePreMigrationEpic_Boundary_ConfiguredFrameworkWithoutSubstitution(t *testing.T) {
	repo := t.TempDir()
	writeRepoFile(t, filepath.Join(repo, "go.mod"), "module github.com/test/unmapped\ngo 1.27\nrequire github.com/unknown/lib v1.0.0\n")
	epic, err := GeneratePreMigrationEpic(t.Context(), repo, acmeSource(""), nil)
	if err != nil {
		t.Fatalf("epic generation failed: %v", err)
	}
	if len(epic.OmittedTasks) != 1 || epic.OmittedTasks[0].Task != 3 ||
		!strings.HasPrefix(epic.OmittedTasks[0].Reason, "no substitution is proposed against `"+acmeKit+"`") {
		t.Fatalf("omitted tasks = %+v", epic.OmittedTasks)
	}
	f := &fakeForge{}
	parent, children, err := PublishPreMigrationEpic(t.Context(), f, epic)
	if err != nil || parent == nil || len(children) != totalEpicTasks-1 {
		t.Fatalf("publish = %v, %d children", err, len(children))
	}
	for _, spec := range f.created {
		if strings.HasPrefix(spec.Title, "[TASK 3/5]") {
			t.Errorf("the omitted task was published: %s", spec.Title)
		}
	}
	gate := f.created[3]
	if want := fmt.Sprintf("test/unmapped#%d", children[1].Number); !strings.HasPrefix(gate.Title, "[TASK 4/5]") ||
		len(gate.DependsOn) != 1 || gate.DependsOn[0] != want {
		t.Errorf("%s chains onto %v, want %q", gate.Title, gate.DependsOn, want)
	}
}

// Boundary: runner routing is work only when it says something. An empty runners section
// declares nothing; a configuration that does not load becomes repair work that names no local
// path, instead of failing the epic.
func TestGeneratePreMigrationEpic_Boundary_RunnerRoutingEdges(t *testing.T) {
	for _, tc := range []struct {
		name, fleet, want string
	}{
		{"empty runners section", "runners: {}\n", ""},
		{"unloadable configuration", "runners: [\n", "Repair the runner routing configuration"},
	} {
		repo := writeRustWorkspaceRepo(t)
		writeRepoFile(t, filepath.Join(repo, ".config", "fleet.yaml"), tc.fleet)
		epic, err := GeneratePreMigrationEpic(t.Context(), repo, FrameworkSource{}, nil)
		if err != nil {
			t.Fatalf("%s: epic generation failed: %v", tc.name, err)
		}
		body := epicTaskBody(t, epic, 5)
		if routed := strings.Contains(body, "runner routing"); routed != (tc.want != "") || !strings.Contains(body, tc.want) {
			t.Errorf("%s: task 5 =\n%s", tc.name, body)
		}
		if strings.Contains(epic.ChecklistMarkdown+body, repo) {
			t.Errorf("%s: the epic names the local path %s", tc.name, repo)
		}
	}
}

// Boundary: a repository mixing a root Go module with TypeScript is phrased for both, in table
// order; only TypeScript admits module cycles and only TypeScript runs outside the gate.
func TestGeneratePreMigrationEpic_Boundary_MixedLanguages(t *testing.T) {
	repo := writeEpicFixtureRepo(t)
	writeRepoFile(t, filepath.Join(repo, "package.json"), "{\"name\": \"epic-target-ui\", \"version\": \"1.0.0\", \"dependencies\": {\"axios\": \"^1.0.0\"}}\n")
	epic, err := GeneratePreMigrationEpic(t.Context(), repo, acmeSource(""), nil)
	if err != nil {
		t.Fatalf("epic generation failed: %v", err)
	}
	checks := map[string]string{
		epic.ChecklistMarkdown:   "- **Detected Languages**: Go, TypeScript\n",
		epicTaskBody(t, epic, 1): "TypeScript: add 3D unit tests (positive, negative, boundary) and run them with the package's `test` script.",
		epicTaskBody(t, epic, 2): "Break circular dependencies between modules (TypeScript).",
		epicTaskBody(t, epic, 4): "run the task 1 tests and audits for TypeScript outside the gate",
	}
	for text, want := range checks {
		if !strings.Contains(text, want) {
			t.Errorf("lacks %q:\n%s", want, text)
		}
	}
}

// Boundary: a language no step set covers gets the generic steps, which name no language and
// start their own sentence; an empty step renders as nothing rather than panicking.
func TestDetectedLanguageSteps_Boundary_UnknownLanguage(t *testing.T) {
	steps := detectedLanguageSteps(&RepoNeeds{Language: "zig", Languages: []string{"zig"}})
	if len(steps) != 1 || steps[0].name != "" || steps[0].tests != genericLanguageSteps.tests {
		t.Fatalf("unknown language steps = %+v", steps)
	}
	if got := languageLine("", "eliminate x."); got != "Eliminate x." {
		t.Errorf("generic line = %q", got)
	}
	if got := languageLine("", ""); got != "" {
		t.Errorf("empty generic line = %q", got)
	}
	if got := languageNames(steps, func(languageSteps) bool { return true }); got != "" {
		t.Errorf("generic steps are named %q", got)
	}
	native := detectedLanguageSteps(&RepoNeeds{Language: "native", Languages: []string{"c", "cpp", "cuda"}})
	if len(native) != 1 || native[0].name != "C/C++" {
		t.Errorf("native steps = %+v", native)
	}
}

// Negative: a cancelled context is the one runner routing failure that fails the epic.
func TestResolveRunnerRouting_Negative_CancelledContext(t *testing.T) {
	repo := t.TempDir()
	writeRepoFile(t, filepath.Join(repo, ".config", "fleet.yaml"), "runners:\n  default: example\n")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := resolveRunnerRouting(ctx, repo); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled resolution = %v, want context.Canceled", err)
	}
	if routing, err := resolveRunnerRouting(t.Context(), repo); err != nil || !routing.declared || routing.loadErr != nil {
		t.Fatalf("declared routing = %+v, %v", routing, err)
	}
	if routing, err := resolveRunnerRouting(t.Context(), filepath.Join(repo, "absent")); err != nil || routing.declared {
		t.Fatalf("absent repository routing = %+v, %v", routing, err)
	}
	if _, statErr := os.Stat(filepath.Join(repo, ".config", "fleet.yaml")); statErr != nil {
		t.Fatal(statErr)
	}
}

// unanalyzedNote is the checklist line naming source languages no needs analyzer detects.
const unanalyzedNote = "; no `needs` analyzer detects them, so tasks 1 and 4 name them as unverified\n"

// Positive (#296): shell scripts beside a Python package are named in the checklist with
// their file count, and tasks 1 and 4 name them as unverified instead of leaving them out.
func TestGeneratePreMigrationEpic_Positive_UnanalyzedLanguagesAreNamed(t *testing.T) {
	repo := t.TempDir()
	writeRepoFile(t, filepath.Join(repo, "requirements.txt"), "pytest\n")
	writeRepoFile(t, filepath.Join(repo, "tests", "test_boot.py"), "def test_boot():\n    pass\n")
	for _, script := range []string{"build.sh", "package.sh", "release.sh"} {
		writeRepoFile(t, filepath.Join(repo, "scripts", script), "#!/bin/sh\nset -eu\n")
	}
	epic, err := GeneratePreMigrationEpic(t.Context(), repo, FrameworkSource{}, nil)
	if err != nil {
		t.Fatalf("epic generation failed: %v", err)
	}
	for _, want := range []string{"- **Detected Languages**: Python\n", "- **Unanalyzed Languages**: shell (3 files)" + unanalyzedNote} {
		if !strings.Contains(epic.ChecklistMarkdown, want) {
			t.Errorf("checklist lacks %q:\n%s", want, epic.ChecklistMarkdown)
		}
	}
	wants := map[int]string{
		1: "Unverified, no `needs` analyzer detects them: shell (3 files). Choose an error-handling audit and a 3D test runner for them.",
		4: "Unverified, no `needs` analyzer detects them: shell (3 files). The gate checks none of them: run their tests and audits outside the gate.",
	}
	for n, want := range wants {
		if body := epicTaskBody(t, epic, n); !strings.Contains(body, want) {
			t.Errorf("task %d lacks %q:\n%s", n, want, body)
		}
	}
	if body := epicTaskBody(t, epic, 1); strings.Contains(body, "python (") {
		t.Errorf("task 1 names the analyzed Python as unverified:\n%s", body)
	}
}

// Negative (#296): source every analyzer covers is never named as unanalyzed - JavaScript in
// a package the Node analyzer reads included - and a failed source listing says so instead
// of naming nothing.
func TestGeneratePreMigrationEpic_Negative_AnalyzedSourceIsNotUnverified(t *testing.T) {
	node := t.TempDir()
	writeRepoFile(t, filepath.Join(node, "package.json"), "{\"name\":\"web\",\"dependencies\":{\"zod\":\"3\"}}")
	writeRepoFile(t, filepath.Join(node, "src", "index.js"), "export const a = 1;\n")
	writeRepoFile(t, filepath.Join(node, "src", "view.vue"), "<template><p/></template>\n")
	rust := writeRustWorkspaceRepo(t)
	writeRepoFile(t, filepath.Join(rust, "engine", "src", "lib.rs"), "pub fn run() {}\n")
	for _, repo := range []string{node, rust} {
		epic, err := GeneratePreMigrationEpic(t.Context(), repo, FrameworkSource{}, nil)
		if err != nil {
			t.Fatalf("epic generation failed: %v", err)
		}
		if strings.Contains(renderEpicDocument(epic), "Unanalyzed Languages") || strings.Contains(renderEpicDocument(epic), "Unverified") {
			t.Errorf("fully analyzed repository names unanalyzed source:\n%s", renderEpicDocument(epic))
		}
	}
	// A .git directory git does not accept as a repository fails the listing.
	broken := t.TempDir()
	writeRepoFile(t, filepath.Join(broken, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeRepoFile(t, filepath.Join(broken, "requirements.txt"), "pytest\n")
	writeRepoFile(t, filepath.Join(broken, "build.sh"), "#!/bin/sh\n")
	epic, err := GeneratePreMigrationEpic(t.Context(), broken, FrameworkSource{}, nil)
	if err != nil {
		t.Fatalf("epic generation failed: %v", err)
	}
	if !strings.Contains(epic.ChecklistMarkdown, "- **Unanalyzed Languages**: not inventoried: the source listing failed") ||
		strings.Contains(renderEpicDocument(epic), "Unverified") || strings.Contains(epic.ChecklistMarkdown, broken) {
		t.Errorf("failed listing is not named, or names a local path:\n%s", epic.ChecklistMarkdown)
	}
}

// Boundary (#296): one file is counted in the singular, test fixtures are not counted, and a
// language whose analyzer finds no project (Python without a manifest) is unanalyzed.
func TestGeneratePreMigrationEpic_Boundary_UnanalyzedCounts(t *testing.T) {
	repo := writeRustWorkspaceRepo(t)
	writeRepoFile(t, filepath.Join(repo, "tools", "bump.py"), "print('bump')\n")
	writeRepoFile(t, filepath.Join(repo, "ci.sh"), "#!/bin/sh\n")
	writeRepoFile(t, filepath.Join(repo, "testdata", "case.sh"), "#!/bin/sh\n")
	epic, err := GeneratePreMigrationEpic(t.Context(), repo, FrameworkSource{}, nil)
	if err != nil {
		t.Fatalf("epic generation failed: %v", err)
	}
	if want := "- **Unanalyzed Languages**: python (1 file), shell (1 file)" + unanalyzedNote; !strings.Contains(epic.ChecklistMarkdown, want) {
		t.Errorf("checklist lacks %q:\n%s", want, epic.ChecklistMarkdown)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := unanalyzedLanguages(cancelled, repo, &RepoNeeds{Language: "rust"}); err == nil {
		t.Error("a cancelled inventory returned counts")
	}
}
