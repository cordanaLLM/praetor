package adopt

import (
	"bytes"
	"context"
	"path"
	"path/filepath"

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

// readTrackedFragment returns the text of the file rel names below root when git tracks it under
// exactly that spelling and contextopt reads it as a bounded, regular, UTF-8 text file with no
// symlink on the way (ObserveSnapshotIn). An untracked, generated, missing, escaping or oversized
// file reads as false.
func readTrackedFragment(ctx context.Context, root, rel string) (string, bool) {
	clean := path.Clean(rel)
	if !filepath.IsLocal(filepath.FromSlash(clean)) {
		return "", false
	}
	listing, err := util.RunGitProbe(ctx, root, len(clean)+2, "--literal-pathspecs", "ls-files", "--cached", "-z", "--", clean)
	if err != nil || !bytes.Equal(listing.Stdout, []byte(clean+"\x00")) {
		return "", false
	}
	data, exists, err := contextopt.ObserveSnapshotIn(ctx, root, filepath.FromSlash(clean))
	if err != nil || !exists {
		return "", false
	}
	return string(data), true
}
