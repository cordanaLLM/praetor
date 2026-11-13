package dedupe

import (
	"context"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	maxLinguistAttrBytes     = 8 << 20
	maxLinguistBatchPaths    = 10000
	maxLinguistBatchAttempts = 16
)

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
// scopes have no git attributes: callers pass nil or call only for a Git inventory.
func linguistGeneratedPaths(ctx context.Context, repoPath string, paths []string) (map[string]struct{}, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	if len(paths) > maxScopeEntries {
		return nil, fmt.Errorf("read linguist-generated attributes in %s: scope exceeds %d paths", repoPath, maxScopeEntries)
	}
	generated := make(map[string]struct{})
	for start, batch := 0, 0; start < len(paths) && batch < maxScopeEntries; batch++ {
		end := linguistBatchEnd(paths, start)
		records, consumed, err := linguistAttributeRecords(ctx, repoPath, paths[start:end])
		if err != nil {
			return nil, fmt.Errorf("read linguist-generated attributes in %s: %w", repoPath, err)
		}
		for i := 0; i < len(records) && i < maxScopeEntries; i++ {
			if util.LinguistGeneratedAttrValue(records[i].Value) {
				generated[records[i].Path] = struct{}{}
			}
		}
		start += consumed
	}
	return generated, nil
}

// linguistBatchEnd budgets paths plus the longest standard check-attr value and its delimiters.
func linguistBatchEnd(paths []string, start int) int {
	end, size := start, 0
	const overhead = len("linguist-generated") + len("unspecified") + 3
	for i := 0; end < len(paths) && i < maxLinguistBatchPaths; i++ {
		next := len(paths[end]) + overhead
		if end > start && size+next > maxLinguistAttrBytes {
			break
		}
		size += next
		end++
	}
	return end
}

// linguistAttributeRecords shrinks overflowing batches for arbitrary, nonstandard attribute values.
// Sixteen attempts reach a single path from the maximum 10,000-path batch; other failures abort.
func linguistAttributeRecords(ctx context.Context, repoPath string, paths []string) ([]util.CheckAttrRecord, int, error) {
	count := len(paths)
	for attempt := 0; attempt < maxLinguistBatchAttempts; attempt++ {
		stdinCtx, err := util.WithCommandStdin(ctx, []byte(strings.Join(paths[:count], "\x00")+"\x00"))
		if err != nil {
			return nil, 0, err
		}
		res, err := linguistGitProbe(stdinCtx, repoPath, maxLinguistAttrBytes,
			"check-attr", "--stdin", "-z", "linguist-generated")
		if err != nil && len(res.Stdout) == maxLinguistAttrBytes && count > 1 {
			count /= 2
			continue
		}
		if err != nil {
			return nil, 0, err
		}
		records, err := util.ParseCheckAttrRecords(res.Stdout)
		return records, count, err
	}
	return nil, 0, fmt.Errorf("linguist-generated batch exceeds %d attempts", maxLinguistBatchAttempts)
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
