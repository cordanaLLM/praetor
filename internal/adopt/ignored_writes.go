package adopt

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// configDir is the directory adoption writes its pinned catalog, label taxonomy, checkpoint
// policy and hook scripts under; a Kconfig tree uses the same name for its build configuration
// file.
const configDir = ".config"

// editorsStep names the adoption step whose editor files are developer conveniences audit never
// reads; an ignored one is reported as a warning, not an error (reportIgnoredWrites).
const editorsStep = "editors"

// ignoreProbeName is a file name no ignore rule of a real repository targets; asking git about
// it below a directory asks whether the directory itself is ignored.
const ignoreProbeName = "PRAETOR-IGNORE-PROBE"

// maxIgnoredPathDepth bounds the parent directories walked for one path (HISS-02).
const maxIgnoredPathDepth = 64

// IgnoredPath is a file Praetor writes that the repository's own ignore rules keep out of every
// commit: git ignores it and does not track it, so a clean checkout, CI included, lacks it.
type IgnoredPath struct {
	// Path is the file, slash-separated and relative to the work tree.
	Path string
	// Rule names the ignore rule that hides it (util.GitIgnoreMatch.Rule). When a parent
	// directory is ignored, it is the rule that ignores that directory.
	Rule string
	// Negation is the rule that re-includes Path when placed after Rule: the directory-only
	// negation of its shallowest ignored parent directory, which comes first because git never
	// looks inside an ignored directory, or Path's own negation when no parent is ignored.
	Negation string
}

// IgnoredPaths returns, in git's order, the files among rels that git ignores and does not
// track, each with the rule that hides it and the negation that re-includes it. The answer
// comes from the repository's own ignore files (util.GitIgnoreMatches), never an operator's
// personal excludes, so a clean checkout sees the same. Adoption (reportIgnoredWrites) and the
// documentation gate audit both ask through it.
func IgnoredPaths(ctx context.Context, root string, rels []string) ([]IgnoredPath, error) {
	matches, err := util.GitIgnoreMatches(ctx, root, rels, false)
	if err != nil || len(matches) == 0 {
		return nil, err
	}
	hidden, err := ignoredParents(ctx, root, matches)
	if err != nil {
		return nil, err
	}
	found := make([]IgnoredPath, 0, len(matches))
	for i := 0; i < len(matches) && i < maxReportActions; i++ {
		found = append(found, IgnoredPath{
			Path: matches[i].Path, Rule: matches[i].Rule(), Negation: negationFor(matches[i].Path, hidden),
		})
	}
	return found, nil
}

// parentDirs returns the parent directories of rel, shallowest first: "a/b/c.txt" gives "a"
// and "a/b".
func parentDirs(rel string) []string {
	parts := strings.Split(rel, "/")
	dirs := make([]string, 0, len(parts))
	for i := 1; i < len(parts) && i <= maxIgnoredPathDepth; i++ {
		dirs = append(dirs, strings.Join(parts[:i], "/"))
	}
	return dirs
}

// ignoredParents returns the parent directories of the matched paths that git ignores by the
// patterns alone. Each is asked with a trailing slash, which git reads as a directory whether
// or not it exists yet, so a dry run gets the answer the real run would.
func ignoredParents(ctx context.Context, root string, matches []util.GitIgnoreMatch) (map[string]bool, error) {
	probes := make([]string, 0, len(matches))
	seen := make(map[string]bool)
	for i := 0; i < len(matches) && i < maxReportActions; i++ {
		for _, dir := range parentDirs(matches[i].Path) {
			if !seen[dir] {
				seen[dir] = true
				probes = append(probes, dir+"/")
			}
		}
	}
	ignored, err := util.GitIgnoreMatches(ctx, root, probes, true)
	if err != nil {
		return nil, err
	}
	hidden := make(map[string]bool, len(ignored))
	for _, match := range ignored {
		hidden[strings.TrimSuffix(match.Path, "/")] = true
	}
	return hidden, nil
}

// negationFor returns the rule that re-includes rel after the rule that ignores it: the
// directory-only negation of its shallowest ignored parent (configDirNegation for .config), or
// rel's own negation when no parent is ignored.
func negationFor(rel string, hidden map[string]bool) string {
	for _, dir := range parentDirs(rel) {
		if !hidden[dir] {
			continue
		}
		if dir == configDir {
			return configDirNegation
		}
		return "!/" + dir + "/"
	}
	return "!/" + rel
}

// ignoredWriteProblem is the finding adoption reports for p. editor marks an editor file,
// which stays local by the repository's choice and is reported as a warning.
func ignoredWriteProblem(p IgnoredPath, editor bool) string {
	note := ""
	if p.Negation == configDirNegation {
		note = " (directory-only, so Kconfig " + configDir + " files stay ignored at every depth)"
	}
	if editor {
		return fmt.Sprintf("%s: ignored by %s, so this editor configuration stays in this checkout only; "+
			"add %s after that rule to commit it%s", p.Path, p.Rule, p.Negation, note)
	}
	return fmt.Sprintf("%s: ignored by %s; adoption writes it, but git will not commit it, so a clean checkout lacks it. "+
		"Add %s after that rule%s, or stop ignoring the path", p.Path, p.Rule, p.Negation, note)
}

// reportIgnoredWrites checks every file the run wrote or verified, or would write in a dry run,
// against the repository's own ignore rules once the chain has run, so the rules the git-ignore
// step wrote count. Each ignored file is reported on the step that recorded it, naming the rule
// and the negation that re-includes it: an error, because git will not commit the file and a
// clean checkout lacks it, or a warning for an editor file, which audit never reads. Adoption
// still writes the file, so the local checkout works. In a dry run that planned
// configDirNegation, a file only its .config/ directory hides is left out: the real run
// re-includes it. When git cannot answer (not installed, not a work tree) the check is skipped
// and the skip is a warning, never passed off as a clean result.
func reportIgnoredWrites(ctx context.Context, s *adoptSession) {
	written := writtenFiles(s.report)
	ignored, err := IgnoredPaths(ctx, s.repoPath, written.paths)
	if err != nil {
		s.report.addWarning("adopted files not checked against .gitignore, so some may be uncommittable: %v", err)
		return
	}
	for i := 0; i < len(ignored) && i < maxReportActions; i++ {
		found := ignored[i]
		if s.configNegationPlanned && found.Negation == configDirNegation {
			continue
		}
		step := s.report.stepOfAction(written.action[found.Path])
		if step >= 0 && s.report.Steps[step].Name == editorsStep {
			s.report.addStepWarning(step, ignoredWriteProblem(found, true))
			continue
		}
		s.report.addStepError(step, ignoredWriteProblem(found, false))
	}
}

// writtenPaths lists the work-tree files a run's report names, in report order, each with the
// index of the first action entry that names it.
type writtenPaths struct {
	paths  []string
	action map[string]int
}

// writtenFiles collects the files r names as created, reconciled, merged, appended or
// replaced (committablePath).
func writtenFiles(r *AdoptReport) writtenPaths {
	written := writtenPaths{action: make(map[string]int)}
	for i := 0; i < len(r.ActionDetails) && i < maxReportActions; i++ {
		detail := r.ActionDetails[i]
		if detail.Action == actionSkip || detail.Action == actionRemove {
			continue
		}
		rel, ok := committablePath(detail.Path)
		if _, seen := written.action[rel]; !ok || seen {
			continue
		}
		written.action[rel] = i
		written.paths = append(written.paths, rel)
	}
	return written
}

// committablePath returns rel cleaned when it names a file git could commit: not absolute,
// not outside the work tree, not under .git (an installed hook), and not one of the private
// paths the managed block ignores by design (managedIgnoreRules).
func committablePath(rel string) (string, bool) {
	if rel == "" || strings.HasPrefix(rel, "/") || filepath.IsAbs(filepath.FromSlash(rel)) {
		return "", false
	}
	clean := path.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || clean == ".git" || strings.HasPrefix(clean, ".git/") {
		return "", false
	}
	return clean, !privateByDesign(clean)
}

// privateByDesign reports whether rel is a path the managed block ignores on purpose: the
// session ledgers, the gate worktrees, the per-host AGY configuration.
func privateByDesign(rel string) bool {
	for i := 0; i < len(managedIgnoreRules); i++ {
		rule := strings.TrimPrefix(managedIgnoreRules[i], "/")
		if rel == strings.TrimSuffix(rule, "/") || (strings.HasSuffix(rule, "/") && strings.HasPrefix(rel, rule)) {
			return true
		}
	}
	return false
}

// kconfigConfigRule returns the rule that hides .config/ when it is Kconfig-style: a rule that
// ignores a file named .config (a bare .config, /.config, .*), not a directory-only rule the
// repository chose for the directory itself. It reports false when no such rule hides the
// directory, when a negation already re-includes it, and when git cannot answer; the
// ignored-write check states that skip (reportIgnoredWrites).
func kconfigConfigRule(ctx context.Context, repoPath string) (util.GitIgnoreMatch, bool) {
	dirProbe := configDir + "/" + ignoreProbeName
	matches, err := util.GitIgnoreMatches(ctx, repoPath, []string{configDir, dirProbe}, true)
	if err != nil {
		return util.GitIgnoreMatch{}, false
	}
	var rule util.GitIgnoreMatch
	fileRule, dirHidden := false, false
	for _, match := range matches {
		switch match.Path {
		case configDir:
			rule, fileRule = match, !match.DirectoryOnly()
		case dirProbe:
			dirHidden = true
		}
	}
	return rule, fileRule && dirHidden
}

// preflightConfigRoot refuses, before any step writes, a repository whose root holds .config as
// a regular file, the name a Kconfig tree gives its build configuration. Adoption writes its
// pinned catalog, label taxonomy, checkpoint policy and hook scripts under a .config/
// directory, and one name cannot be both; checked only at write time, the first such write
// failed with a raw lstat error after earlier steps had written. A dry run is checked too, so
// its preview does not report a run that would fail. Any other kind of entry is left to the
// writers, which refuse what they cannot write through.
func preflightConfigRoot(repoPath string) error {
	info, err := os.Lstat(filepath.Join(repoPath, configDir))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("inspect %s: %w", configDir, err)
	case !info.Mode().IsRegular():
		return nil
	}
	return fmt.Errorf("%s at the repository root is a file, the name a Kconfig tree gives its build configuration, "+
		"and adoption writes its pinned catalog, label taxonomy, checkpoint policy and hook scripts under a %s/ directory; "+
		"one name cannot be both. Keep the build configuration out of the source root (an out-of-tree build) "+
		"or move the file, then adopt again", configDir, configDir)
}
