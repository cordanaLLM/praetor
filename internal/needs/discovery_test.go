package needs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Fixture helpers. A checkout is a directory whose .git holds a HEAD; a linked worktree's
// gitlink names <main>/.git/worktrees/<name>, whose commondir points back at <main>/.git;
// a submodule's gitlink names <super>/.git/modules/<name>, which has no commondir.

func makeCheckout(t *testing.T, dir string) {
	t.Helper()
	writeRepoFile(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/main\n")
}

func makeLinkedWorktree(t *testing.T, mainDir, worktree, name string) {
	t.Helper()
	gitDir := filepath.Join(mainDir, ".git", "worktrees", name)
	writeRepoFile(t, filepath.Join(gitDir, "HEAD"), "ref: refs/heads/"+name+"\n")
	writeRepoFile(t, filepath.Join(gitDir, "commondir"), "../..\n")
	writeRepoFile(t, filepath.Join(worktree, ".git"), "gitdir: "+gitDir+"\n")
}

func makeSubmodule(t *testing.T, superDir, rel string) string {
	t.Helper()
	dir := filepath.Join(superDir, rel)
	moduleDir := filepath.Join(superDir, ".git", "modules", filepath.Base(rel))
	writeRepoFile(t, filepath.Join(moduleDir, "HEAD"), "ref: refs/heads/main\n")
	target, err := filepath.Rel(dir, moduleDir)
	if err != nil {
		t.Fatal(err)
	}
	writeRepoFile(t, filepath.Join(dir, ".git"), "gitdir: "+filepath.ToSlash(target)+"\n")
	return dir
}

// discoveredRoots returns the repository roots discoverFleet finds under root.
func discoveredRoots(t *testing.T, root string) []string {
	t.Helper()
	layout, err := discoverFleet(context.Background(), root)
	if err != nil {
		t.Fatalf("discoverFleet(%s) error = %v", root, err)
	}
	roots := make([]string, 0, len(layout.repos))
	for _, repo := range layout.repos {
		roots = append(roots, repo.root)
	}
	return roots
}

// aggregateRows runs AggregateFleet and indexes the leaderboard by row path.
func aggregateRows(t *testing.T, root string) (*FleetDemandReport, map[string]RepoNeeds) {
	t.Helper()
	report, err := AggregateFleet(context.Background(), root, "")
	if err != nil {
		t.Fatalf("AggregateFleet(%s) error = %v", root, err)
	}
	rows := make(map[string]RepoNeeds, len(report.Leaderboard))
	for _, row := range report.Leaderboard {
		rows[row.Path] = row
	}
	return report, rows
}

func hasDependency(row RepoNeeds, pkg string) bool {
	for _, dep := range row.Dependencies {
		if dep.Package == pkg {
			return true
		}
	}
	return false
}

const (
	pgxModule   = "github.com/jackc/pgx/v5"
	redisModule = "github.com/redis/go-redis/v9"
)

func writeGoProject(t *testing.T, dir, module, importPath string) {
	t.Helper()
	writeRepoFile(t, filepath.Join(dir, "go.mod"), "module "+module+"\ngo 1.24\n")
	writeRepoFile(t, filepath.Join(dir, "main.go"), "package main\n\nimport _ \""+importPath+"\"\n")
}

// TestDiscoverFleetNestedCheckoutsAreOwnRows pins review case c3: a submodule (gitlink) and
// an independent clone nested in a checkout are repositories of their own, each with its
// own demand. The outer checkout's Go import scan does not reach into them.
func TestDiscoverFleetNestedCheckoutsAreOwnRows(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "app")
	makeCheckout(t, app)
	writeGoProject(t, app, "example.com/app", pgxModule)
	sub := makeSubmodule(t, app, filepath.Join("libs", "sub"))
	writeGoProject(t, sub, "example.com/sub", redisModule)
	clone := filepath.Join(app, "deps", "clone")
	makeCheckout(t, clone)
	writeRepoFile(t, filepath.Join(clone, "package.json"), `{"name":"clone","dependencies":{"express":"4"}}`)

	report, rows := aggregateRows(t, root)
	if report.TotalRepositories != 3 || len(rows) != 3 {
		t.Fatalf("total=%d rows=%v, want the checkout, the submodule and the clone as 3 rows", report.TotalRepositories, rows)
	}
	if hasDependency(rows[app], redisModule) || hasDependency(rows[app], "express") {
		t.Errorf("the outer checkout's row carries a nested repository's demand: %+v", rows[app].Dependencies)
	}
	if !hasDependency(rows[app], pgxModule) || !hasDependency(rows[sub], redisModule) || !hasDependency(rows[clone], "express") {
		t.Errorf("a repository lost its own demand: app=%v sub=%v clone=%v",
			rows[app].Dependencies, rows[sub].Dependencies, rows[clone].Dependencies)
	}
}

// TestDiscoverFleetDeclarationsNeverSwallowCheckouts pins review cases c6c, c6d and c6e: a
// manifest directory or a declaration directory outside every checkout, and a fleet root
// that is itself a checkout, keep the checkouts below them as rows of their own.
func TestDiscoverFleetDeclarationsNeverSwallowCheckouts(t *testing.T) {
	cases := map[string]func(t *testing.T, root string) (outer, inner string){
		"c6c non-checkout manifest root": func(t *testing.T, root string) (string, string) {
			mono := filepath.Join(root, "mono")
			writeRepoFile(t, filepath.Join(mono, "package.json"), `{"name":"mono","dependencies":{"express":"4"}}`)
			return mono, filepath.Join(mono, "svc")
		},
		"c6d non-checkout declaration root": func(t *testing.T, root string) (string, string) {
			org := filepath.Join(root, "org")
			writeRepoFile(t, filepath.Join(org, ".standards.yaml"), "repository:\n  name: org\n")
			return org, filepath.Join(org, "svc")
		},
		"c6e fleet root is a checkout": func(t *testing.T, root string) (string, string) {
			makeCheckout(t, root)
			writeRepoFile(t, filepath.Join(root, "package.json"), `{"name":"root","dependencies":{"express":"4"}}`)
			return root, filepath.Join(root, "svc")
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			outer, inner := build(t, root)
			makeCheckout(t, inner)
			writeGoProject(t, inner, "example.com/svc", pgxModule)

			roots := discoveredRoots(t, root)
			if !slices.Contains(roots, outer) || !slices.Contains(roots, inner) {
				t.Fatalf("discovered %v, want both %s and %s", roots, outer, inner)
			}
			_, rows := aggregateRows(t, root)
			if !hasDependency(rows[inner], pgxModule) || hasDependency(rows[outer], pgxModule) {
				t.Errorf("the checkout's demand is misattributed: outer=%v inner=%v", rows[outer].Dependencies, rows[inner].Dependencies)
			}
		})
	}
}

// TestDiscoverFleetFoldsSubprojectsIntoOneRow pins that analyzer-detectable projects inside
// a repository are sub-projects merged into the repository's single row, including one
// under a first-party directory hiss.ShouldIgnoreDir would drop (review m2), while exact
// build-output and vendored names stay pruned.
func TestDiscoverFleetFoldsSubprojectsIntoOneRow(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	makeCheckout(t, repo)
	writeGoProject(t, repo, "example.com/repo", pgxModule)
	writeRepoFile(t, filepath.Join(repo, "core", "meson.build"), "dep = dependency('zlib')\n")
	writeRepoFile(t, filepath.Join(repo, "Build-tools", "meson.build"), "dep = dependency('libpng')\n")
	writeRepoFile(t, filepath.Join(repo, "build_scripts", "requirements.txt"), "click==8.1\n")
	writeRepoFile(t, filepath.Join(repo, "build", "meson.build"), "dep = dependency('libjpeg')\n")
	writeRepoFile(t, filepath.Join(repo, "third_party", "x", "package.json"), `{"name":"x","dependencies":{"lodash":"4"}}`)

	report, rows := aggregateRows(t, root)
	if report.TotalRepositories != 1 {
		t.Fatalf("total = %d, want one repository for one checkout", report.TotalRepositories)
	}
	row := rows[repo]
	want := []string{"Build-tools", "build_scripts", "core"}
	if !slices.Equal(row.Subprojects, want) {
		t.Errorf("sub-projects = %v, want %v", row.Subprojects, want)
	}
	for _, pkg := range []string{pgxModule, "zlib", "libpng", "click"} {
		if !hasDependency(row, pkg) {
			t.Errorf("row lacks %s from a sub-project: %v", pkg, row.Dependencies)
		}
	}
	if hasDependency(row, "libjpeg") || hasDependency(row, "lodash") {
		t.Errorf("row carries build-output or vendored demand: %v", row.Dependencies)
	}
}

// TestDiscoverFleetReportsSubprojectsBeyondDepthBound pins review m1: a sub-project deeper
// than maxSubprojectDepth is listed, never silently dropped; one at the bound is scanned.
func TestDiscoverFleetReportsSubprojectsBeyondDepthBound(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	makeCheckout(t, repo)
	writeGoProject(t, repo, "example.com/repo", pgxModule)
	atBound := filepath.Join("a", "b", "c", "d", "e")
	deep := filepath.Join("a", "b", "c", "d", "e", "f")
	writeRepoFile(t, filepath.Join(repo, atBound, "package.json"), `{"name":"shallow","dependencies":{"express":"4"}}`)
	writeRepoFile(t, filepath.Join(repo, deep, "package.json"), `{"name":"deep","dependencies":{"lodash":"4"}}`)

	report, rows := aggregateRows(t, root)
	row := rows[repo]
	if !slices.Equal(row.UnscannedSubprojects, []string{"a/b/c/d/e/f"}) {
		t.Errorf("row unscanned = %v, want the depth-6 project", row.UnscannedSubprojects)
	}
	if !slices.Equal(report.UnscannedSubprojects, []string{filepath.Join(repo, deep)}) {
		t.Errorf("report unscanned = %v", report.UnscannedSubprojects)
	}
	if !hasDependency(row, "express") || hasDependency(row, "lodash") {
		t.Errorf("depth-5 project must be scanned and depth-6 not: %v", row.Dependencies)
	}
	md := RenderFrameworkDemandMarkdown(report)
	if !strings.Contains(md, "Sub-projects Not Scanned") || !strings.Contains(md, "repo/a/b/c/d/e/f") {
		t.Errorf("markdown does not list the unscanned sub-project:\n%s", md)
	}
	if text := FormatLibraryRelationships(&row); !strings.Contains(text, "NOT scanned") {
		t.Errorf("scan output does not report the unscanned sub-project:\n%s", text)
	}
}

// TestDiscoverFleetDeepOnlyRepositoryIsSkippedWithReason: a checkout whose only manifest
// is beyond the depth bound is skipped, and the error names the unscanned manifest.
func TestDiscoverFleetDeepOnlyRepositoryIsSkippedWithReason(t *testing.T) {
	repo := t.TempDir()
	makeCheckout(t, repo)
	writeRepoFile(t, filepath.Join(repo, "a", "b", "c", "d", "e", "f", "go.mod"), "module example.com/deep\n")

	_, err := ScanRepo(context.Background(), repo)
	if !errors.Is(err, ErrNoAnalyzer) || !strings.Contains(err.Error(), "a/b/c/d/e/f") {
		t.Fatalf("ScanRepo error = %v, want ErrNoAnalyzer naming the deep manifest", err)
	}
}

// TestDiscoverFleetCollapsesLinkedWorktrees pins the operator rule that linked worktrees
// of one repository are one row, listed as duplicates of the main checkout, while
// independent clones of the same project stay distinct rows even under one name.
func TestDiscoverFleetCollapsesLinkedWorktrees(t *testing.T) {
	root := t.TempDir()
	mainDir := filepath.Join(root, "proj")
	makeCheckout(t, mainDir)
	writeRepoFile(t, filepath.Join(mainDir, "meson.build"), "dep = dependency('zlib')\n")
	wtA := filepath.Join(mainDir, "wt", "a")
	wtB := filepath.Join(root, "worktrees", "b")
	for name, dir := range map[string]string{"a": wtA, "b": wtB} {
		makeLinkedWorktree(t, mainDir, dir, name)
		writeRepoFile(t, filepath.Join(dir, "meson.build"), "dep = dependency('zlib')\n")
		clone := filepath.Join(dir, "subprojects", "checkasm")
		makeCheckout(t, clone)
		writeRepoFile(t, filepath.Join(clone, "meson.build"), "dep = dependency('libpng')\n")
	}

	report, rows := aggregateRows(t, root)
	if report.TotalRepositories != 3 || len(rows) != 3 {
		t.Fatalf("total=%d rows=%d, want proj plus two checkasm clones", report.TotalRepositories, len(rows))
	}
	if len(report.DuplicateCheckouts) != 2 {
		t.Fatalf("duplicates = %+v, want both linked worktrees", report.DuplicateCheckouts)
	}
	for _, dup := range report.DuplicateCheckouts {
		if dup.Of != mainDir {
			t.Errorf("worktree %s collapsed onto %s, want the main checkout %s", dup.Dir, dup.Of, mainDir)
		}
	}
	cloneA := rows[filepath.Join(wtA, "subprojects", "checkasm")]
	if len(cloneA.Dependencies) != 1 {
		t.Fatalf("clone row dependencies = %v", cloneA.Dependencies)
	}
	if consumers := report.CapabilityConsumers[cloneA.Dependencies[0].Capability]; len(consumers) != 2 {
		t.Errorf("two same-named clones are %v, want two distinct consumers", consumers)
	}
	if md := RenderFrameworkDemandMarkdown(report); !strings.Contains(md, "`worktrees/b` is a worktree of `proj`") {
		t.Errorf("markdown does not list the collapsed worktree:\n%s", md)
	}
}

// TestDiscoverFleetWorktreesWithoutMainKeepFirst: linked worktrees whose main checkout is
// outside the walk collapse onto the first one found.
func TestDiscoverFleetWorktreesWithoutMainKeepFirst(t *testing.T) {
	outside := t.TempDir()
	makeCheckout(t, outside)
	root := t.TempDir()
	first := filepath.Join(root, "a")
	second := filepath.Join(root, "b")
	makeLinkedWorktree(t, outside, first, "a")
	makeLinkedWorktree(t, outside, second, "b")

	layout, err := discoverFleet(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(layout.repos) != 1 || layout.repos[0].root != first {
		t.Fatalf("repos = %+v, want only %s", layout.repos, first)
	}
	if len(layout.duplicates) != 1 || layout.duplicates[0] != (FleetDuplicate{Dir: second, Of: first}) {
		t.Errorf("duplicates = %+v", layout.duplicates)
	}
}

// TestDiscoverFleetDoesNotFollowSymlinks: a symlink to a checkout outside the fleet root is
// neither walked nor a repository.
func TestDiscoverFleetDoesNotFollowSymlinks(t *testing.T) {
	outside := t.TempDir()
	makeCheckout(t, outside)
	writeGoProject(t, outside, "example.com/outside", pgxModule)
	root := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable on this platform or account: %v", err)
	}
	if roots := discoveredRoots(t, root); len(roots) != 0 {
		t.Fatalf("discovered %v through a symlink", roots)
	}
}

// TestDiscoverFleetBounds: a tree deeper than maxDiscoveryDepth fails the walk instead of
// being truncated; cancellation stops it.
func TestDiscoverFleetBounds(t *testing.T) {
	root := t.TempDir()
	parts := make([]string, 0, maxDiscoveryDepth+2)
	for i := 0; i < maxDiscoveryDepth+2; i++ {
		parts = append(parts, "d")
	}
	if err := os.MkdirAll(filepath.Join(append([]string{root}, parts...)...), 0o750); err != nil {
		t.Skipf("cannot build a %d-level tree here: %v", len(parts), err)
	}
	if _, err := discoverFleet(context.Background(), root); !errors.Is(err, ErrDiscoveryBound) {
		t.Fatalf("error = %v, want ErrDiscoveryBound", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := discoverFleet(ctx, t.TempDir()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled discovery error = %v", err)
	}
}

// TestScanRepoFoldsSubprojectsUnderNonProjectRoot: the single-repository scan names a row
// whose root holds no manifest after the root, carries the root's declarations, and keeps
// a nested checkout out.
func TestScanRepoFoldsSubprojectsUnderNonProjectRoot(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "vmafx")
	makeCheckout(t, repo)
	writeRepoFile(t, filepath.Join(repo, ".needs.yaml"), "capabilities:\n  required: [observability.tracing]\n")
	writeRepoFile(t, filepath.Join(repo, "core", "meson.build"), "dep = dependency('zlib')\n")
	nested := filepath.Join(repo, "ext")
	makeCheckout(t, nested)
	writeRepoFile(t, filepath.Join(nested, "meson.build"), "dep = dependency('libpng')\n")

	row, err := ScanRepo(context.Background(), repo)
	if err != nil {
		t.Fatalf("ScanRepo() error = %v", err)
	}
	if row.Repository != "vmafx" || !hasDependency(*row, "zlib") || hasDependency(*row, "libpng") {
		t.Errorf("row = %s %v, want vmafx with core's demand and without the nested checkout's", row.Repository, row.Dependencies)
	}
	if !slices.Contains(row.Capabilities.Required, CapabilityKey("observability.tracing")) {
		t.Errorf("root declarations dropped: %v", row.Capabilities.Required)
	}
}

// TestScanASTImportsStopsAtNestedModules: a nested Go module or checkout is not the root
// module's source.
func TestScanASTImportsStopsAtNestedModules(t *testing.T) {
	repo := t.TempDir()
	writeGoProject(t, repo, "example.com/repo", pgxModule)
	writeGoProject(t, filepath.Join(repo, "tools"), "example.com/repo/tools", redisModule)
	clone := filepath.Join(repo, "clone")
	makeCheckout(t, clone)
	writeRepoFile(t, filepath.Join(clone, "x.go"), "package x\n\nimport _ \"github.com/spf13/cobra\"\n")

	imports, err := scanASTImports(context.Background(), repo, "example.com/repo")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := imports[pgxModule]; !ok || len(imports) != 1 {
		t.Errorf("imports = %v, want only the root module's %s", imports, pgxModule)
	}
}

// TestDemandIdentityNormalisesPyPINames pins review m4 (PEP 503) across two sub-projects
// and inside one project, and that Go module paths stay case-sensitive.
func TestDemandIdentityNormalisesPyPINames(t *testing.T) {
	repo := t.TempDir()
	writeRepoFile(t, filepath.Join(repo, "pyproject.toml"), "[project]\ndependencies = [\"typing-extensions>=4\"]\n")
	writeRepoFile(t, filepath.Join(repo, "requirements.txt"), "Typing.Extensions==4.12\n")
	writeRepoFile(t, filepath.Join(repo, "py", "requirements.txt"), "typing_extensions==4.12\n")

	row, err := ScanRepo(context.Background(), repo)
	if err != nil {
		t.Fatalf("ScanRepo() error = %v", err)
	}
	count := 0
	for _, dep := range row.Dependencies {
		if normalizePyPIName(dep.Package) == "typing-extensions" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("typing-extensions demanded %d times: %v", count, row.Dependencies)
	}

	goUpper := DependencyDemand{Ecosystem: "go", Package: "github.com/Sirupsen/logrus"}
	goLower := DependencyDemand{Ecosystem: "go", Package: "github.com/sirupsen/logrus"}
	if demandIdentity(goUpper) == demandIdentity(goLower) {
		t.Error("Go module paths are case-sensitive and must not merge")
	}
}

func TestNormalizePyPIName(t *testing.T) {
	cases := map[string]string{
		"":                   "",
		"a":                  "a",
		"Typing_Extensions":  "typing-extensions",
		"zope.interface":     "zope-interface",
		"a-_.b":              "a-b",
		"-lead":              "-lead",
		"trail__":            "trail-",
		"already-normalized": "already-normalized",
	}
	for in, want := range cases {
		if got := normalizePyPIName(in); got != want {
			t.Errorf("normalizePyPIName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestPrunedFromDiscovery pins the exact-name prune rules and the scratch/cache scoping.
func TestPrunedFromDiscovery(t *testing.T) {
	cases := []struct {
		name         string
		parentDepth  int
		parentIsRepo bool
		want         bool
	}{
		{".git", 3, false, true},
		{"node_modules", 3, false, true},
		{"build", 3, false, true},
		{"Build-tools", 3, false, false},
		{"build_scripts", 3, false, false},
		{"Vendor", 3, false, false},
		{"scratch", 0, false, true},
		{"cache", 2, true, true},
		{"cache", 2, false, false},
		{"scratch", 1, false, false},
	}
	for _, tc := range cases {
		if got := prunedFromDiscovery(tc.name, tc.parentDepth, tc.parentIsRepo); got != tc.want {
			t.Errorf("prunedFromDiscovery(%q, %d, %v) = %v, want %v", tc.name, tc.parentDepth, tc.parentIsRepo, got, tc.want)
		}
	}
}

// TestFleetEpicsScoreLikeAggregate pins review case c1 (M2): a fleet epic, a
// single-repository epic and the aggregate row score one repository identically, with
// its sub-projects folded in, and the aggregate ranks it once.
func TestFleetEpicsScoreLikeAggregate(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	repo := filepath.Join(root, "vmafx")
	makeCheckout(t, repo)
	writeGoProject(t, repo, "example.com/vmafx", pgxModule)
	writeRepoFile(t, filepath.Join(repo, "core", "meson.build"), "dep = dependency('zlib')\n")
	writeRepoFile(t, filepath.Join(repo, "python", "pyproject.toml"), "[project]\ndependencies = [\"numpy>=2\"]\n")

	_, rows := aggregateRows(t, root)
	row, ok := rows[repo]
	if len(rows) != 1 || !ok {
		t.Fatalf("aggregate rows = %v, want the one repository", rows)
	}
	epics, _, err := RegenerateFleetEpics(ctx, root, FleetEpicOptions{DryRun: true})
	if err != nil || len(epics) != 1 {
		t.Fatalf("fleet epics = %d, err = %v", len(epics), err)
	}
	single, err := GeneratePreMigrationEpic(ctx, repo, "")
	if err != nil {
		t.Fatal(err)
	}
	for name, epic := range map[string]*PreMigrationEpic{"fleet": epics[0], "single": single} {
		if epic.ReadinessScore != row.Readiness.Score {
			t.Errorf("%s epic readiness %.2f, aggregate row %.2f", name, epic.ReadinessScore, row.Readiness.Score)
		}
		total := "`" + strconv.Itoa(row.Readiness.TotalThirdPartyDeps) + "` total"
		if row.Readiness.TotalThirdPartyDeps < 3 || !strings.Contains(epic.ChecklistMarkdown, total) {
			t.Errorf("%s epic does not count the folded %d dependencies:\n%s", name, row.Readiness.TotalThirdPartyDeps, epic.ChecklistMarkdown)
		}
	}
}

// TestFleetEpicsFailOnDeclarationOnlyRepositories pins review case c10 (M1): a repository
// that declares needs but holds no analyzable project fails the run loudly, while an
// undeclared checkout with no project and a collapsed worktree are counted skips.
func TestFleetEpicsFailOnDeclarationOnlyRepositories(t *testing.T) {
	root := t.TempDir()
	good := filepath.Join(root, "good")
	makeCheckout(t, good)
	writeGoProject(t, good, "example.com/good", pgxModule)
	secondary := filepath.Join(root, "good-secondary")
	makeLinkedWorktree(t, good, secondary, "secondary")
	docs := filepath.Join(root, "docs")
	writeRepoFile(t, filepath.Join(docs, ".standards.yaml"), "repository:\n  name: docs\n")
	declared := filepath.Join(root, "declared")
	makeCheckout(t, declared)
	writeRepoFile(t, filepath.Join(declared, ".needs.yaml"), "capabilities:\n  required: []\n")
	notes := filepath.Join(root, "notes")
	makeCheckout(t, notes)
	writeRepoFile(t, filepath.Join(notes, "README.md"), "# notes\n")

	epics, skips, err := RegenerateFleetEpics(context.Background(), root, FleetEpicOptions{DryRun: true})
	if !errors.Is(err, ErrNoAnalyzer) || !strings.Contains(err.Error(), docs) || !strings.Contains(err.Error(), declared) {
		t.Fatalf("error = %v, want ErrNoAnalyzer failures naming %s and %s", err, docs, declared)
	}
	if len(epics) != 1 {
		t.Errorf("epics = %d, want only the analyzable repository", len(epics))
	}
	want := []FleetEpicSkip{
		{RepoDir: secondary, Reason: duplicateReasonPrefix + good},
		{RepoDir: notes, Reason: noAnalyzerReason},
	}
	if !slices.Equal(skips, want) {
		t.Errorf("skips = %+v, want %+v", skips, want)
	}
}

// TestAnalyzerManifestNamesAreDetected keeps discovery's manifest list and the analyzers'
// Detect in step: every name discovery treats as a project marker is one an analyzer
// detects.
func TestAnalyzerManifestNamesAreDetected(t *testing.T) {
	for _, name := range []string{"go.mod", "package.json", "pyproject.toml", "requirements.txt",
		"setup.py", "Cargo.toml", "meson.build", "CMakeLists.txt"} {
		dir := t.TempDir()
		writeRepoFile(t, filepath.Join(dir, name), "")
		if !isAnalyzerManifest(name) || len(DefaultRegistry().DetectAll(dir)) == 0 {
			t.Errorf("%s: discovery and analyzer detection disagree", name)
		}
	}
	if isAnalyzerManifest(".standards.yaml") || !isDeclarationFile(".needs.yaml") {
		t.Error("declarations must mark repositories without being analyzer manifests")
	}
}
