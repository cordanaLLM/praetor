package adopt

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"gopkg.in/yaml.v3"
)

const priorLefthookFixtures = "testdata/lefthook"

// buildLefthookYAML is the rendering a fixture repository without a language marker gets: no
// language detected keeps every language's jobs (lefthookLanguages), and the fixture lock source
// carries no checkpoint bundle.
func buildLefthookYAML() string {
	return buildLefthookYAMLFor(lefthookJobLanguages, false)
}

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

// lefthookLanguageSets are the language sets lefthook.yml renders distinct jobs for.
var lefthookLanguageSets = []hisscatalog.Language{0, hisscatalog.LanguageGo, hisscatalog.LanguageRust, lefthookJobLanguages}

// Boundary: no current rendering, for any language set, is a prior one (none needs migration),
// and a prior rendering that lost its final newline is no longer exact.
func TestIsPriorLefthookConfig_Boundary_CurrentAndTruncated(t *testing.T) {
	for _, languages := range lefthookLanguageSets {
		for _, checkpoint := range []bool{false, true} {
			current := []byte(buildLefthookYAMLFor(languages, checkpoint))
			if isPriorLefthookConfig(current) {
				t.Errorf("current rendering (languages=%v, checkpoint=%v) listed as prior", languages, checkpoint)
			}
			if classifyLefthookConfig(current, languages) != (lefthookIdentity{}) {
				t.Errorf("current rendering (languages=%v, checkpoint=%v) classified as prior or kept", languages, checkpoint)
			}
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

// foreignLefthook is a configuration Praetor did not write: it neither extends the canonical
// policy nor holds any generated job.
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

// assertLefthookKept fails unless adoption left lefthook.yml holding want, recorded the skip that
// says it keeps the file, --force included, and how to regenerate it, replaced and backed up
// nothing, and activated no hook.
func assertLefthookKept(t *testing.T, repoPath string, rep *AdoptReport, want string) {
	t.Helper()
	if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); got != want {
		t.Fatalf("lefthook.yml was rewritten:\n%s", got)
	}
	note := lefthookDetails(rep)
	if !hasAction(rep, lefthookFile, actionSkip) || !strings.Contains(note, "kept, --force included, and not activated") ||
		!strings.Contains(note, "remove lefthook.yml and re-run adopt to regenerate it") {
		t.Fatalf("the keep is not reported with its remedy: %+v", rep.ActionDetails)
	}
	if hasAction(rep, lefthookFile, actionReplace) || strings.Contains(note, " regenerates it)") {
		t.Fatalf("a kept lefthook.yml is reported as replaceable or replaced: %q", note)
	}
	backups, err := filepath.Glob(filepath.Join(repoPath, filepath.FromSlash(adoptBackupRoot), "*", lefthookFile))
	if err != nil || len(backups) != 0 {
		t.Fatalf("a kept lefthook.yml was backed up: %v (err %v)", backups, err)
	}
	if fileExists(filepath.Join(repoPath, ".git", "hooks", preCommitHook)) {
		t.Fatal("a kept lefthook.yml was activated")
	}
}

// Negative (#502): a configuration that is neither a current nor an earlier Praetor rendering
// is the repository's. A plain run and --force both keep it byte for byte and do not activate
// it, and the reason names the generated jobs it lacks and the job it adds; the audit checks
// only that lefthook.yml exists, so --force has nothing to restore.
func TestAdopt_Negative_ForeignLefthookKeptWithAndWithoutForce(t *testing.T) {
	for _, force := range []bool{false, true} {
		repoPath, rep := adoptLefthookFixture(t, "foreign-lefthook", foreignLefthook, force)
		assertLefthookKept(t, repoPath, rep, foreignLefthook)
		note := lefthookDetails(rep)
		if !strings.Contains(note, "It lacks 12 generated jobs (post-commit/commands/dedupe-cadence, ") ||
			!strings.Contains(note, "and adds 1 (pre-commit/commands/lint).") {
			t.Errorf("force=%v: the reason does not name the missing and extra jobs: %q", force, note)
		}
	}
}

// Boundary: under --force a lefthook.yml that is a symlink, even to a file inside the
// repository, is kept, and its target is not written through.
func TestAdopt_Boundary_ForceKeepsSymlinkedLefthook(t *testing.T) {
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
	assertLefthookKept(t, repoPath, rep, foreignLefthook)
}

// Negative: --force never replaces a configuration that holds every generated job plus more,
// and the reason names the jobs it would have dropped.
func TestAdopt_Negative_ForceKeepsLefthookJobSuperset(t *testing.T) {
	extended := buildLefthookYAML() + "commit-msg:\n  commands:\n    conventional:\n      run: ./scripts/check-msg {1}\n"
	repoPath, rep := adoptLefthookFixture(t, "superset-lefthook", extended, true)
	assertLefthookKept(t, repoPath, rep, extended)
	if !strings.Contains(strings.Join(rep.Warnings, "\n"), "It holds every generated job and adds 1 (commit-msg/commands/conventional).") {
		t.Fatalf("the preserved job is not named: %v", rep.Warnings)
	}
}

// preparationLefthook is an adopter lefthook.yml that extends its own preparation file and
// defines no job of its own: none of the generated jobs, and not the canonical policy.
const preparationLefthook = "min_version: 2.1.14\nextends:\n  - .config/lefthook/preparation.yml\n"

// Positive (#502): in a Cargo workspace, an adopter lefthook.yml that extends its own
// preparation file survives --force byte for byte and is not activated. The reason names the
// generated jobs it lacks, the Cargo repository's own (clippy, rustfmt) among them, and no Go job.
func TestAdopt_Positive_ForceKeepsAdopterExtendsLefthook(t *testing.T) {
	repoPath := newTestRepo(t, "adopter-extends-lefthook")
	mustWrite(t, filepath.Join(repoPath, "Cargo.toml"), "[package]\nname = \"widget\"\nversion = \"0.1.0\"\nedition = \"2021\"\n")
	mustWrite(t, filepath.Join(repoPath, lefthookFile), preparationLefthook)
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Force: true})
	if err != nil {
		t.Fatalf("Adopt --force: %v", err)
	}
	assertNoIssues(t, rep)
	assertLefthookKept(t, repoPath, rep, preparationLefthook)
	note := lefthookDetails(rep)
	for _, job := range []string{"pre-commit/commands/clippy", "pre-commit/commands/rustfmt"} {
		if !strings.Contains(note, job) {
			t.Errorf("the reason does not name the missing %s: %q", job, note)
		}
	}
	if strings.Contains(note, "gofmt") || strings.Contains(note, "govet") || !strings.Contains(note, "and adds none.") {
		t.Errorf("the reason names Go jobs a Cargo repository is not generated, or invents extras: %q", note)
	}
}

// Boundary: a lefthook.yml that does not parse as a YAML mapping is kept and reported, never
// overwritten, --force included; an absent one is created and activated.
func TestAdopt_Boundary_UnparsableLefthookKeptAbsentCreated(t *testing.T) {
	for _, body := range []string{"pre-commit: [unterminated\n", "- just\n- a list\n"} {
		repoPath, rep := adoptLefthookFixture(t, "unparsable-lefthook", body, true)
		assertLefthookKept(t, repoPath, rep, body)
		if !strings.Contains(lefthookDetails(rep), "is not a YAML mapping lefthook can read") {
			t.Errorf("%q: the reason does not say the file does not parse: %q", body, lefthookDetails(rep))
		}
	}
	repoPath := newTestRepo(t, "absent-lefthook")
	rep, err := Adopt(context.Background(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, Force: true})
	if err != nil {
		t.Fatalf("Adopt --force: %v", err)
	}
	if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); got != buildLefthookYAML() || !hasAction(rep, lefthookFile, actionCreate) {
		t.Fatalf("an absent lefthook.yml was not created: %+v\n%s", rep.ActionDetails, got)
	}
	if !fileExists(filepath.Join(repoPath, ".git", "hooks", preCommitHook)) {
		t.Fatal("the created configuration was not activated")
	}
}

// Negative: the current rendering under --force is verified in place, not rewritten or
// replaced, and activated.
func TestAdopt_Negative_ForceVerifiesCurrentLefthook(t *testing.T) {
	repoPath, rep := adoptLefthookFixture(t, "current-force", buildLefthookYAML(), true)
	if got := mustRead(t, filepath.Join(repoPath, lefthookFile)); got != buildLefthookYAML() {
		t.Fatalf("the current rendering was rewritten:\n%s", got)
	}
	if !strings.Contains(lefthookDetails(rep), "Existing Lefthook configuration verified present") || hasAction(rep, lefthookFile, actionReplace) {
		t.Fatalf("want a verify and no replace: %+v", rep.ActionDetails)
	}
	if !fileExists(filepath.Join(repoPath, ".git", "hooks", preCommitHook)) {
		t.Fatal("the current rendering was not activated")
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

// Negative: a copy of the current rendering with mixed line endings is no checkout of it and no
// Praetor text, so --force keeps it like any other configuration; the reason says it holds the
// generated jobs and adds none.
func TestAdopt_Negative_ForceKeepsMixedEndingCurrentLefthook(t *testing.T) {
	mixed := strings.Replace(crlfText(buildLefthookYAML()), "\r\n", "\n", 1)
	repoPath, rep := adoptLefthookFixture(t, "current-mixed-force", mixed, true)
	assertLefthookKept(t, repoPath, rep, mixed)
	if !strings.Contains(lefthookDetails(rep), "It holds every generated job and adds none.") {
		t.Errorf("the reason does not say the jobs match: %q", lefthookDetails(rep))
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

// assertKeptNotCanonical fails unless classifyLefthookConfig keeps body as a configuration that
// does not extend the canonical policy, with a reason holding want.
func assertKeptNotCanonical(t *testing.T, body, want string) {
	t.Helper()
	got := classifyLefthookConfig([]byte(body), lefthookJobLanguages)
	if got.canonical || got.prior || !strings.Contains(got.reason, want) {
		t.Errorf("want kept with %q, got %+v\n%s", want, got, body)
	}
}

// Boundary: extends is recognised as a string, as a list and with a ./ prefix. A different
// extends target, a configuration missing one generated job and one that does not parse are
// kept, each reason saying why; a long job list is counted and truncated.
func TestClassifyLefthookConfig_Boundary_ExtendsAndJobDeltaEdges(t *testing.T) {
	current := buildLefthookYAML()
	for _, body := range []string{
		"extends: .config/lefthook/praetor.yml\n",
		"extends:\n  - other.yml\n  - ./.config/lefthook/praetor.yml\n",
	} {
		if got := classifyLefthookConfig([]byte(body), lefthookJobLanguages); !got.canonical || got.reason == "" {
			t.Errorf("extends not recognised in %q: %+v", body, got)
		}
	}
	assertKeptNotCanonical(t, "extends:\n  - .config/lefthook/other.yml\n", "It lacks 12 generated jobs")
	missingOne := strings.Replace(current, "    gate:\n", "    gate-renamed:\n", 1) + "extra:\n  commands:\n    x:\n      run: 'true'\n"
	assertKeptNotCanonical(t, missingOne, "It lacks 1 generated jobs (pre-push/commands/gate) and adds 2 (extra/commands/x, pre-push/commands/gate-renamed).")
	assertKeptNotCanonical(t, "not: [valid yaml\n", "is not a YAML mapping lefthook can read")
	many := current
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		many += "hook-" + name + ":\n  commands:\n    job:\n      run: 'true'\n"
	}
	assertKeptNotCanonical(t, many, "adds 7 (hook-a/commands/job, hook-b/commands/job, hook-c/commands/job, hook-d/commands/job, hook-e/commands/job and 2 more).")
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

// Positive: a configuration written in lefthook's jobs-list syntax is compared job for job like
// the commands-map form, so only the job it adds is named, and a remotes entry whose configs
// name the canonical policy is recognised like extends.
func TestClassifyLefthookConfig_Positive_JobsListSupersetAndRemotes(t *testing.T) {
	superset := jobsListRendering(t, map[string]any{"name": "lint-docs", "run": "make docs-lint"})
	assertKeptNotCanonical(t, superset, "It holds every generated job and adds 1 (pre-commit/commands/lint-docs).")
	remote := "remotes:\n  - git_url: https://github.com/cordanaLLM/praetor\n    ref: v1.0.0\n    configs:\n      - ./.config/lefthook/praetor.yml\n"
	for _, body := range []string{remote, canonicalRootLefthook} {
		if got := classifyLefthookConfig([]byte(body), lefthookJobLanguages); !got.canonical || !strings.Contains(got.reason, canonicalLefthookPolicy) {
			t.Fatalf("configuration reaching the canonical policy not recognised: %+v\n%s", got, body)
		}
	}
}

// Negative: the jobs-list form of exactly the generated jobs is no Praetor rendering and is kept
// with nothing missing or added, and a remote naming another configuration, or remotes that are
// not a list, are not the canonical policy.
func TestClassifyLefthookConfig_Negative_JobsListEquivalentAndOtherRemotes(t *testing.T) {
	assertKeptNotCanonical(t, jobsListRendering(t), "It holds every generated job and adds none.")
	for _, body := range []string{
		"remotes:\n  - git_url: https://example.invalid/hooks\n    configs:\n      - lefthook.yml\n",
		"remotes:\n  git_url: https://example.invalid/hooks\n  configs: [.config/lefthook/praetor.yml]\n",
	} {
		assertKeptNotCanonical(t, body, "It lacks 12 generated jobs")
	}
}

// Boundary: the checkpoint jobs are optional both ways. A user extension of the rendering
// without them names only the user's job, and the rendering's checkpoint jobs never count as
// added; the jobs compared are the ones for the repository's languages.
func TestClassifyLefthookConfig_Boundary_CheckpointOptionalAndLanguageJobs(t *testing.T) {
	extension := "commit-msg:\n  commands:\n    conventional:\n      run: ./scripts/check-msg {1}\n"
	for _, checkpoint := range []bool{false, true} {
		extended := buildLefthookYAMLFor(lefthookJobLanguages, checkpoint) + extension
		assertKeptNotCanonical(t, extended, "It holds every generated job and adds 1 (commit-msg/commands/conventional).")
	}
	goOnly := buildLefthookYAMLFor(hisscatalog.LanguageGo, false) + extension
	got := classifyLefthookConfig([]byte(goOnly), hisscatalog.LanguageRust)
	if !strings.Contains(got.reason, "It lacks 2 generated jobs (pre-commit/commands/clippy, pre-commit/commands/rustfmt) and adds 4 "+
		"(commit-msg/commands/conventional, pre-commit/commands/gofmt, pre-commit/commands/govet, pre-push/commands/security).") {
		t.Errorf("a Go extension in a Cargo repository names the wrong jobs: %+v", got)
	}
}
