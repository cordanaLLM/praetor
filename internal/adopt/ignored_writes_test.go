package adopt

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// ignoreTestRepo is a work tree whose .gitignore holds rules, for the ignored-write checks.
// It skips without git: the check asks git, and a host without it cannot answer (HISS-21).
func ignoreTestRepo(t *testing.T, rules string) string {
	t.Helper()
	requireGit(t)
	root := t.TempDir()
	initTestGit(t, root)
	mustWrite(t, filepath.Join(root, ".gitignore"), rules)
	return root
}

// recordedStep records one step of a report that created paths, as executeAdoptSteps does.
func recordedStep(r *AdoptReport, name string, paths ...string) {
	from := r.mark()
	for _, rel := range paths {
		r.recordCreated(rel, "fixture")
	}
	r.recordStep(name, StepCompleted, from)
}

// TestIgnoredPaths_Positive_NamesTheRuleAndTheNegation: a file under an ignored directory
// names the directory's rule and the directory-only negation of the shallowest ignored parent
// (!.config/ for .config), and a file a rule matches itself names its own negation.
func TestIgnoredPaths_Positive_NamesTheRuleAndTheNegation(t *testing.T) {
	root := ignoreTestRepo(t, "dist/\n*.log\n.config\n")
	rels := []string{"tools/figures/dist/loader.js", "logs/run.log", ".config/labels.yaml", "README.md"}
	got, err := IgnoredPaths(t.Context(), root, rels)
	if err != nil {
		t.Fatal(err)
	}
	want := []IgnoredPath{
		{Path: "tools/figures/dist/loader.js", Rule: ".gitignore:1 (dist/)", Negation: "!/tools/figures/dist/"},
		{Path: "logs/run.log", Rule: ".gitignore:2 (*.log)", Negation: "!/logs/run.log"},
		{Path: ".config/labels.yaml", Rule: ".gitignore:3 (.config)", Negation: "!.config/"},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("ignored = %+v, want %+v", got, want)
	}
}

// TestIgnoredPaths_Negative_TrackedReincludedAndUnmatchedFilesAreCommittable: a tracked file
// is committed whatever its rules say, a negation re-includes a directory, and a file no rule
// matches is committable; none is reported.
func TestIgnoredPaths_Negative_TrackedReincludedAndUnmatchedFilesAreCommittable(t *testing.T) {
	root := ignoreTestRepo(t, "dist/\n!/tools/figures/dist/\n*.log\n")
	mustWrite(t, filepath.Join(root, "kept.log"), "x\n")
	if _, err := util.RunGit(t.Context(), root, "add", "-f", "kept.log"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	got, err := IgnoredPaths(t.Context(), root, []string{"kept.log", "tools/figures/dist/loader.js", "src/main.go"})
	if err != nil || len(got) != 0 {
		t.Fatalf("committable files reported ignored: %+v, %v", got, err)
	}
}

// TestIgnoredPaths_Boundary_EmptyCRLFAndUnanswerable: no files asks git nothing, a CRLF
// .gitignore with a blank line names the real rule (git reads that line as an empty pattern,
// which a directory probe would otherwise match), and a directory outside any work tree is an
// error rather than "nothing ignored".
func TestIgnoredPaths_Boundary_EmptyCRLFAndUnanswerable(t *testing.T) {
	requireGit(t)
	if got, err := IgnoredPaths(t.Context(), t.TempDir(), nil); got != nil || err != nil {
		t.Fatalf("empty query = %+v, %v", got, err)
	}
	root := ignoreTestRepo(t, "dist/\r\n\r\ncoverage/\r\n")
	got, err := IgnoredPaths(t.Context(), root, []string{"tools/figures/dist/loader.js"})
	if err != nil || len(got) != 1 || got[0].Negation != "!/tools/figures/dist/" {
		t.Fatalf("CRLF blank line decided the negation: %+v, %v", got, err)
	}
	if _, err := IgnoredPaths(t.Context(), t.TempDir(), []string{"x"}); err == nil {
		t.Fatal("a directory outside any work tree answered as if git had checked it")
	}
}

// TestReportIgnoredWrites_Positive_FindingsLandOnTheWritingStep: an ignored file is an error
// on the step that wrote it, which then counts as failed, and an ignored editor file is a
// warning on the editors step, so the IDE pillar no longer shows a success mark.
func TestReportIgnoredWrites_Positive_FindingsLandOnTheWritingStep(t *testing.T) {
	root := ignoreTestRepo(t, ".idea/\ndist/\n")
	s := &adoptSession{repoPath: root, report: &AdoptReport{}}
	recordedStep(s.report, editorsStep, ".idea/workspace.xml")
	recordedStep(s.report, "documentation-gate", "tools/figures/dist/loader.js", "docs/index.md")
	reportIgnoredWrites(t.Context(), s)
	editors, docs := s.report.Steps[0], s.report.Steps[1]
	if len(editors.Warnings) != 1 || !strings.HasPrefix(editors.Warnings[0], ".idea/workspace.xml: ignored by .gitignore:1 (.idea/)") ||
		editors.Status != StepCompleted {
		t.Fatalf("editor finding = %+v", editors)
	}
	if len(docs.Errors) != 1 || !strings.Contains(docs.Errors[0], "Add !/tools/figures/dist/ after that rule") || docs.Status != StepFailed {
		t.Fatalf("documentation finding = %+v", docs)
	}
	if len(s.report.Errors) != 1 || len(s.report.Warnings) != 1 {
		t.Fatalf("run findings: errors %q, warnings %q", s.report.Errors, s.report.Warnings)
	}
	for _, pillar := range s.report.Pillars() {
		if pillar.Step == editorsStep && pillar.Status != PillarWarned {
			t.Fatalf("IDE pillar = %s, want warned", pillar.Status)
		}
	}
}

// TestReportIgnoredWrites_Negative_PrivateHookRemovedAndSkippedPathsAreNotChecked: the
// private ledger the managed block ignores by design, an installed Git hook, a path outside
// the work tree, a removed file and a skipped surface are never reported, and a repository
// whose rules leave every written file alone gets no finding.
func TestReportIgnoredWrites_Negative_PrivateHookRemovedAndSkippedPathsAreNotChecked(t *testing.T) {
	root := ignoreTestRepo(t, "*\n")
	s := &adoptSession{repoPath: root, report: &AdoptReport{}}
	from := s.report.mark()
	for _, rel := range []string{".workingdir/STATE.md", ".workingdir2/notes.md", ".git/hooks/pre-commit", "../outside.txt", filepath.Join(root, "abs.txt")} {
		s.report.recordCreated(rel, "fixture")
	}
	s.report.recordReconciledAs("dist/old.js", actionRemove, "fixture")
	s.report.recordNotApplicable("flavor", "fixture")
	s.report.recordStep("working-dir-and-flavor", StepCompleted, from)
	reportIgnoredWrites(t.Context(), s)
	if len(s.report.Errors) != 0 || len(s.report.Warnings) != 0 {
		t.Fatalf("excluded paths reported: errors %q, warnings %q", s.report.Errors, s.report.Warnings)
	}
	clean := ignoreTestRepo(t, "build/\n")
	s = &adoptSession{repoPath: clean, report: &AdoptReport{}}
	recordedStep(s.report, "labels", ".config/labels.yaml", "lefthook.yml")
	reportIgnoredWrites(t.Context(), s)
	if len(s.report.Errors) != 0 || len(s.report.Warnings) != 0 {
		t.Fatalf("unrelated rules reported: errors %q, warnings %q", s.report.Errors, s.report.Warnings)
	}
}

// TestReportIgnoredWrites_Boundary_PlannedNegationAndUnownedFindings: a dry run that planned
// !.config/ leaves out the files only .config hides, and still reports a file another rule
// hides; a finding about a path no step recorded lands on the run alone.
func TestReportIgnoredWrites_Boundary_PlannedNegationAndUnownedFindings(t *testing.T) {
	root := ignoreTestRepo(t, ".config\ndist/\n")
	s := &adoptSession{repoPath: root, report: &AdoptReport{DryRun: true}, configNegationPlanned: true}
	recordedStep(s.report, "labels", ".config/labels.yaml", "tools/figures/dist/loader.js")
	reportIgnoredWrites(t.Context(), s)
	if len(s.report.Errors) != 1 || !strings.HasPrefix(s.report.Errors[0], "tools/figures/dist/loader.js: ") {
		t.Fatalf("planned negation findings = %q", s.report.Errors)
	}
	unowned := &adoptSession{repoPath: root, report: &AdoptReport{}}
	unowned.report.recordCreated(".config/labels.yaml", "fixture")
	reportIgnoredWrites(t.Context(), unowned)
	if len(unowned.report.Errors) != 1 || len(unowned.report.Steps) != 0 {
		t.Fatalf("unowned finding = %q on %+v", unowned.report.Errors, unowned.report.Steps)
	}
}

// TestKconfigConfigRule_ClassifiesTheRuleThatHidesConfig: a rule that also ignores a file
// named .config (bare, anchored, dotfile glob) is Kconfig-style; a directory-only rule the
// repository chose, a directory already re-included, an unrelated rule and an unanswerable git
// are not.
func TestKconfigConfigRule_ClassifiesTheRuleThatHidesConfig(t *testing.T) {
	for _, tc := range []struct {
		rules, rule string
		kconfig     bool
	}{
		{".config\n", ".gitignore:1 (.config)", true},
		{"/.config\n", ".gitignore:1 (/.config)", true},
		{".*\n!.gitignore\n", ".gitignore:1 (.*)", true},
		{".config/\n", "", false},
		{".config\n.config.old\n!.config/\n", "", false},
		{"dist/\n", "", false},
	} {
		root := ignoreTestRepo(t, tc.rules)
		rule, kconfig := kconfigConfigRule(t.Context(), root)
		if kconfig != tc.kconfig || (kconfig && rule.Rule() != tc.rule) {
			t.Fatalf("%q: kconfig=%v rule=%q, want %v %q", tc.rules, kconfig, rule.Rule(), tc.kconfig, tc.rule)
		}
	}
	if _, kconfig := kconfigConfigRule(t.Context(), t.TempDir()); kconfig {
		t.Fatal("a directory outside any work tree was classified")
	}
}

// TestMergeGitIgnoreRules_ConfigNegationIsAddedKeptAndAudited: the negation joins the block on
// request, a later merge that does not ask keeps it, a merge that never asked has none, and
// audit accepts exactly the two canonical blocks.
func TestMergeGitIgnoreRules_ConfigNegationIsAddedKeptAndAudited(t *testing.T) {
	negated, err := mergeGitIgnoreRules(".config\n", true)
	if err != nil || !strings.Contains(negated, "\n"+configDirNegation+"\n"+gitIgnoreManagedEnd) || !HasManagedGitIgnoreTail(negated) {
		t.Fatalf("negated merge = %q, %v", negated, err)
	}
	kept, err := mergeGitIgnore(negated)
	if err != nil || kept != negated {
		t.Fatalf("a merge that did not probe dropped the negation: %q, %v", kept, err)
	}
	plain, err := mergeGitIgnore("dist/\n" + configDirNegation + "\n")
	if err != nil || !strings.HasSuffix(plain, ManagedGitIgnoreBlock()) || !strings.HasPrefix(plain, "dist/\n"+configDirNegation+"\n") {
		t.Fatalf("an operator's own negation moved into the block: %q, %v", plain, err)
	}
	for _, text := range []string{"dist/\n", strings.Replace(negated, configDirNegation, "!docs/", 1)} {
		if HasManagedGitIgnoreTail(text) {
			t.Fatalf("audit accepted a non-canonical tail: %q", text)
		}
	}
}

// TestPreflightConfigRoot_NamesTheKconfigCollision: a regular .config file at the root stops
// adoption with the collision named; an absent entry, a directory and any other kind of entry
// are left to the writers.
func TestPreflightConfigRoot_NamesTheKconfigCollision(t *testing.T) {
	root := t.TempDir()
	if err := preflightConfigRoot(root); err != nil {
		t.Fatalf("absent .config refused: %v", err)
	}
	mustWrite(t, filepath.Join(root, configDir), "CONFIG_X=y\n")
	err := preflightConfigRoot(root)
	if err == nil || !strings.Contains(err.Error(), ".config at the repository root is a file") ||
		!strings.Contains(err.Error(), "one name cannot be both") {
		t.Fatalf("root .config file = %v", err)
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, configDir), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := preflightConfigRoot(dir); err != nil {
		t.Fatalf(".config directory refused: %v", err)
	}
	link := t.TempDir()
	if err := os.Symlink(filepath.Join(dir, configDir), filepath.Join(link, configDir)); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	if err := preflightConfigRoot(link); err != nil {
		t.Fatalf("symlinked .config refused by the preflight instead of the writers: %v", err)
	}
}

// configFiles lists every file below root's .config directory, slash-separated and relative.
func configFiles(t *testing.T, root string) []string {
	t.Helper()
	files := make([]string, 0)
	err := filepath.WalkDir(filepath.Join(root, configDir), func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		files = append(files, filepath.ToSlash(rel))
		return relErr
	})
	if err != nil || len(files) == 0 {
		t.Fatalf("walk %s: %v (%d files)", configDir, err, len(files))
	}
	return files
}

// configErrors returns the report errors about a file below .config.
func configErrors(report *AdoptReport) []string {
	found := make([]string, 0)
	for _, text := range report.Errors {
		if strings.HasPrefix(text, configDir+"/") {
			found = append(found, text)
		}
	}
	return found
}

// TestAdopt_Positive_KconfigRuleGetsTheDirectoryNegation is the kernel-tree shape end to end:
// under a bare .config rule the git-ignore step re-includes .config/ and says so, every file
// adoption wrote there is committable, a re-run keeps the rule, and a Kconfig .config file
// stays ignored at the root and at any depth.
func TestAdopt_Positive_KconfigRuleGetsTheDirectoryNegation(t *testing.T) {
	repo := newTestRepo(t, "kconfig-negation")
	mustWrite(t, filepath.Join(repo, ".gitignore"), ".config\n")
	report, err := Adopt(t.Context(), goldenAdoptOptions(t, repo))
	if err != nil || len(configErrors(report)) != 0 {
		t.Fatalf("adopt: %v, .config errors %q", err, configErrors(report))
	}
	if detail := findActionDetail(report.ActionDetails, gitIgnoreFile); !strings.Contains(detail, "with !.config/, because .gitignore:1 (.config) ignores it") {
		t.Fatalf(".gitignore detail does not name the negation: %q", detail)
	}
	ignored, err := IgnoredPaths(t.Context(), repo, configFiles(t, repo))
	if err != nil || len(ignored) != 0 {
		t.Fatalf("files under .config stay ignored: %+v, %v", ignored, err)
	}
	written := mustRead(t, filepath.Join(repo, ".gitignore"))
	if _, err := Adopt(t.Context(), goldenAdoptOptions(t, repo)); err != nil || mustRead(t, filepath.Join(repo, ".gitignore")) != written {
		t.Fatalf("a re-run changed .gitignore: %v", err)
	}
	kernel := t.TempDir()
	initTestGit(t, kernel)
	mustWrite(t, filepath.Join(kernel, ".gitignore"), written)
	probes := []string{".config", "build/linux/.config", "output/.config"}
	if got, err := util.GitIgnoredPaths(t.Context(), kernel, probes, true); err != nil || !slices.Equal(got, probes) {
		t.Fatalf("a Kconfig .config file was un-ignored: %q, %v", got, err)
	}
}

// TestAdopt_Negative_DeclinedGitIgnoreProposesTheNegation: with git-ignore declined adoption
// leaves .gitignore alone, so every file under .config is an error naming the rule and
// proposing !.config/; a repository without such a rule gets no ignore finding at all.
func TestAdopt_Negative_DeclinedGitIgnoreProposesTheNegation(t *testing.T) {
	repo := newTestRepo(t, "kconfig-declined")
	mustWrite(t, filepath.Join(repo, ".gitignore"), ".config\n")
	mustWrite(t, filepath.Join(repo, manifestFile), "version: 1\nadoption:\n  decline: [git-ignore]\n")
	report, err := Adopt(t.Context(), goldenAdoptOptions(t, repo))
	if err != nil {
		t.Fatal(err)
	}
	found := configErrors(report)
	if len(found) != len(configFiles(t, repo)) || mustRead(t, filepath.Join(repo, ".gitignore")) != ".config\n" {
		t.Fatalf("want one error per file under .config and an untouched .gitignore, got %q", found)
	}
	for _, text := range found {
		if !strings.Contains(text, "ignored by .gitignore:1 (.config)") || !strings.Contains(text, "Add !.config/ after that rule") {
			t.Fatalf("finding does not name the rule and the negation: %q", text)
		}
	}
	clean := newTestRepo(t, "no-ignore-rule")
	report, err = Adopt(t.Context(), goldenAdoptOptions(t, clean))
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range append(report.Errors, report.Warnings...) {
		if strings.Contains(text, "ignored by ") || strings.Contains(text, "not checked against .gitignore") {
			t.Fatalf("a repository without an ignore rule got a finding: %q", text)
		}
	}
}

// TestAdopt_Boundary_DryRunAndRootConfigFile: a dry run plans the negation without writing it
// and reports no .config file it would re-include; a .config file at the root stops adoption
// before any write with the collision named.
func TestAdopt_Boundary_DryRunAndRootConfigFile(t *testing.T) {
	repo := newTestRepo(t, "kconfig-dry-run")
	mustWrite(t, filepath.Join(repo, ".gitignore"), ".config\n")
	opts := goldenAdoptOptions(t, repo)
	opts.DryRun = true
	report, err := Adopt(t.Context(), opts)
	if err != nil || len(configErrors(report)) != 0 || mustRead(t, filepath.Join(repo, ".gitignore")) != ".config\n" {
		t.Fatalf("dry run: %v, .config errors %q", err, configErrors(report))
	}
	if detail := findActionDetail(report.ActionDetails, gitIgnoreFile); !strings.Contains(detail, "!.config/") {
		t.Fatalf("dry run does not plan the negation: %q", detail)
	}
	collision := newTestRepo(t, "kconfig-root-file")
	mustWrite(t, filepath.Join(collision, configDir), "CONFIG_X=y\n")
	if _, err := Adopt(t.Context(), goldenAdoptOptions(t, collision)); err == nil || !strings.Contains(err.Error(), "one name cannot be both") {
		t.Fatalf("root .config file = %v", err)
	}
	if _, err := os.Stat(filepath.Join(collision, manifestFile)); !os.IsNotExist(err) {
		t.Fatalf("adoption wrote before refusing the collision: %v", err)
	}
}
