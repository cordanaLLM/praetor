package needs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/topology"
)

const (
	// maxDiscoveryDirs bounds how many directories one discovery walk visits (HISS-02). A
	// walk that would visit more fails instead of returning a partial fleet.
	maxDiscoveryDirs = 250000
	// maxDiscoveryDepth bounds how far below the walk root a directory may sit. A deeper
	// tree fails the walk instead of being dropped.
	maxDiscoveryDepth = 64
	// maxSubprojectDepth bounds how many directories below its repository root a
	// sub-project is scanned. A manifest deeper than that is listed in
	// RepoNeeds.UnscannedSubprojects: reported, never scanned, never dropped.
	maxSubprojectDepth = 5
)

// ErrDiscoveryBound is returned when a discovery walk exceeds maxDiscoveryDirs or
// maxDiscoveryDepth.
var ErrDiscoveryBound = errors.New("needs: discovery walk exceeded its bound")

// discoveryPrunedNames are the exact, case-sensitive directory names discovery never
// enters: dependency trees, vendored sources, build output and test fixtures. No prefix or
// case folding applies (hiss.ShouldIgnoreDir folds both), so first-party directories such
// as Build-tools/ or build_scripts/ are walked.
var discoveryPrunedNames = map[string]struct{}{
	"vendor": {}, "node_modules": {}, "third_party": {},
	"build": {}, "target": {}, "testdata": {},
}

// fleetRepo is one repository a discovery walk found: a git checkout, or a directory
// outside every checkout that holds an analyzer manifest or a Praetor declaration.
type fleetRepo struct {
	root string
	// checkout is true for a git checkout, linked worktree or submodule.
	checkout bool
	// mainWorktree is true for a checkout whose .git is a directory.
	mainWorktree bool
	// declared is true when root holds .standards.yaml or .needs.yaml.
	declared bool
	// commonDir is the git common directory of a checkout; worktrees share it.
	commonDir string
	// subprojects are the directories holding an analyzer manifest at most
	// maxSubprojectDepth below root, root first when it holds one.
	subprojects []string
	// unscanned are the directories holding an analyzer manifest deeper than that.
	unscanned []string
}

// fleetLayout is the result of one discovery walk.
type fleetLayout struct {
	repos      []*fleetRepo
	duplicates []FleetDuplicate
}

// walkItem is one directory waiting on the discovery stack.
type walkItem struct {
	path string
	// depth counts directories below the walk root.
	depth int
	// owner is the repository the directory belongs to, nil outside every repository.
	owner *fleetRepo
	// relDepth counts directories below owner.root.
	relDepth int
}

// dirMarks records what one directory's entries mark it as.
type dirMarks struct {
	gitEntry     bool
	gitDirectory bool
	manifest     bool
	declaration  bool
}

// layoutWalker carries the state of one discovery walk.
type layoutWalker struct {
	ctx    context.Context
	root   string
	layout fleetLayout
	// rootOnly makes the walk root a repository whatever it holds and stops at every
	// nested checkout, whose tree belongs to another repository.
	rootOnly bool
}

// discoverFleet walks fleetRoot and returns every repository below it, fleetRoot included.
//
// A git checkout (by the shared topology checkout detection) starts a repository wherever
// it sits, so a submodule or an independent clone nested in another checkout is a
// repository of its own. Outside every checkout, a directory holding an analyzer manifest
// or a declaration starts a repository. Neither stops the walk: a checkout below a
// manifest or declaration directory is still found. Every other manifest directory is a
// sub-project of the repository it sits in. Linked worktrees of one repository (one git
// common directory) collapse onto a single repository and are returned as duplicates.
func discoverFleet(ctx context.Context, fleetRoot string) (*fleetLayout, error) {
	walker := &layoutWalker{ctx: ctx, root: filepath.Clean(fleetRoot)}
	if err := walker.walk(); err != nil {
		return nil, err
	}
	walker.collapseWorktrees()
	return &walker.layout, nil
}

// discoverRepository returns the repository rooted at repoRoot with the sub-projects the
// fleet walk would assign it. repoRoot is a repository whatever it holds: the caller named
// it. Nested checkouts are other repositories and are not entered.
func discoverRepository(ctx context.Context, repoRoot string) (*fleetRepo, error) {
	walker := &layoutWalker{ctx: ctx, root: filepath.Clean(repoRoot), rootOnly: true}
	if err := walker.walk(); err != nil {
		return nil, err
	}
	if len(walker.layout.repos) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoAnalyzer, repoRoot)
	}
	return walker.layout.repos[0], nil
}

// walk runs the bounded, iterative depth-first discovery walk in lexical order. Symlinked
// directories are never entered: os.ReadDir reports them as symlinks, not directories.
func (w *layoutWalker) walk() error {
	stack := []walkItem{{path: w.root}}
	for visited := 0; len(stack) > 0; visited++ {
		if visited >= maxDiscoveryDirs {
			return fmt.Errorf("%w: more than %d directories below %s", ErrDiscoveryBound, maxDiscoveryDirs, w.root)
		}
		item := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		children, err := w.visit(item)
		if err != nil {
			return err
		}
		stack = append(stack, children...)
	}
	return nil
}

// visit classifies one directory and returns the child directories to walk, in reverse
// lexical order so that the stack pops them lexically.
func (w *layoutWalker) visit(item walkItem) ([]walkItem, error) {
	if err := w.ctx.Err(); err != nil {
		return nil, err
	}
	if item.depth > maxDiscoveryDepth {
		return nil, fmt.Errorf("%w: %s sits more than %d directories below %s",
			ErrDiscoveryBound, item.path, maxDiscoveryDepth, w.root)
	}
	entries, err := os.ReadDir(item.path)
	if err != nil {
		return nil, fmt.Errorf("read directory %q: %w", item.path, err)
	}
	marks := classifyEntries(entries)
	owner, relDepth, opened, err := w.claim(item, marks)
	if err != nil {
		return nil, err
	}
	if owner != nil && marks.manifest {
		owner.addProject(item.path, relDepth)
	}
	if opened && w.rootOnly && item.path != w.root {
		return nil, nil
	}
	return w.children(item, entries, owner, relDepth), nil
}

// classifyEntries reports whether a directory's entries mark it as a checkout, a project
// or a declared repository. Only regular files count as manifests or declarations: a
// symlink's target may sit outside the walk, and a directory named go.mod is no manifest.
func classifyEntries(entries []os.DirEntry) dirMarks {
	var marks dirMarks
	for _, entry := range entries {
		name := entry.Name()
		if name == ".git" {
			marks.gitEntry = true
			marks.gitDirectory = entry.IsDir()
			continue
		}
		if !entry.Type().IsRegular() {
			continue
		}
		marks.manifest = marks.manifest || isAnalyzerManifest(name)
		marks.declaration = marks.declaration || isDeclarationFile(name)
	}
	return marks
}

// claim decides which repository the directory belongs to. opened reports that the
// directory starts a repository of its own.
func (w *layoutWalker) claim(item walkItem, marks dirMarks) (owner *fleetRepo, relDepth int, opened bool, err error) {
	if marks.gitEntry && topology.HasValidGitRepo(item.path) {
		common, cErr := topology.GitCommonDir(w.ctx, item.path)
		if cErr != nil {
			return nil, 0, false, fmt.Errorf("resolve the repository of checkout %q: %w", item.path, cErr)
		}
		repo := &fleetRepo{root: item.path, checkout: true, mainWorktree: marks.gitDirectory,
			declared: marks.declaration, commonDir: common}
		w.layout.repos = append(w.layout.repos, repo)
		return repo, 0, true, nil
	}
	if item.owner != nil {
		return item.owner, item.relDepth, false, nil
	}
	if marks.manifest || marks.declaration || (w.rootOnly && item.path == w.root) {
		repo := &fleetRepo{root: item.path, declared: marks.declaration}
		w.layout.repos = append(w.layout.repos, repo)
		return repo, 0, true, nil
	}
	return nil, 0, false, nil
}

// addProject records dir as a sub-project, or as unscanned beyond maxSubprojectDepth.
func (r *fleetRepo) addProject(dir string, relDepth int) {
	if relDepth > maxSubprojectDepth {
		r.unscanned = append(r.unscanned, dir)
		return
	}
	r.subprojects = append(r.subprojects, dir)
}

// children returns the subdirectories of item the walk enters.
func (w *layoutWalker) children(item walkItem, entries []os.DirEntry, owner *fleetRepo, relDepth int) []walkItem {
	next := make([]walkItem, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if !entry.IsDir() || prunedFromDiscovery(entry.Name(), item.depth, owner != nil && relDepth == 0) {
			continue
		}
		next = append(next, walkItem{
			path:     filepath.Join(item.path, entry.Name()),
			depth:    item.depth + 1,
			owner:    owner,
			relDepth: relDepth + 1,
		})
	}
	return next
}

// prunedFromDiscovery reports whether discovery skips the child directory name of a
// directory parentDepth levels below the walk root. Dot-directories and
// discoveryPrunedNames are skipped everywhere. scratch/ and cache/ are local work areas
// only directly under the walk root or directly under a repository root (parentIsRepo);
// deeper, as in internal/cache, they are ordinary source directories (BUG-864).
func prunedFromDiscovery(name string, parentDepth int, parentIsRepo bool) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	if _, pruned := discoveryPrunedNames[name]; pruned {
		return true
	}
	if name != "scratch" && name != "cache" {
		return false
	}
	return parentDepth == 0 || parentIsRepo
}

// collapseWorktrees keeps one repository per git common directory. The main worktree
// wins; without one in the walk, the first worktree found does. The others are recorded
// as duplicates of the one kept.
func (w *layoutWalker) collapseWorktrees() {
	owners := make(map[string]*fleetRepo)
	for _, repo := range w.layout.repos {
		if !repo.checkout {
			continue
		}
		kept, seen := owners[repo.commonDir]
		if !seen || (repo.mainWorktree && !kept.mainWorktree) {
			owners[repo.commonDir] = repo
		}
	}
	repos := make([]*fleetRepo, 0, len(w.layout.repos))
	for _, repo := range w.layout.repos {
		if kept := owners[repo.commonDir]; repo.checkout && kept != repo {
			w.layout.duplicates = append(w.layout.duplicates, FleetDuplicate{Dir: repo.root, Of: kept.root})
			continue
		}
		repos = append(repos, repo)
	}
	w.layout.repos = repos
}

// isAnalyzerManifest reports whether name is a file a registered analyzer detects a
// project by (CMakeLists.txt and setup.py included, BUG-864).
func isAnalyzerManifest(name string) bool {
	switch name {
	case "go.mod", "package.json", "pyproject.toml", "requirements.txt", "setup.py",
		"Cargo.toml", "meson.build", "CMakeLists.txt":
		return true
	}
	return false
}

// isDeclarationFile reports whether name is a Praetor declaration. A declaration starts a
// repository outside a checkout, but no analyzer detects a project by it.
func isDeclarationFile(name string) bool {
	return name == ".standards.yaml" || name == ".needs.yaml"
}

// scanRepository scans one discovered repository as one row. Every sub-project is
// analysed and merged into the row, and a demand two sub-projects share is counted once.
// When the root itself is no analyzer's project (a checkout or a declaration above
// core/meson.build), the row is named after the root and carries the root's declarations.
// A repository without a sub-project within maxSubprojectDepth fails with ErrNoAnalyzer,
// naming any deeper manifest it did not scan. Fleet aggregation, fleet epics and the
// single-repository scan all score through this function.
func scanRepository(ctx context.Context, repo *fleetRepo) (*RepoNeeds, error) {
	if len(repo.subprojects) == 0 {
		return nil, noProjectError(repo)
	}
	row, err := mergeSubprojectScans(ctx, repo.subprojects)
	if err != nil {
		return nil, err
	}
	nested := repo.subprojects
	if repo.subprojects[0] == repo.root {
		nested = repo.subprojects[1:]
	} else {
		row.Repository = repositoryDirName(repo.root)
		if declErr := loadExistingDeclarations(repo.root, row); declErr != nil {
			return nil, fmt.Errorf("failed to load declarations of repository %q: %w", repo.root, declErr)
		}
	}
	// One project may name a package in two spellings too (typing-extensions in
	// pyproject.toml, typing_extensions in requirements.txt).
	row.Dependencies = appendNewDemands(make([]DependencyDemand, 0, len(row.Dependencies)), row.Dependencies)
	row.Subprojects = relativeTo(repo.root, nested)
	row.UnscannedSubprojects = relativeTo(repo.root, repo.unscanned)
	calculateReadiness(row)
	return row, nil
}

// scanRepositoryWithFramework scores one repository against the selected framework.
func scanRepositoryWithFramework(ctx context.Context, repo *fleetRepo, framework *FrameworkIndex) (*RepoNeeds, error) {
	report, err := scanRepository(ctx, repo)
	if err != nil {
		return nil, err
	}
	applyFrameworkCoverage(framework, report)
	return report, nil
}

// noProjectError reports a repository no analyzer recognises within the depth bound.
func noProjectError(repo *fleetRepo) error {
	if len(repo.unscanned) == 0 {
		return fmt.Errorf("failed to analyze repository %q: %w: %s", repo.root, ErrNoAnalyzer, repo.root)
	}
	return fmt.Errorf("failed to analyze repository %q: %w within %d directories of %s; deeper manifests not scanned: %s",
		repo.root, ErrNoAnalyzer, maxSubprojectDepth, repo.root, strings.Join(relativeTo(repo.root, repo.unscanned), ", "))
}

// mergeSubprojectScans scans every directory in dirs and merges each result into the
// first one's.
func mergeSubprojectScans(ctx context.Context, dirs []string) (*RepoNeeds, error) {
	var row *RepoNeeds
	for _, dir := range dirs {
		sub, err := scanProjectDir(ctx, dir)
		if err != nil {
			return nil, err
		}
		if row == nil {
			row = sub
			continue
		}
		mergeRepoNeeds(row, sub)
	}
	return row, nil
}

// repositoryDirName names a repository after its root directory.
func repositoryDirName(root string) string {
	if abs, err := filepath.Abs(root); err == nil {
		return filepath.Base(abs)
	}
	return filepath.Base(root)
}

// relativeTo returns every dir relative to root in slash form, or nil for none.
func relativeTo(root string, dirs []string) []string {
	if len(dirs) == 0 {
		return nil
	}
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		rel, err := filepath.Rel(root, dir)
		if err != nil {
			rel = dir
		}
		out = append(out, filepath.ToSlash(rel))
	}
	return out
}
