// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"github.com/cordanaLLM/praetor/internal/managedasset"
	"github.com/cordanaLLM/praetor/internal/supplychain"
	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

// reuseGoldenRoot holds the lefthook.yml adoption writes into a REUSE repository, one per
// language set (lefthookLanguageSets), without the checkpoint jobs; the emitted fixture under
// testdata/emitted is the superset with them.
const reuseGoldenRoot = "testdata/lefthook-reuse"

// reuseGoldenNames names the golden of each language set.
var reuseGoldenNames = map[hisscatalog.Language]string{
	0:                        "governance.lefthook.yml",
	hisscatalog.LanguageGo:   "go.lefthook.yml",
	hisscatalog.LanguageRust: "rust.lefthook.yml",
	lefthookJobLanguages:     "go-rust.lefthook.yml",
}

// Positive: a REUSE repository's rendering, for every language set, carries the reuse-lint
// pre-commit job running reuseLintCommand, names the pin in its header, and stays within
// yamllint's defaults; each is committed as a golden. Negative: the rendering without the switch
// names reuse nowhere, so a repository without REUSE keeps the bytes adoption wrote before.
func TestLefthookReuseJob_RenderingPerLanguageSet(t *testing.T) {
	for _, languages := range lefthookLanguageSets {
		rendering := buildLefthookYAMLFor(lefthookShape{languages: languages, reuse: true}, false)
		var decoded map[string]any
		if err := yaml.Unmarshal([]byte(rendering), &decoded); err != nil {
			t.Fatalf("languages=%v: decode: %v", languages, err)
		}
		if run := decodedJob(t, decoded, "pre-commit", reuseLintJob)["run"]; run != reuseLintCommand() {
			t.Errorf("languages=%v: the reuse-lint job runs %v", languages, run)
		}
		if !strings.Contains(rendering, "# reuse lint at the release line "+supplychain.ReuseActionRef()) {
			t.Errorf("languages=%v: the header does not name the pin", languages)
		}
		for i, line := range strings.Split(rendering, "\n") {
			if len(line) > yamlLineLimit {
				t.Errorf("languages=%v line %d: %d columns: %s", languages, i+1, len(line), line)
			}
		}
		compareGolden(t, filepath.Join(reuseGoldenRoot, reuseGoldenNames[languages]), rendering)
		for _, checkpoint := range []bool{false, true} {
			if without := buildLefthookYAMLFor(lefthookShape{languages: languages}, checkpoint); strings.Contains(strings.ToLower(without), "reuse") {
				t.Errorf("languages=%v checkpoint=%v: the rendering without REUSE names reuse", languages, checkpoint)
			}
		}
	}
}

// compareGolden fails unless the committed file at path holds want, line endings aside, or
// rewrites it under updateEmittedFixturesEnv.
func compareGolden(t *testing.T, path, want string) {
	t.Helper()
	if os.Getenv(updateEmittedFixturesEnv) == "1" {
		mustWrite(t, path, want)
		return
	}
	if got := strings.ReplaceAll(mustRead(t, path), "\r\n", "\n"); got != want {
		t.Errorf("%s differs from the rendering; regenerate it with %s=1 go test ./internal/adopt -run %s",
			path, updateEmittedFixturesEnv, t.Name())
	}
}

// reuseJobTools links sh and the tools the reuse-lint line pipes through into a directory of
// their own, so a PATH of it and a stub directory holds no other reuse. It skips where no POSIX
// shell runs the line: lefthook runs every job through sh, Git's on Windows, which the Windows
// test host need not have, and a link to it does not start there.
func reuseJobTools(t *testing.T) (shell, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the reuse-lint line is POSIX sh; lefthook runs it through Git's sh on Windows, which this host need not provide")
	}
	dir = t.TempDir()
	for _, tool := range []string{"sh", "head", "tr", "cut"} {
		path, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("%s required to run the reuse-lint line: %v", tool, err)
		}
		if err := os.Symlink(path, filepath.Join(dir, tool)); err != nil {
			t.Fatalf("link %s: %v", tool, err)
		}
	}
	return filepath.Join(dir, "sh"), dir
}

// runReuseLine runs the reuse-lint line through sh -c in root with PATH set to path, as lefthook
// runs it, and returns its exit code and output.
func runReuseLine(t *testing.T, shell, root, path string) (int, string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), shell, "-c", reuseLintCommand())
	cmd.Dir = root
	environ := util.FilterEnvironment(os.Environ(), func(name string) bool { return name == "PATH" })
	cmd.Env = append(environ, "PATH="+path)
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatalf("run the reuse-lint line: %v", err)
	}
	return cmd.ProcessState.ExitCode(), string(output)
}

// The reuse-lint line is run, not only compared, against a stub reuse. Positive: at the pinned
// major the job's status is reuse lint's. Negative: a failing reuse lint fails it, and another
// reuse major fails it naming the pin. Boundary: without reuse on PATH, or without REUSE.toml
// and LICENSES/ at the root, it skips with the reason; REUSE.toml alone runs the lint.
func TestReuseLintCommand_RunsTheLintAtThePinnedMajor(t *testing.T) {
	shell, tools := reuseJobTools(t)
	stub := func(version string, lint int) string {
		dir := t.TempDir()
		writeStub(t, dir, "reuse", "if [ \"$1\" = --version ]; then echo 'reuse, version "+version+"'; exit 0; fi\nexit "+
			string(rune('0'+lint))+"\n")
		return dir + string(os.PathListSeparator) + tools
	}
	licensed := t.TempDir()
	mustWrite(t, filepath.Join(licensed, supplychain.LicensesDir, "MIT.txt"), "MIT License\n")
	tomlOnly := t.TempDir()
	mustWrite(t, filepath.Join(tomlOnly, supplychain.ReuseFile), "version = 1\n")
	pinned := supplychain.ReuseMajor() + ".2.0"
	for _, tc := range []struct {
		name, root, path, output string
		code                     int
	}{
		{"pinned major, lint passes", licensed, stub(pinned, 0), "", 0},
		{"pinned major, lint fails", licensed, stub(pinned, 1), "", 1},
		{"another major", licensed, stub("5.1.0", 0), "reuse " + supplychain.ReuseMajor() + ".x is required", 1},
		{"reuse not installed", licensed, tools, "reuse is not installed, skipping", 0},
		{"no REUSE.toml or LICENSES/", t.TempDir(), stub(pinned, 1), "skipping reuse lint", 0},
		{"REUSE.toml alone", tomlOnly, stub(pinned, 1), "", 1},
	} {
		code, output := runReuseLine(t, shell, tc.root, tc.path)
		if code != tc.code || !strings.Contains(output, tc.output) {
			t.Errorf("%s: exit %d, want %d and %q: %s", tc.name, code, tc.code, tc.output, output)
		}
	}
}

// Negative, with the real tool: the emitted job fails a tree holding a file without licensing
// information, and passes once every file carries it. It runs where reuse at the pinned major is
// installed, and otherwise skips naming why; the stub test above holds the line's logic.
func TestReuseLintCommand_Negative_UnlabelledFileFailsTheJob(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil || runtime.GOOS == "windows" {
		t.Skip("the reuse-lint line is POSIX sh; lefthook runs it through Git's sh on Windows, which this host need not provide")
	}
	version, err := exec.CommandContext(t.Context(), "reuse", "--version").Output()
	if err != nil || !strings.Contains(string(version), "version "+supplychain.ReuseMajor()+".") {
		t.Skipf("reuse %s.x is not installed (%v); TestReuseLintCommand_RunsTheLintAtThePinnedMajor covers the line with a stub",
			supplychain.ReuseMajor(), err)
	}
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, supplychain.LicensesDir, "MIT.txt"), "MIT License\n")
	// The tags are split so reuse lint of this repository does not read them as this file's.
	mustWrite(t, filepath.Join(root, "labelled.txt"), "SPDX-"+"FileCopyrightText: 2026 Example\nSPDX-"+"License-Identifier: MIT\n")
	if code, output := runReuseLine(t, shell, root, os.Getenv("PATH")); code != 0 {
		t.Fatalf("every file labelled: exit %d, want 0: %s", code, output)
	}
	mustWrite(t, filepath.Join(root, "unlabelled.txt"), "no licensing information\n")
	if code, output := runReuseLine(t, shell, root, os.Getenv("PATH")); code == 0 || !strings.Contains(output, "unlabelled.txt") {
		t.Fatalf("an unlabelled file: exit %d, want a failure naming it: %s", code, output)
	}
}

// Positive: adoption writes the hosted REUSE gate and the reuse-lint job into a repository whose
// root carries LICENSES/, derives the gate's job into the ruleset and names what it runs in the
// harness. Negative: a repository with neither marker gets neither. Boundary: a declined
// reuse-gate step writes no workflow and the harness claims none.
func TestAdopt_ReuseGateFollowsTheRoot(t *testing.T) {
	reuseRepo := newTestRepo(t, "reuse-gate")
	mustWrite(t, filepath.Join(reuseRepo, supplychain.LicensesDir, "MIT.txt"), "MIT License\n")
	rep := adoptWithSource(t, reuseRepo, newAdoptLockSource(t), false)
	if got := mustRead(t, filepath.Join(reuseRepo, filepath.FromSlash(reuseWorkflowFile))); got != reuseWorkflow(forge.FallbackDefaultBranch) {
		t.Fatalf("the hosted REUSE gate is not the rendering:\n%s", got)
	}
	if !hasAction(rep, reuseWorkflowFile, actionCreate) {
		t.Errorf("the gate was not reported created: %+v", rep.ActionDetails)
	}
	if got := mustRead(t, filepath.Join(reuseRepo, lefthookFile)); got != buildLefthookYAMLFor(lefthookShape{languages: lefthookJobLanguages, reuse: true}, false) {
		t.Errorf("lefthook.yml lacks the reuse-lint job:\n%s", got)
	}
	if ruleset := mustRead(t, filepath.Join(reuseRepo, filepath.FromSlash(rulesetFile))); !strings.Contains(ruleset, "REUSE lint") {
		t.Errorf("the ruleset does not require the gate's job:\n%s", ruleset)
	}
	if harness := mustRead(t, filepath.Join(reuseRepo, agentsFile)); !strings.Contains(harness,
		"`"+reuseWorkflowFile+"` runs no command of its own, only the actions `actions/checkout@v7`, `"+supplychain.ReuseActionRef()+"`") {
		t.Errorf("the harness does not name what the gate runs:\n%s", harness)
	}

	plain := newTestRepo(t, "no-reuse-gate")
	adoptWithSource(t, plain, newAdoptLockSource(t), false)
	if fileExists(filepath.Join(plain, filepath.FromSlash(reuseWorkflowFile))) {
		t.Error("a repository without REUSE.toml or LICENSES/ got the hosted REUSE gate")
	}
	if strings.Contains(mustRead(t, filepath.Join(plain, lefthookFile)), reuseLintJob) {
		t.Error("a repository without REUSE.toml or LICENSES/ got the reuse-lint job")
	}

	declined := newTestRepo(t, "declined-reuse-gate")
	mustWrite(t, filepath.Join(declined, manifestFile), "version: 1\nadoption:\n  decline: ["+reuseGateStep+"]\n")
	mustWrite(t, filepath.Join(declined, supplychain.ReuseFile), "version = 1\n")
	if _, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: declined, Profile: "framework"}); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if fileExists(filepath.Join(declined, filepath.FromSlash(reuseWorkflowFile))) {
		t.Error("a declined reuse-gate step wrote the hosted REUSE gate")
	}
	if strings.Contains(mustRead(t, filepath.Join(declined, agentsFile)), reuseWorkflowFile) {
		t.Error("the harness claims a declined hosted REUSE gate")
	}
}

// Boundary: lefthook.yml adoption wrote before the root carried REUSE gains the reuse-lint job,
// and one written while it did loses the job once the root carries neither marker. Both are
// Praetor's own text, so neither needs --force, and the checkpoint jobs survive the migration.
func TestAdopt_Boundary_LefthookFollowsTheReuseSwitch(t *testing.T) {
	gains := newTestRepo(t, "lefthook-gains-reuse")
	mustWrite(t, filepath.Join(gains, supplychain.ReuseFile), "version = 1\n")
	mustWrite(t, filepath.Join(gains, lefthookFile), buildLefthookYAMLFor(lefthookShape{languages: lefthookJobLanguages}, true))
	rep := adoptWithSource(t, gains, newAdoptLockSource(t), false)
	if got := mustRead(t, filepath.Join(gains, lefthookFile)); got != buildLefthookYAMLFor(lefthookShape{languages: lefthookJobLanguages, reuse: true}, true) {
		t.Fatalf("the reuse-lint job was not added with the checkpoint jobs kept:\n%s", got)
	}
	if !strings.Contains(lefthookDetails(rep), "Added the reuse-lint job") || hasAction(rep, lefthookFile, actionReplace) {
		t.Fatalf("want a reconcile adding the job, no replace: %+v", rep.ActionDetails)
	}

	loses := newTestRepo(t, "lefthook-loses-reuse")
	mustWrite(t, filepath.Join(loses, lefthookFile), buildLefthookYAMLFor(lefthookShape{languages: lefthookJobLanguages, reuse: true}, false))
	rep = adoptWithSource(t, loses, newAdoptLockSource(t), false)
	if got := mustRead(t, filepath.Join(loses, lefthookFile)); got != buildLefthookYAMLFor(lefthookShape{languages: lefthookJobLanguages}, false) {
		t.Fatalf("the reuse-lint job was not removed:\n%s", got)
	}
	if !strings.Contains(lefthookDetails(rep), "Removed the reuse-lint job") {
		t.Fatalf("want a reconcile removing the job: %+v", rep.ActionDetails)
	}
	for _, shape := range []lefthookShape{{languages: lefthookJobLanguages, reuse: true}, {languages: hisscatalog.LanguageGo}} {
		if isPriorLefthookConfig([]byte(buildLefthookYAMLFor(shape, false))) {
			t.Errorf("shape %+v: a current rendering is recorded as an earlier one", shape)
		}
		if got := classifyLefthookConfig([]byte(buildLefthookYAMLFor(shape.otherReuse(), false)), shape); got != (lefthookIdentity{prior: true}) {
			t.Errorf("shape %+v: the rendering for the other switch classifies as %+v, want prior", shape, got)
		}
	}
}

// Boundary: an edited hosted REUSE gate is the repository's: kept, --force included, and not
// claimed by the harness, which under-claims rather than credit a gate the repository changed.
func TestAdopt_Boundary_EditedReuseGateIsKept(t *testing.T) {
	repoPath := newTestRepo(t, "edited-reuse-gate")
	mustWrite(t, filepath.Join(repoPath, supplychain.LicensesDir, "MIT.txt"), "MIT License\n")
	edited := strings.Replace(reuseWorkflow(forge.FallbackDefaultBranch), "name: REUSE\n", "name: Licensing\n", 1)
	mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(reuseWorkflowFile)), edited)
	adoptWithSource(t, repoPath, newAdoptLockSource(t), true)
	if got := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(reuseWorkflowFile))); got != edited {
		t.Fatalf("the edited gate was replaced:\n%s", got)
	}
	if strings.Contains(mustRead(t, filepath.Join(repoPath, agentsFile)), reuseWorkflowFile) {
		t.Error("the harness claims an edited hosted REUSE gate")
	}
}

// Boundary: the current hosted REUSE gate for main is recorded among the earlier renderings, so
// the release that changes it still refreshes this one without --force, and every recorded digest
// is reproduced by a text under testdata/reuse-workflow.
func TestPriorReuseWorkflowDigests_Boundary_CurrentRenderingRecorded(t *testing.T) {
	digest, _, err := util.CanonicalTextDigest([]byte(reuseWorkflow(forge.FallbackDefaultBranch)))
	if err != nil {
		t.Fatalf("digest the rendering: %v", err)
	}
	if _, recorded := priorReuseWorkflowDigests[digest]; !recorded {
		t.Fatalf("the current hosted REUSE gate (%s) is not recorded in priorReuseWorkflowDigests", digest)
	}
	entries, err := os.ReadDir(filepath.Join("testdata", "reuse-workflow"))
	if err != nil {
		t.Fatalf("read the recorded texts: %v", err)
	}
	reproduced := map[string]bool{}
	for _, entry := range entries {
		text, _, err := util.CanonicalTextDigest([]byte(mustRead(t, filepath.Join("testdata", "reuse-workflow", entry.Name()))))
		if err != nil {
			t.Fatalf("digest %s: %v", entry.Name(), err)
		}
		reproduced[text] = true
	}
	for recorded, producer := range priorReuseWorkflowDigests {
		if !reproduced[recorded] {
			t.Errorf("no text under testdata/reuse-workflow reproduces %s (%s)", recorded, producer)
		}
	}
}

// Positive: a workflow whose steps run no command of their own is named by the actions they use.
// Negative: a workflow running commands is named by them alone. Boundary: one with neither runs
// no command, as before.
func TestWorkflowClaim_NamesActionsOnlyWithoutCommands(t *testing.T) {
	actionsOnly := scaffoldedWorkflow{path: reuseWorkflowFile, actions: []string{"actions/checkout@v7", "a/b@v1"}}
	if got := workflowClaim(actionsOnly); got != "no command of its own, only the actions `actions/checkout@v7`, `a/b@v1`" {
		t.Errorf("actions only: %q", got)
	}
	withRuns := actionsOnly
	withRuns.runs = []forge.WorkflowRun{{Label: "go vet ./..."}}
	if got := workflowClaim(withRuns); got != "`go vet ./...`" {
		t.Errorf("with runs: %q", got)
	}
	if got := workflowClaim(scaffoldedWorkflow{path: "x.yml"}); got != "no command" {
		t.Errorf("neither: %q", got)
	}
}

// The hosted REUSE gate runs on the default branch the ruleset requiring its job protects, within
// a timeout. Positive: the rendering for master names master on its push and pull request triggers,
// sets timeout-minutes, and reads back as Praetor's rendering for master. Negative: an edited
// rendering is no rendering. Boundary: a branch YAML would read as a number or a boolean stays a
// string, and a name config.ValidBranchName refuses is no rendering.
func TestReuseWorkflow_RendersTheDefaultBranch(t *testing.T) {
	var decoded struct {
		On map[string]struct {
			Branches []any `yaml:"branches"`
		} `yaml:"on"`
		Jobs map[string]struct {
			Timeout int `yaml:"timeout-minutes"`
		} `yaml:"jobs"`
	}
	for _, branch := range []string{"master", "1.0", "true"} {
		rendering := reuseWorkflow(branch)
		if err := yaml.Unmarshal([]byte(rendering), &decoded); err != nil {
			t.Fatalf("%s: decode: %v", branch, err)
		}
		for _, event := range []string{"push", "pull_request"} {
			if branches := decoded.On[event].Branches; len(branches) != 1 || branches[0] != branch {
				t.Errorf("%s: the %s trigger runs on %#v", branch, event, branches)
			}
		}
		if decoded.Jobs["reuse"].Timeout != 10 {
			t.Errorf("%s: timeout-minutes %d, want 10", branch, decoded.Jobs["reuse"].Timeout)
		}
		if got, rendered := reuseRenderingBranch([]byte(rendering)); !rendered || got != branch {
			t.Errorf("%s: read back as %q, %v", branch, got, rendered)
		}
	}
	edited := strings.Replace(reuseWorkflow("master"), "timeout-minutes: 10", "timeout-minutes: 30", 1)
	if _, rendered := reuseRenderingBranch([]byte(edited)); rendered || isReuseRendering([]byte(edited)) {
		t.Error("an edited rendering reads as Praetor's")
	}
	if _, rendered := reuseRenderingBranch([]byte(reuseWorkflow("a b"))); rendered {
		t.Error("a rendering for a name that is no branch reads as Praetor's")
	}
}

// Adoption renders the hosted REUSE gate for the declared default branch. Positive: a repository
// declaring master gets the master rendering. Boundary: the unedited rendering for main, written
// before the repository declared master, is refreshed to master without --force.
func TestAdopt_ReuseGateFollowsTheDefaultBranch(t *testing.T) {
	for name, existing := range map[string]string{"absent": "", "main rendering": reuseWorkflow(forge.FallbackDefaultBranch)} {
		repoPath := newTestRepo(t, "reuse-gate-master-"+strings.ReplaceAll(name, " ", "-"))
		mustWrite(t, filepath.Join(repoPath, manifestFile), "version: 1\nrepository:\n  owner: acme\n  name: x\n  default_branch: master\n")
		mustWrite(t, filepath.Join(repoPath, supplychain.LicensesDir, "MIT.txt"), "MIT License\n")
		if existing != "" {
			mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(reuseWorkflowFile)), existing)
		}
		rep := adoptWithSource(t, repoPath, newAdoptLockSource(t), false)
		if got := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(reuseWorkflowFile))); got != reuseWorkflow("master") {
			t.Fatalf("%s: the gate is not the master rendering:\n%s", name, got)
		}
		if existing != "" && (hasAction(rep, reuseWorkflowFile, actionReplace) || !strings.Contains(reuseGateDetails(rep), "Refreshed")) {
			t.Fatalf("%s: want a refresh without --force: %+v", name, rep.ActionDetails)
		}
	}
}

// reuseGateDetails joins the report details adoption recorded for the hosted REUSE gate.
func reuseGateDetails(rep *AdoptReport) string {
	var details []string
	for _, detail := range rep.ActionDetails {
		if detail.Path == reuseWorkflowFile {
			details = append(details, detail.Action+": "+detail.Details)
		}
	}
	return strings.Join(details, "\n")
}

// A root that loses REUSE.toml and LICENSES/ loses the hosted REUSE gate too, as lefthook.yml
// loses its reuse-lint job. Positive: Praetor's unedited rendering, for main or another branch, is
// removed, reported as a removal, and the ruleset no longer requires its job. Negative: an edited
// gate is kept, with a warning naming the check it fails. Boundary: a dry run removes nothing and
// previews the removal.
func TestAdopt_ReuseGateRemovedWithTheMarkers(t *testing.T) {
	for _, branch := range []string{forge.FallbackDefaultBranch, "trunk"} {
		repoPath := newTestRepo(t, "reuse-gate-gone-"+branch)
		mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(reuseWorkflowFile)), reuseWorkflow(branch))
		rep := adoptWithSource(t, repoPath, newAdoptLockSource(t), false)
		if fileExists(filepath.Join(repoPath, filepath.FromSlash(reuseWorkflowFile))) || !hasAction(rep, reuseWorkflowFile, actionRemove) {
			t.Fatalf("%s: the unedited gate was not removed: %+v", branch, rep.ActionDetails)
		}
		if ruleset := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(rulesetFile))); strings.Contains(ruleset, "REUSE lint") {
			t.Fatalf("%s: the ruleset still requires the removed gate:\n%s", branch, ruleset)
		}
	}

	edited := newTestRepo(t, "reuse-gate-gone-edited")
	text := strings.Replace(reuseWorkflow(forge.FallbackDefaultBranch), "name: REUSE\n", "name: Licensing\n", 1)
	mustWrite(t, filepath.Join(edited, filepath.FromSlash(reuseWorkflowFile)), text)
	rep := adoptWithSource(t, edited, newAdoptLockSource(t), true)
	if got := mustRead(t, filepath.Join(edited, filepath.FromSlash(reuseWorkflowFile))); got != text {
		t.Fatalf("the edited gate was not kept:\n%s", got)
	}
	if !slices.ContainsFunc(rep.Warnings, func(warning string) bool {
		return strings.Contains(warning, reuseWorkflowFile+" kept: the root carries neither REUSE.toml nor LICENSES/")
	}) {
		t.Fatalf("no warning about the kept gate: %v", rep.Warnings)
	}

	dry := newTestRepo(t, "reuse-gate-gone-dry")
	mustWrite(t, filepath.Join(dry, filepath.FromSlash(reuseWorkflowFile)), reuseWorkflow(forge.FallbackDefaultBranch))
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: dry, DryRun: true})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if !fileExists(filepath.Join(dry, filepath.FromSlash(reuseWorkflowFile))) || !hasAction(rep, reuseWorkflowFile, actionRemove) {
		t.Fatalf("a dry run must keep the file and report the removal: %+v", rep.ActionDetails)
	}
}

// workflowActions names what a workflow uses through util.ScanActionUses. Positive: every uses:
// value in file order, a release comment and quotes dropped, a reusable workflow's job-level uses
// included. Negative: a workflow past managedasset.MaxWorkflowLines is refused, not read in part.
// Boundary: a workflow using nothing names nothing.
func TestWorkflowActions_3D(t *testing.T) {
	workflow := "jobs:\n  b:\n    steps:\n      - uses: x/second@v1\n  a:\n    steps:\n" +
		"      - uses: 'actions/checkout@v7'\n      - run: make\n      - uses: fsfe/reuse-action@" + strings.Repeat("a", 40) + " # v6.0.0\n" +
		"  call:\n    uses: acme/ci/.github/workflows/ci.yml@v1\n"
	want := []string{"x/second@v1", "actions/checkout@v7", "fsfe/reuse-action@" + strings.Repeat("a", 40), "acme/ci/.github/workflows/ci.yml@v1"}
	if actions, err := workflowActions(workflow); err != nil || !slices.Equal(actions, want) {
		t.Fatalf("workflowActions = %q, %v; want %q", actions, err, want)
	}
	if _, err := workflowActions(strings.Repeat("\n", managedasset.MaxWorkflowLines)); err == nil {
		t.Fatal("a workflow past the line bound was read")
	}
	if actions, err := workflowActions("jobs:\n  a:\n    steps:\n      - run: make\n"); err != nil || len(actions) != 0 {
		t.Fatalf("a workflow using nothing: %q, %v", actions, err)
	}
}
