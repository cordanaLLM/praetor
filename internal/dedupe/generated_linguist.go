package dedupe

import (
	"context"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

const maxLinguistAttrBytes = 8 << 20

// linguistGitProbe runs check-attr for linguist classification; tests may replace it.
var linguistGitProbe = util.RunGitProbe

type linguistGitProbeFunc func(context.Context, string, int, ...string) (util.CommandBytes, error)

// SetLinguistGitProbeForTest replaces the linguist attribute probe until the test ends.
func SetLinguistGitProbeForTest(t interface{ Cleanup(func()) }, probe linguistGitProbeFunc) {
	saved := linguistGitProbe
	linguistGitProbe = probe
	t.Cleanup(func() { linguistGitProbe = saved })
}

// linguistGeneratedPaths resolves linguist-generated for paths relative to repoPath. Non-git
// scopes have no git attributes: callers pass nil or call only from gitSourceFiles.
func linguistGeneratedPaths(ctx context.Context, repoPath string, paths []string) (map[string]struct{}, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	var stdin strings.Builder
	for i := 0; i < len(paths) && i < maxScopeEntries; i++ {
		stdin.WriteString(paths[i])
		stdin.WriteByte(0)
	}
	stdinCtx, err := util.WithCommandStdin(ctx, []byte(stdin.String()))
	if err != nil {
		return nil, fmt.Errorf("read linguist-generated attributes in %s: %w", repoPath, err)
	}
	res, err := linguistGitProbe(stdinCtx, repoPath, maxLinguistAttrBytes,
		"check-attr", "--stdin", "-z", "linguist-generated")
	if err != nil {
		return nil, fmt.Errorf("read linguist-generated attributes in %s: %w", repoPath, err)
	}
	attrs, err := util.ParseCheckAttrOutput(res.Stdout)
	if err != nil {
		return nil, fmt.Errorf("parse linguist-generated attributes in %s: %w", repoPath, err)
	}
	generated := make(map[string]struct{}, len(attrs))
	for path, pathAttrs := range attrs {
		if util.LinguistGeneratedAttrValue(pathAttrs["linguist-generated"]) {
			generated[path] = struct{}{}
		}
	}
	return generated, nil
}

// isLinguistGenerated reports whether git marks path, relative to the scope root, linguist-generated.
func (s sourceScope) isLinguistGenerated(path string) bool {
	if len(s.linguistGenerated) == 0 {
		return false
	}
	_, ok := s.linguistGenerated[path]
	return ok
}

// scannableGoFiles returns the scope's Go files without those git marks linguist-generated; each dropped file
// counts as a skipped generated file, like a file carrying the cmd/go generated header (one marking, both scanners).
func scannableGoFiles(scope sourceScope, report *DedupeReport) []string {
	files := make([]string, 0, len(scope.goFiles))
	for _, relPath := range scope.goFiles {
		if scope.isLinguistGenerated(relPath) {
			report.SkippedGeneratedFiles++
			continue
		}
		files = append(files, relPath)
	}
	return files
}
