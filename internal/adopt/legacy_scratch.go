package adopt

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/state"
	"github.com/cordanaLLM/praetor/internal/util"
)

// legacyScratchIgnore is the rule the managed block carries for the legacy scratch root
// (state.LegacyWorkingDirName). The block ignores that root by default; a repository that
// retired it can take the rule out and keep the path visible to Git (#641).
const legacyScratchIgnore = "/" + state.LegacyWorkingDirName + "/"

// keepsLegacyScratch reports whether legacyScratchIgnore is required of a repository whose
// .gitignore reads text. present says the repository holds an entry named
// state.LegacyWorkingDirName; declined says the git-ignore adoption step is declined, so no
// managed block is ever written and the operator's own rules decide.
//
// The rule is kept while the root is present, or while text carries the exact rule, inside the
// block or as the unmarked line Praetor's former format wrote. Beyond that it is the default:
// a .gitignore without a managed block gets the full block, so a root created after adoption
// is already ignored. An adopter retires the root by deleting the rule from an existing block
// (or, with git-ignore declined, by leaving it out of the operator-owned rules) while no entry
// of that name is on disk.
func keepsLegacyScratch(text string, present, declined bool) bool {
	normalized, _ := util.NormalizeLineEndings(text)
	if present || strings.Contains("\n"+normalized+"\n", "\n"+legacyScratchIgnore+"\n") {
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

// legacyScratchPresent reports whether the repository at repoPath holds an entry named
// state.LegacyWorkingDirName, of any kind, a dangling symbolic link included. An entry that
// cannot be inspected (an Lstat error other than not-exist) counts as present, so the rule
// stays required rather than silently dropped.
func legacyScratchPresent(repoPath string) bool {
	_, err := os.Lstat(filepath.Join(repoPath, state.LegacyWorkingDirName))
	return !errors.Is(err, fs.ErrNotExist)
}

// privateIgnoreRules returns managedIgnoreRules, in block order, without legacyScratchIgnore
// unless keepLegacy is set.
func privateIgnoreRules(keepLegacy bool) []string {
	if keepLegacy {
		return managedIgnoreRules
	}
	return slices.DeleteFunc(slices.Clone(managedIgnoreRules), func(rule string) bool {
		return rule == legacyScratchIgnore
	})
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
func PrivateScratchRoots(repoPath, text string, declined bool) []string {
	if keepsLegacyScratch(text, legacyScratchPresent(repoPath), declined) {
		return slices.Clone(everyPrivateScratchRoot)
	}
	return []string{state.WorkingDirName}
}

// EveryPrivateScratchRoot returns every private scratch root adoption protects by default. Audit
// probes them all when it cannot read an operator-owned .gitignore that Git still applies.
func EveryPrivateScratchRoot() []string {
	return slices.Clone(everyPrivateScratchRoot)
}
