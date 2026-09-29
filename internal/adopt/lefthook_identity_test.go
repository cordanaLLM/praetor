package adopt

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const priorLefthookFixtures = "testdata/lefthook"

func readPriorLefthookFixtures(t *testing.T) map[string][]byte {
	t.Helper()
	return readFixtureDir(t, priorLefthookFixtures)
}

func adoptLefthookFixture(t *testing.T, name, lefthook string, force bool) (string, *AdoptReport) {
	t.Helper()
	repoPath := newTestRepo(t, name)
	mustWrite(t, filepath.Join(repoPath, lefthookFile), lefthook)
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Force: force})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	assertNoIssues(t, rep)
	return repoPath, rep
}

// The digest set is replayable in both directions: every fixture is a recognised digest and
// every digest has a fixture, so the set can neither claim bytes nobody can reproduce nor
// silently stop covering a fixture.
func TestPriorLefthookDigests_Positive_ReproducedByFixtures(t *testing.T) {
	assertPriorDigestsReproduced(t, priorLefthookFixtures, priorLefthookDigests)
}

// Positive: an earlier Praetor rendering is migrated to the current one without --force and
// then activated, instead of being treated as foreign and never fixed (BUG-859).
func TestAdopt_Positive_PriorLefthookMigratedAndActivated(t *testing.T) {
	for name, data := range readPriorLefthookFixtures(t) {
		repoPath, rep := adoptLefthookFixture(t, "prior-"+strings.TrimSuffix(name, ".lefthook.yml"), string(data), false)
		if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); got != buildLefthookYAML() {
			t.Errorf("%s: not migrated to the current rendering:\n%s", name, got)
		}
		if !contains(rep.ReconciledFiles, lefthookFile) {
			t.Errorf("%s: migration not reported: %v", name, rep.ReconciledFiles)
		}
		if !fileExists(filepath.Join(repoPath, ".git", "hooks", preCommitHook)) {
			t.Errorf("%s: migrated configuration was not activated", name)
		}
	}
}

// Positive (HISS-21): a CRLF checkout of an earlier rendering is still Praetor's unedited
// output. It is migrated to the current rendering's exact LF bytes, the only bytes activation
// trusts, and activated.
func TestAdopt_Positive_CRLFPriorLefthookMigratedAndActivated(t *testing.T) {
	prior := crlfText(string(readPriorLefthookFixtures(t)["root-go.lefthook.yml"]))
	repoPath, rep := adoptLefthookFixture(t, "prior-crlf", prior, false)
	if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); got != buildLefthookYAML() {
		t.Errorf("not migrated to the current rendering:\n%q", got)
	}
	if !contains(rep.ReconciledFiles, lefthookFile) {
		t.Errorf("migration not reported: %v", rep.ReconciledFiles)
	}
	if !fileExists(filepath.Join(repoPath, ".git", "hooks", preCommitHook)) {
		t.Error("the migrated configuration was not activated")
	}
}

// Negative: an edited copy of an earlier rendering is not exact, so it is preserved and not
// activated, exactly like any other configuration Praetor did not write.
func TestAdopt_Negative_EditedPriorLefthookPreserved(t *testing.T) {
	edited := string(readPriorLefthookFixtures(t)["root-go.lefthook.yml"]) + "# local edit\n"
	repoPath, rep := adoptLefthookFixture(t, "edited-prior", edited, false)
	if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); got != edited {
		t.Fatal("an edited configuration was rewritten")
	}
	if fileExists(filepath.Join(repoPath, ".git", "hooks", preCommitHook)) {
		t.Fatal("an edited configuration was activated")
	}
	if !hasAction(rep, lefthookFile, actionSkip) {
		t.Fatalf("expected the not-generated skip, got %v", rep.ActionDetails)
	}
}

// Boundary: the current renderings are not prior ones (they need no migration), and a prior
// rendering that lost its final newline is no longer exact.
func TestIsPriorLefthookConfig_Boundary_CurrentAndTruncated(t *testing.T) {
	for _, checkpoint := range []bool{false, true} {
		current := []byte(buildLefthookYAMLFor(checkpoint))
		if isPriorLefthookConfig(current) {
			t.Errorf("current rendering (checkpoint=%v) listed as prior", checkpoint)
		}
		if classifyLefthookConfig(current, buildLefthookYAML()) != (lefthookIdentity{}) {
			t.Errorf("current rendering (checkpoint=%v) classified as prior or protected", checkpoint)
		}
	}
	prior := readPriorLefthookFixtures(t)["hiss16.lefthook.yml"]
	if isPriorLefthookConfig(prior[:len(prior)-1]) {
		t.Error("a truncated prior rendering was recognised")
	}
	if isPriorLefthookConfig([]byte(strings.Replace(crlfText(string(prior)), "\r\n", "\n", 1))) {
		t.Error("a prior rendering with mixed line endings was recognised")
	}
}

const canonicalRootLefthook = "min_version: 2.1.12\nassert_lefthook_installed: true\nextends:\n  - .config/lefthook/praetor.yml\n"

// Negative: --force never replaces a configuration that extends the canonical policy, nor
// the vendored interceptor beside it, and says why (BUG-858).
func TestAdopt_Negative_ForceKeepsCanonicalLefthookAndInterceptor(t *testing.T) {
	repoPath := newTestRepo(t, "canonical-lefthook")
	mustWrite(t, filepath.Join(repoPath, lefthookFile), canonicalRootLefthook)
	vendored := "# vendored canonical interceptor\n"
	mustWrite(t, filepath.Join(repoPath, filepath.FromSlash(evasionHookFile)), vendored)
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Force: true})
	if err != nil {
		t.Fatalf("Adopt --force: %v", err)
	}
	assertNoIssues(t, rep)
	if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); got != canonicalRootLefthook {
		t.Fatalf("--force replaced a configuration extending the canonical policy:\n%s", got)
	}
	if got := mustRead(t, filepath.Join(repoPath, filepath.FromSlash(evasionHookFile))); got != vendored {
		t.Fatal("--force replaced the vendored interceptor with the generated one")
	}
	if !hasAction(rep, lefthookFile, actionSkip) || !strings.Contains(strings.Join(rep.Warnings, "\n"), canonicalLefthookPolicy) {
		t.Fatalf("the protection is not reported: %v", rep.Warnings)
	}
	if fileExists(filepath.Join(repoPath, ".git", "hooks", preCommitHook)) {
		t.Fatal("a configuration adoption did not write was activated")
	}
}

// foreignLefthook is a configuration Praetor did not write that classifyLefthookConfig does not
// protect: it neither extends the canonical policy nor holds every generated job.
const foreignLefthook = "pre-commit:\n  commands:\n    lint:\n      run: make lint\n"

// lefthookDetails joins every action detail the report records for lefthook.yml: the scaffold
// entry and the activation entry that follows it.
func lefthookDetails(rep *AdoptReport) string {
	var details []string
	for _, detail := range rep.ActionDetails {
		if detail.Path == lefthookFile {
			details = append(details, detail.Details)
		}
	}
	return strings.Join(details, "\n")
}

// Positive: lefthook.yml keeps its own --force contract (scaffold.forceable). A plain run keeps
// an unprotected foreign configuration and its note names --force, and --force replaces it with
// a line delta, since audit locks nothing about the file but its presence.
func TestAdopt_Positive_UnprotectedLefthookNoteNamesForceAndForceReplaces(t *testing.T) {
	repoPath, rep := adoptLefthookFixture(t, "foreign-lefthook", foreignLefthook, false)
	if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); got != foreignLefthook {
		t.Fatalf("a plain run rewrote the configuration:\n%s", got)
	}
	note := lefthookDetails(rep)
	if !strings.Contains(note, "(--force regenerates it)") || strings.Contains(note, "--force included") {
		t.Errorf("the drift note does not say --force regenerates the file: %q", note)
	}
	repoPath, rep = adoptLefthookFixture(t, "foreign-lefthook-force", foreignLefthook, true)
	if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); got != buildLefthookYAML() {
		t.Errorf("--force did not regenerate the configuration:\n%s", got)
	}
	if !hasAction(rep, lefthookFile, actionReplace) {
		t.Errorf("want a replace: %+v", rep.ActionDetails)
	}
}

// Boundary: under --force a lefthook.yml that is a symlink, even to a file inside the
// repository, is preserved and reported unverified, and its target is not written through:
// the scaffold reads it only through contextopt.ObserveSnapshot, which accepts a regular file
// alone.
func TestAdopt_Boundary_ForceKeepsSymlinkedLefthookUnverified(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs developer mode on Windows")
	}
	repoPath := newTestRepo(t, "symlinked-lefthook-force")
	target := filepath.Join(repoPath, "real.yml")
	mustWrite(t, target, foreignLefthook)
	if err := os.Symlink("real.yml", filepath.Join(repoPath, lefthookFile)); err != nil {
		t.Fatal(err)
	}
	rep, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Force: true})
	if err != nil {
		t.Fatalf("Adopt --force: %v", err)
	}
	if got := mustRead(t, target); got != foreignLefthook {
		t.Fatalf("--force wrote through the symlink:\n%s", got)
	}
	if info, err := os.Lstat(filepath.Join(repoPath, lefthookFile)); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("lefthook.yml is no longer the symlink: %v", err)
	}
	if hasAction(rep, lefthookFile, actionReplace) || !strings.Contains(lefthookDetails(rep), "preserved unverified") {
		t.Fatalf("want the symlink preserved unverified, no replace: %+v", rep.ActionDetails)
	}
}

// Negative: --force never replaces a configuration that holds every generated job plus more,
// and the reason names the jobs it would have dropped.
func TestAdopt_Negative_ForceKeepsLefthookJobSuperset(t *testing.T) {
	extended := buildLefthookYAML() + "commit-msg:\n  commands:\n    conventional:\n      run: ./scripts/check-msg {1}\n"
	repoPath, rep := adoptLefthookFixture(t, "superset-lefthook", extended, true)
	if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); got != extended {
		t.Fatal("--force dropped the repository's extra jobs")
	}
	if !strings.Contains(strings.Join(rep.Warnings, "\n"), "commit-msg/commands/conventional") {
		t.Fatalf("the preserved job is not named: %v", rep.Warnings)
	}
}

// Positive (HISS-21): under --force a CRLF checkout of the current rendering is rewritten with
// the rendering's own LF bytes, the only bytes activation trusts (lefthookConfigIsPraetor), and
// activated. The text is Praetor's, so it is a reconcile with no backup, never a replace.
func TestAdopt_Positive_ForceRewritesCRLFCurrentLefthookAndActivates(t *testing.T) {
	repoPath, rep := adoptLefthookFixture(t, "current-crlf-force", crlfText(buildLefthookYAML()), true)
	if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); got != buildLefthookYAML() {
		t.Errorf("not rewritten with the rendering's LF bytes:\n%q", got)
	}
	if !hasAction(rep, lefthookFile, actionReconcile) || hasAction(rep, lefthookFile, actionReplace) {
		t.Errorf("want a reconcile and no replace: %+v", rep.ActionDetails)
	}
	if hasAction(rep, lefthookFile, actionSkip) || !fileExists(filepath.Join(repoPath, ".git", "hooks", preCommitHook)) {
		t.Errorf("the rewritten configuration was not activated: %+v", rep.ActionDetails)
	}
}

// Negative: a copy of the current rendering with mixed line endings is no checkout of it, so
// --force replaces it like any drifted file (replaceExisting), and the LF rendering it leaves
// is activated.
func TestAdopt_Negative_ForceReplacesMixedEndingCurrentLefthook(t *testing.T) {
	mixed := strings.Replace(crlfText(buildLefthookYAML()), "\r\n", "\n", 1)
	repoPath, rep := adoptLefthookFixture(t, "current-mixed-force", mixed, true)
	if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); got != buildLefthookYAML() {
		t.Errorf("mixed line endings not replaced:\n%q", got)
	}
	if !hasAction(rep, lefthookFile, actionReplace) {
		t.Errorf("want a replace: %+v", rep.ActionDetails)
	}
	if !fileExists(filepath.Join(repoPath, ".git", "hooks", preCommitHook)) {
		t.Error("the replaced configuration was not activated")
	}
}

// Boundary: without --force a CRLF checkout of the current rendering is verified and kept byte
// for byte, and a --force dry run records the rewrite it plans and writes nothing.
func TestAdopt_Boundary_CRLFCurrentLefthookKeptWithoutForceAndInDryRun(t *testing.T) {
	crlf := crlfText(buildLefthookYAML())
	repoPath, rep := adoptLefthookFixture(t, "current-crlf", crlf, false)
	if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); got != crlf {
		t.Errorf("rewritten without --force:\n%q", got)
	}
	if hasAction(rep, lefthookFile, actionReplace) {
		t.Errorf("a line-ending checkout reported as replaced: %+v", rep.ActionDetails)
	}
	dry := newTestRepo(t, "current-crlf-dry")
	mustWrite(t, filepath.Join(dry, lefthookFile), crlf)
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: dry, Force: true, DryRun: true})
	if err != nil {
		t.Fatalf("Adopt --force --dry-run: %v", err)
	}
	if got := mustRead(t, filepath.Join(dry, lefthookFile)); got != crlf {
		t.Errorf("the dry run wrote:\n%q", got)
	}
	if !hasAction(rep, lefthookFile, actionReconcile) || hasAction(rep, lefthookFile, actionReplace) {
		t.Errorf("want a planned reconcile and no replace: %+v", rep.ActionDetails)
	}
}

// Boundary: extends is recognised as a string, as a list and with a ./ prefix; a different
// extends target and a configuration missing one generated job keep the --force contract.
func TestClassifyLefthookConfig_Boundary_ExtendsAndSupersetEdges(t *testing.T) {
	current := buildLefthookYAML()
	for _, body := range []string{
		"extends: .config/lefthook/praetor.yml\n",
		"extends:\n  - other.yml\n  - ./.config/lefthook/praetor.yml\n",
	} {
		if got := classifyLefthookConfig([]byte(body), current); !got.canonical || got.reason == "" {
			t.Errorf("extends not recognised in %q: %+v", body, got)
		}
	}
	missingOne := strings.Replace(current, "    gate:\n", "    gate-renamed:\n", 1) + "extra:\n  commands:\n    x:\n      run: 'true'\n"
	for _, body := range []string{"extends:\n  - .config/lefthook/other.yml\n", missingOne, "not: [valid yaml\n"} {
		if got := classifyLefthookConfig([]byte(body), current); got != (lefthookIdentity{}) {
			t.Errorf("%q wrongly protected or migrated: %+v", body, got)
		}
	}
	many := current
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		many += "hook-" + name + ":\n  commands:\n    job:\n      run: 'true'\n"
	}
	if reason := classifyLefthookConfig([]byte(many), current).reason; !strings.Contains(reason, "plus 7 more") || !strings.Contains(reason, "hook-e/commands/job and 2 more)") {
		t.Errorf("a long superset is not counted and truncated: %q", reason)
	}
}

// jobsListRendering rewrites the generated rendering's commands maps as lefthook jobs lists, in
// sorted order, and appends extra to the pre-commit list.
func jobsListRendering(t *testing.T, extra ...map[string]any) string {
	t.Helper()
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(buildLefthookYAML()), &parsed); err != nil {
		t.Fatal(err)
	}
	for hook, body := range parsed {
		section, ok := body.(map[string]any)
		commands, hasCommands := section["commands"].(map[string]any)
		if !ok || !hasCommands {
			continue
		}
		names := make([]string, 0, len(commands))
		for name := range commands {
			names = append(names, name)
		}
		sort.Strings(names)
		list := make([]any, 0, len(names)+len(extra))
		for _, name := range names {
			job, isMap := commands[name].(map[string]any)
			if !isMap {
				t.Fatalf("generated job %s/%s is not a map", hook, name)
			}
			job["name"] = name
			list = append(list, job)
		}
		if hook == "pre-commit" {
			for _, job := range extra {
				list = append(list, job)
			}
		}
		delete(section, "commands")
		section["jobs"] = list
	}
	data, err := yaml.Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// Positive: a configuration written in lefthook's jobs-list syntax that holds every generated
// job plus more is protected like the commands-map form, and a remotes entry whose configs
// name the canonical policy is recognised like extends.
func TestClassifyLefthookConfig_Positive_JobsListSupersetAndRemotes(t *testing.T) {
	current := buildLefthookYAML()
	superset := jobsListRendering(t, map[string]any{"name": "lint-docs", "run": "make docs-lint"})
	got := classifyLefthookConfig([]byte(superset), current)
	if got.canonical || !strings.Contains(got.reason, "plus 1 more (pre-commit/commands/lint-docs)") {
		t.Fatalf("jobs-list superset not protected: %+v\n%s", got, superset)
	}
	remote := "remotes:\n  - git_url: https://github.com/cordanaLLM/praetor\n    ref: v1.0.0\n    configs:\n      - ./.config/lefthook/praetor.yml\n"
	if got := classifyLefthookConfig([]byte(remote), current); !got.canonical || !strings.Contains(got.reason, canonicalLefthookPolicy) {
		t.Fatalf("remotes entry naming the canonical policy not recognised: %+v", got)
	}
	if !lefthookExtendsCanonical([]byte(remote)) || !lefthookExtendsCanonical([]byte(canonicalRootLefthook)) {
		t.Fatal("lefthookExtendsCanonical missed extends or remotes")
	}
}

// Negative: the jobs-list form of exactly the generated jobs adds nothing to protect, and a
// remote naming another configuration, or remotes that are not a list, are not the canonical
// policy; both keep the --force contract.
func TestClassifyLefthookConfig_Negative_JobsListEquivalentAndOtherRemotes(t *testing.T) {
	current := buildLefthookYAML()
	for _, body := range []string{
		jobsListRendering(t),
		"remotes:\n  - git_url: https://example.invalid/hooks\n    configs:\n      - lefthook.yml\n",
		"remotes:\n  git_url: https://example.invalid/hooks\n  configs: [.config/lefthook/praetor.yml]\n",
	} {
		if got := classifyLefthookConfig([]byte(body), current); got != (lefthookIdentity{}) {
			t.Errorf("wrongly protected: %+v\n%s", got, body)
		}
	}
	if lefthookExtendsCanonical(nil) || lefthookExtendsCanonical([]byte("not: [valid yaml\n")) {
		t.Error("an absent or unparsable lefthook.yml read as extending the canonical policy")
	}
}

// Boundary: a user extension of the rendering without checkpoint jobs stays protected once the
// checkpoint lifecycle is ready and the current rendering carries those jobs, and only the
// user's job is named. Jobs-list entries are named like lefthook names them: script, name, run
// line, then position; a group is its own kind.
func TestClassifyLefthookConfig_Boundary_CheckpointOptionalAndListNames(t *testing.T) {
	extended := buildLefthookYAML() + "commit-msg:\n  commands:\n    conventional:\n      run: ./scripts/check-msg {1}\n"
	got := classifyLefthookConfig([]byte(extended), buildLefthookYAMLFor(true))
	if !strings.Contains(got.reason, "plus 1 more (commit-msg/commands/conventional)") {
		t.Fatalf("extension lost its protection once checkpoint jobs were generated: %+v", got)
	}
	cases := []struct {
		job  map[string]any
		want string
	}{
		{map[string]any{"name": "fmt", "script": "fmt.sh"}, "scripts/fmt.sh"},
		{map[string]any{"name": "fmt", "run": "gofmt -l ."}, "commands/fmt"},
		{map[string]any{"run": "gofmt -l ."}, "commands/gofmt -l ."},
		{map[string]any{"name": "checks", "group": map[string]any{"jobs": []any{}}}, "jobs/checks"},
		{map[string]any{}, "jobs/[3]"},
		{nil, "jobs/[3]"},
	}
	for _, tc := range cases {
		if got := lefthookListJob(tc.job, 3); got != tc.want {
			t.Errorf("lefthookListJob(%v) = %q, want %q", tc.job, got, tc.want)
		}
	}
}
