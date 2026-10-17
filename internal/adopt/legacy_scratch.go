package adopt

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/state"
	"github.com/cordanaLLM/praetor/internal/util"
)

// legacyScratchIgnore is the rule the managed block carries for the legacy scratch root
// (state.LegacyWorkingDirName), at every depth like workingDirIgnore (#1263). The block ignores
// that root by default; a repository that retired it can take the rule out and keep the path
// visible to Git (#641).
const legacyScratchIgnore = state.LegacyWorkingDirName + "/"

// rootLegacyScratchIgnore is the root-anchored form of legacyScratchIgnore that blocks up to v2
// carried. It still counts as the rule wherever a repository holds it.
const rootLegacyScratchIgnore = "/" + legacyScratchIgnore

// isLegacyScratchRule reports whether rule is the legacy scratch rule in either form.
func isLegacyScratchRule(rule string) bool {
	return rule == legacyScratchIgnore || rule == rootLegacyScratchIgnore
}

// keepsLegacyScratch reports whether legacyScratchIgnore is required of a repository whose
// .gitignore reads text. present says the repository holds an entry named
// state.LegacyWorkingDirName; declined says the git-ignore adoption step is declined, so no
// managed block is ever written and the operator's own rules decide.
//
// The rule is kept while the root is present, or while text carries the exact rule in either
// form (isLegacyScratchRule), inside the block or as an unmarked line. Beyond that it is the default:
// a .gitignore without a managed block gets the full block, so a root created after adoption
// is already ignored. An adopter retires the root by deleting the rule from an existing block
// (or, with git-ignore declined, by leaving it out of the operator-owned rules) while no entry
// of that name is on disk.
func keepsLegacyScratch(text string, present, declined bool) bool {
	normalized, _ := util.NormalizeLineEndings(text)
	if present || slices.ContainsFunc(strings.Split(normalized, "\n"), isLegacyScratchRule) {
		return true
	}
	return !declined && !managedGitIgnoreBlockHeld(text)
}

// managedGitIgnoreBlockHeld reports whether text holds a readable managed .gitignore block. A
// file the block reader refuses counts as holding none, so the default applies to it; the
// merge that follows refuses it with the reader's error.
func managedGitIgnoreBlockHeld(text string) bool {
	_, held, err := gitIgnoreTailBlock().held(text)
	return err == nil && held
}

// legacyScratchOwnedTimeout bounds the git query legacyScratchOwned asks (HISS-02).
const legacyScratchOwnedTimeout = 10 * time.Second

// legacyScratchOwned reports whether the repository at repoPath, whose .gitignore reads text,
// keeps its legacy scratch root in Git, so the managed block must not ignore it (#1072): HEAD
// tracks an entry below it, or text un-ignores it with a negation such as
// !.workingdir2/**/*.md. A block rule appended after the repository's own rules would hide
// every new file there. The reason names which, for the report; a git that cannot answer
// counts as tracking nothing, so the default (keepsLegacyScratch) decides.
func legacyScratchOwned(ctx context.Context, repoPath, text string) (bool, string) {
	normalized, _ := util.NormalizeLineEndings(text)
	for _, line := range strings.Split(normalized, "\n") {
		if trimmed := strings.TrimSpace(line); negatesLegacyScratch(trimmed) {
			return true, ".gitignore un-ignores it with " + trimmed
		}
	}
	ctx, cancel := context.WithTimeout(ctx, legacyScratchOwnedTimeout)
	defer cancel()
	result, err := util.RunGitProbe(ctx, repoPath, maxLegacyScratchTreeBytes, "ls-tree", "--name-only", "HEAD", "--", state.LegacyWorkingDirName)
	if err == nil && strings.TrimSpace(string(result.Stdout)) != "" {
		return true, "git tracks files below it"
	}
	return false, ""
}

// negatesLegacyScratch reports whether rule, one trimmed .gitignore line, un-ignores the legacy
// scratch root or a path below it: a negation whose pattern, without a leading slash or **/,
// is the root's name or starts with it and a slash. A sibling such as .workingdir2-archive is
// another directory.
func negatesLegacyScratch(rule string) bool {
	pattern, negated := strings.CutPrefix(rule, "!")
	if !negated {
		return false
	}
	pattern = strings.TrimPrefix(strings.TrimPrefix(pattern, "**/"), "/")
	return pattern == state.LegacyWorkingDirName || strings.HasPrefix(pattern, state.LegacyWorkingDirName+"/")
}

// maxLegacyScratchTreeBytes bounds the ls-tree answer legacyScratchOwned reads: the one entry
// for the root itself.
const maxLegacyScratchTreeBytes = 4096

// keepsLegacyScratchIn is keepsLegacyScratch for the repository at repoPath: the rule is never
// required while the repository keeps the root in Git (legacyScratchOwned).
func keepsLegacyScratchIn(ctx context.Context, repoPath, text string, declined bool) bool {
	if owned, _ := legacyScratchOwned(ctx, repoPath, text); owned {
		return false
	}
	return keepsLegacyScratch(text, legacyScratchPresent(repoPath), declined)
}

// legacyScratchPresent reports whether the repository at repoPath holds an entry named
// state.LegacyWorkingDirName, of any kind, a dangling symbolic link included. An entry that
// cannot be inspected (an Lstat error other than not-exist) counts as present, so the rule
// stays required rather than silently dropped.
func legacyScratchPresent(repoPath string) bool {
	_, err := os.Lstat(filepath.Join(repoPath, state.LegacyWorkingDirName))
	return !errors.Is(err, fs.ErrNotExist)
}

// privateIgnoreRules returns rules, in block order, without the legacy scratch rule in either
// form (isLegacyScratchRule) unless keepLegacy is set. It always returns a new slice to prevent
// callers from mutating the underlying rules.
func privateIgnoreRules(rules []string, keepLegacy bool) []string {
	if keepLegacy {
		return slices.Clone(rules)
	}
	return slices.DeleteFunc(slices.Clone(rules), isLegacyScratchRule)
}

// everyPrivateScratchRoot lists the private scratch roots a repository that keeps the legacy
// scratch root must keep out of Git.
var everyPrivateScratchRoot = []string{state.WorkingDirName, state.LegacyWorkingDirName}

// PrivateScratchRoots returns the private scratch roots, slash-separated and relative to
// repoPath, that Git must ignore in the repository at repoPath whose .gitignore reads text:
// the working directory always, and state.LegacyWorkingDirName while keepsLegacyScratch
// requires it. declined says the git-ignore adoption step is declined. Audit probes these
// roots, so with the managed block it demands what adoption writes (HasManagedGitIgnoreTail)
// and no more.
func PrivateScratchRoots(ctx context.Context, repoPath, text string, declined bool) []string {
	if keepsLegacyScratchIn(ctx, repoPath, text, declined) {
		return slices.Clone(everyPrivateScratchRoot)
	}
	return []string{state.WorkingDirName}
}

// EveryPrivateScratchRoot returns every private scratch root adoption protects by default. Audit
// probes them all when it cannot read an operator-owned .gitignore that Git still applies.
func EveryPrivateScratchRoot() []string {
	return slices.Clone(everyPrivateScratchRoot)
}
