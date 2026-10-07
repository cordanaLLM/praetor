package adopt

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// makefileExpander returns a Makefile's text with the includes the ownership check may follow
// replaced by the fragments they name (util.MakefileExpandIncludes). Its result is read, never
// written.
type makefileExpander func(string) string

// noMakefileIncludes follows no include: every one stays ambiguous.
func noMakefileIncludes(data string) string { return data }

// includeExpander follows the literal includes of the repository's Makefile that name a regular,
// tracked, symlink-free text file inside the repository (readTrackedFragment). Anything else stays
// an include line, which the reader treats as ambiguous (#843).
func (s *adoptSession) includeExpander(ctx context.Context) makefileExpander {
	return func(data string) string {
		return util.MakefileExpandIncludes(data, func(operand string) (string, bool) {
			return readTrackedFragment(ctx, s.repoPath, operand)
		})
	}
}

// maxImplicitSourceEntries bounds how many directory entries makefileImplicitSourceNear reads
// (HISS-02); a larger directory is treated as holding a source.
const maxImplicitSourceEntries = 4096

// makefileImplicitSourceNear reports whether Make could remake the included file rel below root
// from a source beside it. GNU Make remakes an included makefile before it reads it, and its
// built-in match-anything rules build "name" from "name.sh", "name.c", "name.o", "name,v",
// "RCS/name,v", "s.name" and "SCCS/s.name" (measured against GNU Make 4.4.1 with "include gen.mk"
// and a newer "gen.mk.sh"). A file with any of these neighbours may be regenerated, so its tracked
// text proves nothing. An unreadable or oversized directory counts as holding one.
func makefileImplicitSourceNear(root, rel string) bool {
	dir, base := path.Split(rel)
	handle, err := os.Open(filepath.Join(root, filepath.FromSlash(dir)))
	if err != nil {
		return true
	}
	entries, err := handle.ReadDir(maxImplicitSourceEntries + 1)
	if closeErr := handle.Close(); err != nil || closeErr != nil || len(entries) > maxImplicitSourceEntries {
		return true
	}
	for i := 0; i < len(entries) && i < maxImplicitSourceEntries; i++ {
		name := entries[i].Name()
		if makefileImplicitSourceName(name, base) {
			return true
		}
	}
	return false
}

// makefileImplicitSourceName reports whether name is a file or directory a built-in rule builds
// base from.
func makefileImplicitSourceName(name, base string) bool {
	return strings.HasPrefix(name, base+".") || name == base+",v" || name == "s."+base || name == "RCS" || name == "SCCS"
}

// readTrackedFragment returns the text of the file rel names below root when git tracks it under
// exactly that spelling and contextopt reads it as a bounded, regular, UTF-8 text file with no
// symlink on the way (ObserveSnapshotIn). An untracked, generated, missing, escaping or oversized
// file reads as false.
func readTrackedFragment(ctx context.Context, root, rel string) (string, bool) {
	clean := path.Clean(rel)
	if !filepath.IsLocal(filepath.FromSlash(clean)) {
		return "", false
	}
	untracked, err := util.GitUntrackedPaths(ctx, root, []string{clean})
	if err != nil || len(untracked) != 0 || makefileImplicitSourceNear(root, clean) {
		return "", false
	}
	data, exists, err := contextopt.ObserveSnapshotIn(ctx, root, filepath.FromSlash(clean))
	if err != nil || !exists {
		return "", false
	}
	return string(data), true
}
