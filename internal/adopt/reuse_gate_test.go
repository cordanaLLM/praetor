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
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"github.com/cordanaLLM/praetor/internal/managedasset"
	"github.com/cordanaLLM/praetor/internal/supplychain"
	"github.com/cordanaLLM/praetor/internal/util"
	markdownassets "github.com/cordanaLLM/praetor/tools/markdownlint"
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

func mustReuseWorkflow(t *testing.T, branch string) string {
	t.Helper()
	text, err := reuseWorkflow(branch)
	if err != nil {
		t.Fatalf("reuseWorkflow(%q): %v", branch, err)
	}
	return text
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
	if got := mustRead(t, filepath.Join(reuseRepo, filepath.FromSlash(reuseWorkflowFile))); got != mustReuseWorkflow(t, forge.FallbackDefaultBranch) {
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
		"`"+reuseWorkflowFile+"` runs step `"+ghworkflow.HostedGateDraftStepName+"`") {
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
	edited := strings.Replace(mustReuseWorkflow(t, forge.FallbackDefaultBranch), "name: REUSE\n", "name: Licensing\n", 1)
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
	digest, _, err := util.CanonicalTextDigest([]byte(mustReuseWorkflow(t, forge.FallbackDefaultBranch)))
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
// a timeout. Positive: the rendering for master names master on its push trigger,
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
		rendering := mustReuseWorkflow(t, branch)
		if err := yaml.Unmarshal([]byte(rendering), &decoded); err != nil {
			t.Fatalf("%s: decode: %v", branch, err)
		}
		if branches := decoded.On["push"].Branches; len(branches) != 1 || branches[0] != branch {
			t.Errorf("%s: the push trigger runs on %#v", branch, branches)
		}
		if branches := decoded.On["pull_request"].Branches; len(branches) != 0 {
			t.Errorf("%s: the pull_request trigger is filtered to %#v", branch, branches)
		}
		if decoded.Jobs["reuse"].Timeout != 10 {
			t.Errorf("%s: timeout-minutes %d, want 10", branch, decoded.Jobs["reuse"].Timeout)
		}
		got, rendered, err := reuseRenderingBranch([]byte(rendering))
		if err != nil {
			t.Fatalf("%s: reuseRenderingBranch err: %v", branch, err)
		}
		if !rendered || got != branch {
			t.Errorf("%s: read back as %q, %v", branch, got, rendered)
		}
	}
	edited := strings.Replace(mustReuseWorkflow(t, "master"), "timeout-minutes: 10", "timeout-minutes: 30", 1)
	if _, rendered, err := reuseRenderingBranch([]byte(edited)); err != nil || rendered {
		t.Errorf("reuseRenderingBranch(edited): rendered=%v, err=%v", rendered, err)
	}
	if rendered, err := isReuseRendering([]byte(edited)); err != nil || rendered {
		t.Errorf("isReuseRendering(edited): rendered=%v, err=%v", rendered, err)
	}
	if _, rendered, err := reuseRenderingBranch([]byte(mustReuseWorkflow(t, "a b"))); err != nil || rendered {
		t.Errorf("a rendering for a name that is no branch reads as Praetor's: rendered=%v, err=%v", rendered, err)
	}
}

// The hosted REUSE gate has the hosted gate shape every emitted gate shares. Positive: the
// rendering for main and for master passes ghworkflow.HostedGateFault for its branch. Negative: it
// fails the check for another branch, without the draft step, and with a step that lost its
// not-draft condition.
func TestReuseWorkflow_HasTheHostedGateShape(t *testing.T) {
	for _, branch := range []string{forge.FallbackDefaultBranch, "master"} {
		spec, err := ghworkflow.Parse([]byte(mustReuseWorkflow(t, branch)))
		if err != nil {
			t.Fatalf("%s: parse: %v", branch, err)
		}
		if err := ghworkflow.HostedGateFault(&spec, "reuse", branch); err != nil {
			t.Errorf("%s: %v", branch, err)
		}
		if err := ghworkflow.HostedGateFault(&spec, "reuse", branch+"-other"); err == nil {
			t.Errorf("%s: the shape check accepts another default branch", branch)
		}
	}
	rendering := mustReuseWorkflow(t, forge.FallbackDefaultBranch)
	mutants := map[string]string{
		"no draft step":       strings.Replace(rendering, ghworkflow.HostedGateDraftStep, "", 1),
		"unguarded lint":      strings.Replace(rendering, "reuse lint"+ghworkflow.HostedGateStepIf, "reuse lint", 1),
		"draft job skipped":   strings.Replace(rendering, "    timeout-minutes: 10\n", "    timeout-minutes: 10\n    if: "+ghworkflow.HostedGateNotDraft+"\n", 1),
		"pull request filter": strings.Replace(rendering, "  pull_request:\n", "  pull_request:\n    branches: ['main']\n", 1),
	}
	for name, mutant := range mutants {
		spec, err := ghworkflow.Parse([]byte(mutant))
		if err != nil {
			t.Fatalf("%s: parse: %v", name, err)
		}
		if ghworkflow.HostedGateFault(&spec, "reuse", forge.FallbackDefaultBranch) == nil {
			t.Errorf("%s: the shape check accepts the mutant", name)
		}
	}
}

// Adoption renders the hosted REUSE gate for the declared default branch. Positive: a repository
// declaring master gets the master rendering. Boundary: the unedited rendering for main, written
// before the repository declared master, is refreshed to master without --force.
func TestAdopt_ReuseGateFollowsTheDefaultBranch(t *testing.T) {
	for name, existing := range map[string]string{"absent": "", "main rendering": mustReuseWorkflow(t, forge.FallbackDefaultBranch)} {
		repoPath := newTestRepo(t, "reuse-gate-master-"+strings.ReplaceAll(name, " ", "-"))
		mustWrite(t, filepath.Join(repoPath, manifestFile), "version: 1\nrepository:\n  owner: acme\n  name: x\n  default_branch: master\n")
		mustWrite(t, filepath.Join(repoPath, supplychain.LicensesDir, "MIT.txt"), "MIT License\n")
		if existing != "" {
			mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(reuseWorkflowFile)), existing)
		}
		rep := adoptWithSource(t, repoPath, newAdoptLockSource(t), false)
		if got := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(reuseWorkflowFile))); got != mustReuseWorkflow(t, "master") {
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
		mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(reuseWorkflowFile)), mustReuseWorkflow(t, branch))
		rep := adoptWithSource(t, repoPath, newAdoptLockSource(t), false)
		if fileExists(filepath.Join(repoPath, filepath.FromSlash(reuseWorkflowFile))) || !hasAction(rep, reuseWorkflowFile, actionRemove) {
			t.Fatalf("%s: the unedited gate was not removed: %+v", branch, rep.ActionDetails)
		}
		if ruleset := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(rulesetFile))); strings.Contains(ruleset, "REUSE lint") {
			t.Fatalf("%s: the ruleset still requires the removed gate:\n%s", branch, ruleset)
		}
	}

	edited := newTestRepo(t, "reuse-gate-gone-edited")
	text := strings.Replace(mustReuseWorkflow(t, forge.FallbackDefaultBranch), "name: REUSE\n", "name: Licensing\n", 1)
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
	mustWrite(t, filepath.Join(dry, filepath.FromSlash(reuseWorkflowFile)), mustReuseWorkflow(t, forge.FallbackDefaultBranch))
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

// Positive: the hosted REUSE gate pins every action by full 40-hex commit SHA with its release
// version as a trailing comment, and the audit SHA-pin check (managedasset.UnpinnedActions)
// reports zero unpinned actions on the rendering and on the committed emitted fixture.
// Negative (Rule 13): the earlier tag-pinned rendering fails the check, returning the unpinned
// actions. Boundary: each uses: line carries a 40-hex SHA, a version comment, and a yamllint
// line-length exemption.
func TestReuseWorkflow_PinsActionsByDigest_3D(t *testing.T) {
	rendering := mustReuseWorkflow(t, forge.FallbackDefaultBranch)
	assertUnpinnedActions(t, rendering, "rendering", nil)

	emittedPath := filepath.Join(emittedFixtureRoot, filepath.FromSlash(reuseWorkflowFile))
	assertUnpinnedActions(t, mustRead(t, emittedPath), emittedPath, nil)

	oldPath := filepath.Join("testdata", "reuse-workflow", "reuse-action-v6.yml")
	wantOldUnpinned := []string{"uses: actions/checkout@v7", "uses: fsfe/reuse-action@v6"}
	assertUnpinnedActions(t, mustRead(t, oldPath), oldPath, wantOldUnpinned)

	assertUsesPins(t, rendering)
}

func assertUnpinnedActions(t *testing.T, workflow, name string, want []string) {
	t.Helper()
	unpinned, err := managedasset.UnpinnedActions(workflow)
	if err != nil {
		t.Fatalf("UnpinnedActions(%s): %v", name, err)
	}
	if (len(unpinned) == 0 && len(want) != 0) || (len(unpinned) != 0 && !slices.Equal(unpinned, want)) {
		t.Errorf("%s unpinned actions = %q, want %q", name, unpinned, want)
	}
}

func assertValidActionUse(t *testing.T, use util.ActionUse) {
	t.Helper()
	if !use.Pinned || !use.SHAPinned || len(use.Pin.SHA) != 40 || use.Pin.Release == "" {
		t.Errorf("action %q not fully SHA-pinned with release comment: %+v", use.Ref, use)
	}
}

func assertUsesPins(t *testing.T, rendering string) {
	t.Helper()
	_, uses, err := util.ScanActionUses(rendering, managedasset.MaxWorkflowLines)
	if err != nil {
		t.Fatalf("ScanActionUses(rendering): %v", err)
	}
	if len(uses) != 2 {
		t.Fatalf("rendering has %d action uses, want 2", len(uses))
	}
	for _, use := range uses {
		assertValidActionUse(t, use)
	}
	wantCheckout, err := scanCheckoutPinnedRef(markdownassets.Workflow)
	if err != nil {
		t.Fatalf("scan markdownassets.Workflow: %v", err)
	}
	if uses[0].Pin.Action != "actions/checkout" || uses[0].Ref != wantCheckout {
		t.Errorf("checkout action = %q, want %q", uses[0].Ref, wantCheckout)
	}
	if uses[1].Pin.Action != supplychain.ReuseAction || uses[1].Ref != supplychain.ReuseActionPinnedRef() {
		t.Errorf("reuse action = %q, want %q", uses[1].Ref, supplychain.ReuseActionPinnedRef())
	}
}

// Positive: markdownassets.Workflow carries a pinned actions/checkout step, which reuseCheckoutRef
// extracts without duplicating the pin literal (HISS-19).
func TestMarkdownAssetsWorkflowCarriesPinnedCheckout(t *testing.T) {
	ref, err := reuseCheckoutRef()
	if err != nil {
		t.Fatalf("reuseCheckoutRef: %v", err)
	}
	if !strings.HasPrefix(ref, "actions/checkout@") {
		t.Errorf("scanned checkout ref = %q, want actions/checkout@<sha> # <version>", ref)
	}
}

// Negative: a workflow without a pinned checkout reports an error, never falling back to a hardcoded pin.
func TestScanCheckoutPinnedRef_Negative(t *testing.T) {
	_, err := scanCheckoutPinnedRef("name: Empty\njobs:\n  test:\n    steps:\n      - run: true\n")
	if err == nil {
		t.Fatal("scanCheckoutPinnedRef on workflow without checkout: want error, got nil")
	}
}

// Negative (HISS-15): a workflow lacking a pinned checkout fails REUSE workflow rendering with an error.
func TestRenderReuseWorkflow_LacksPinnedCheckout_Negative(t *testing.T) {
	cases := map[string]string{
		"no checkout":       "name: Empty\njobs:\n  test:\n    steps:\n      - run: true\n",
		"unpinned checkout": "name: Unpinned\njobs:\n  test:\n    steps:\n      - uses: actions/checkout@v7\n",
	}
	for name, workflow := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := renderReuseWorkflow(workflow, forge.FallbackDefaultBranch)
			if err == nil {
				t.Fatalf("%s: renderReuseWorkflow want error, got nil", name)
			}
		})
	}
}

// Boundary: an unedited earlier tag-pinned hosted REUSE gate (reuse-action-v6.yml) is refreshed
// to the current digest-pinned rendering on adoption without --force.
func TestAdopt_RefreshesPriorTagPinnedReuseWorkflow(t *testing.T) {
	repoPath := newTestRepo(t, "refresh-prior-reuse-gate")
	mustWrite(t, filepath.Join(repoPath, supplychain.LicensesDir, "MIT.txt"), "MIT License\n")
	priorText := mustRead(t, filepath.Join("testdata", "reuse-workflow", "reuse-action-v6.yml"))
	workflowPath := filepath.Join(repoPath, filepath.FromSlash(reuseWorkflowFile))
	mustWrite(t, workflowPath, priorText)

	rep := adoptWithSource(t, repoPath, newAdoptLockSource(t), false)
	got := mustRead(t, workflowPath)
	want := mustReuseWorkflow(t, forge.FallbackDefaultBranch)
	if got != want {
		t.Fatalf("refreshed gate differs from current rendering:\ngot:\n%s\nwant:\n%s", got, want)
	}
	if hasAction(rep, reuseWorkflowFile, actionReplace) {
		t.Errorf("want a refresh without --force, got actionReplace: %+v", rep.ActionDetails)
	}
	if !strings.Contains(reuseGateDetails(rep), "Refreshed") {
		t.Errorf("report details do not mention Refreshed: %s", reuseGateDetails(rep))
	}
}

// applyRenovateRewrite derives the Renovate-pinned text from a tag-pinned REUSE gate
// by applying the exact rewrite Renovate performed in #1029.
func applyRenovateRewrite(text string) string {
	text = strings.Replace(text, "uses: actions/checkout@v7", "uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7", 1)
	text = strings.Replace(text, "uses: fsfe/reuse-action@v6", "uses: fsfe/reuse-action@676e2d560c9a403aa252096d99fcab3e1132b0f5 # v6", 1)
	return text
}

// Boundary: an adopter whose Renovate pinned the tag-pinned reuse.yml holds an exact rewrite
// (#1029: uses: <action>@<full sha> # <tag>). Adoption refreshes such a copy to the current
// digest-pinned rendering without --force.
func TestAdopt_RefreshesRenovatePinnedReuseWorkflow(t *testing.T) {
	for _, fixture := range []string{"reuse-action-v6.yml", "reuse-action-v6-draft-bash.yml"} {
		t.Run(fixture, func(t *testing.T) {
			repoPath := newTestRepo(t, "refresh-renovate-pinned")
			mustWrite(t, filepath.Join(repoPath, supplychain.LicensesDir, "MIT.txt"), "MIT License\n")
			priorText := mustRead(t, filepath.Join("testdata", "reuse-workflow", fixture))
			renovatePinnedText := applyRenovateRewrite(priorText)
			workflowPath := filepath.Join(repoPath, filepath.FromSlash(reuseWorkflowFile))
			mustWrite(t, workflowPath, renovatePinnedText)

			rep := adoptWithSource(t, repoPath, newAdoptLockSource(t), false)
			got := mustRead(t, workflowPath)
			want := mustReuseWorkflow(t, forge.FallbackDefaultBranch)
			if got != want {
				t.Fatalf("refreshed gate differs from current rendering:\ngot:\n%s\nwant:\n%s", got, want)
			}
			if hasAction(rep, reuseWorkflowFile, actionReplace) {
				t.Errorf("want a refresh without --force, got actionReplace: %+v", rep.ActionDetails)
			}
			if !strings.Contains(reuseGateDetails(rep), "Refreshed") {
				t.Errorf("report details do not mention Refreshed: %s", reuseGateDetails(rep))
			}
		})
	}
}

// Negative (Rule 13): a hand edit beyond the Renovate rewrite is kept and fails the refresh.
func TestAdopt_KeepsHandEditedReuseWorkflow_Negative(t *testing.T) {
	repoPath := newTestRepo(t, "keep-hand-edited-reuse-gate")
	mustWrite(t, filepath.Join(repoPath, supplychain.LicensesDir, "MIT.txt"), "MIT License\n")
	priorText := mustRead(t, filepath.Join("testdata", "reuse-workflow", "reuse-action-v6-draft-bash.yml"))
	handEdited := applyRenovateRewrite(priorText) + "\n        # custom adopter step or comment\n"
	workflowPath := filepath.Join(repoPath, filepath.FromSlash(reuseWorkflowFile))
	mustWrite(t, workflowPath, handEdited)

	rep := adoptWithSource(t, repoPath, newAdoptLockSource(t), false)
	got := mustRead(t, workflowPath)
	if got != handEdited {
		t.Fatalf("hand-edited gate was modified: got:\n%s\nwant:\n%s", got, handEdited)
	}
	if strings.Contains(reuseGateDetails(rep), "Refreshed") {
		t.Errorf("report details incorrectly mention Refreshed: %s", reuseGateDetails(rep))
	}
	want := mustReuseWorkflow(t, forge.FallbackDefaultBranch)
	if got == want {
		t.Fatal("negative test: hand-edited gate unexpectedly matched current rendering")
	}
}

// Boundary (HISS-15): an existing reuse.yml with mixed line endings is preserved unverified
// by both adopt and dry-run without aborting the adoption.
func TestAdopt_MixedLineEndingsReuseWorkflow_PreservedUnverified(t *testing.T) {
	currentText := mustReuseWorkflow(t, forge.FallbackDefaultBranch)
	mixedText := strings.Replace(currentText, "\n", "\r\n", 1)

	// Test dry-run preserves the file and reports preserved unverified
	dryRepo := newTestRepo(t, "mixed-endings-reuse-dry")
	mustWrite(t, filepath.Join(dryRepo, supplychain.LicensesDir, "MIT.txt"), "MIT License\n")
	workflowPathDry := filepath.Join(dryRepo, filepath.FromSlash(reuseWorkflowFile))
	mustWrite(t, workflowPathDry, mixedText)

	dryRep, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: dryRepo, DryRun: true})
	if err != nil {
		t.Fatalf("DryRun with mixed-endings reuse.yml failed: %v", err)
	}
	if got := mustRead(t, workflowPathDry); got != mixedText {
		t.Fatalf("DryRun modified mixed-endings reuse.yml: got:\n%s\nwant:\n%s", got, mixedText)
	}
	if !strings.Contains(reuseGateDetails(dryRep), "Existing file preserved unverified") {
		t.Errorf("DryRun report details do not mention 'Existing file preserved unverified': %s", reuseGateDetails(dryRep))
	}

	// Test adopt preserves the file and reports preserved unverified
	adoptRepo := newTestRepo(t, "mixed-endings-reuse-adopt")
	mustWrite(t, filepath.Join(adoptRepo, supplychain.LicensesDir, "MIT.txt"), "MIT License\n")
	workflowPathAdopt := filepath.Join(adoptRepo, filepath.FromSlash(reuseWorkflowFile))
	mustWrite(t, workflowPathAdopt, mixedText)

	adoptRep, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: adoptRepo})
	if err != nil {
		t.Fatalf("Adopt with mixed-endings reuse.yml failed: %v", err)
	}
	if got := mustRead(t, workflowPathAdopt); got != mixedText {
		t.Fatalf("Adopt modified mixed-endings reuse.yml: got:\n%s\nwant:\n%s", got, mixedText)
	}
	if !strings.Contains(reuseGateDetails(adoptRep), "Existing file preserved unverified") {
		t.Errorf("Adopt report details do not mention 'Existing file preserved unverified': %s", reuseGateDetails(adoptRep))
	}
}
