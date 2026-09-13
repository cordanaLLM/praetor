package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// maxTouchedFiles bounds the change set considered by the touched-file clean rule.
	maxTouchedFiles = 10000
	// maxRatchetExamples bounds how many violations a ratchet failure lists per class.
	maxRatchetExamples = 3
)

// fingerprintViolations converts scanner violations into baseline infractions carrying
// the canonical "<file>:<line>:<rule>" fingerprint used by the ratchet.
func fingerprintViolations(violations []hiss.InvariantViolation) []baseline.Infraction {
	current := hiss.ConvertToBaseline(violations)
	for i := range current {
		current[i].Fingerprint = fmt.Sprintf("%s:%d:%s", current[i].FilePath, current[i].LineNumber, current[i].RuleID)
	}
	return current
}

// resolveTouchedFiles returns the change set the touched-file clean rule applies to,
// relative to the audited root: the --touched list when given, otherwise the files git
// reports as changed (uncommitted changes plus, when --base is set, everything on HEAD
// since the merge base with that ref). Outside a git repository the change set is empty
// unless --base was requested, which then fails.
func resolveTouchedFiles(ctx context.Context, opts *auditOptions) ([]string, error) {
	if len(opts.touched) > 0 {
		return cleanTouched(opts.touched), nil
	}

	topLevel, err := gitTopLevel(ctx, opts.rootDir)
	if err != nil {
		if opts.baseRef != "" {
			return nil, fmt.Errorf("[FAIL] --base=%s requires a git repository at %s: %w", opts.baseRef, opts.rootDir, err)
		}
		return nil, nil
	}

	changed, err := gitChangedFiles(ctx, opts.rootDir, opts.baseRef)
	if err != nil {
		return nil, err
	}
	return relativizeTouched(opts.rootDir, topLevel, changed)
}

// gitTopLevel returns the working tree root of the repository containing dir.
func gitTopLevel(ctx context.Context, dir string) (string, error) {
	out, err := util.RunGit(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel in %s: %w", dir, err)
	}
	return strings.TrimSpace(out), nil
}

// gitHasCommits reports whether HEAD resolves to a commit (false on an unborn branch).
func gitHasCommits(ctx context.Context, dir string) bool {
	_, err := util.RunGit(ctx, dir, "rev-parse", "--verify", "--quiet", "HEAD")
	return err == nil
}

// gitChangedFiles lists files changed in the working tree versus HEAD and, when baseRef
// is set, on HEAD since its merge base with baseRef. Paths are relative to the git root.
func gitChangedFiles(ctx context.Context, dir, baseRef string) ([]string, error) {
	if !gitHasCommits(ctx, dir) {
		// An unborn branch has nothing to diff against; every file is new by definition
		// and the baseline count rule still applies.
		return nil, nil
	}

	diffs := [][]string{{"diff", "--name-only", "HEAD"}}
	if baseRef != "" {
		if err := util.ValidateExecArg(baseRef); err != nil {
			return nil, fmt.Errorf("[FAIL] invalid --base value: %w", err)
		}
		if _, err := util.RunGit(ctx, dir, "rev-parse", "--verify", "--quiet", baseRef+"^{commit}"); err != nil {
			return nil, fmt.Errorf("[FAIL] base ref %q does not resolve to a commit in %s: %w", baseRef, dir, err)
		}
		diffs = append(diffs, []string{"diff", "--name-only", baseRef + "...HEAD"})
	}

	seen := make(map[string]struct{})
	var files []string
	for i := 0; i < len(diffs); i++ {
		out, err := util.RunGit(ctx, dir, diffs[i]...)
		if err != nil {
			return nil, fmt.Errorf("[FAIL] git %s failed: %w", strings.Join(diffs[i], " "), err)
		}
		for _, line := range splitLines(out, maxTouchedFiles) {
			if _, dup := seen[line]; dup {
				continue
			}
			seen[line] = struct{}{}
			files = append(files, line)
		}
	}
	return files, nil
}

// relativizeTouched converts git-root-relative paths into paths relative to the audited
// root, dropping files outside that root (they are not part of the scan).
func relativizeTouched(rootDir, topLevel string, files []string) ([]string, error) {
	absRoot, err := resolveAbsolute(rootDir)
	if err != nil {
		return nil, fmt.Errorf("[FAIL] resolve audited root %s: %w", rootDir, err)
	}
	absTop, err := resolveAbsolute(topLevel)
	if err != nil {
		return nil, fmt.Errorf("[FAIL] resolve git root %s: %w", topLevel, err)
	}

	touched := make([]string, 0, len(files))
	for i := 0; i < len(files) && i < maxTouchedFiles; i++ {
		rel, relErr := filepath.Rel(absRoot, filepath.Join(absTop, filepath.FromSlash(files[i])))
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		touched = append(touched, rel)
	}
	return touched, nil
}

// resolveAbsolute returns the symlink-resolved absolute form of path.
func resolveAbsolute(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

// cleanTouched normalises an explicit --touched list to cleaned, OS-native relative paths.
func cleanTouched(raw []string) []string {
	touched := make([]string, 0, len(raw))
	for i := 0; i < len(raw) && i < maxTouchedFiles; i++ {
		trimmed := strings.TrimSpace(raw[i])
		if trimmed == "" {
			continue
		}
		touched = append(touched, filepath.Clean(filepath.FromSlash(trimmed)))
	}
	return touched
}

// splitLines splits command output into at most limit trimmed, non-empty lines.
func splitLines(out string, limit int) []string {
	raw := strings.Split(out, "\n")
	lines := make([]string, 0, len(raw))
	for i := 0; i < len(raw) && len(lines) < limit; i++ {
		trimmed := strings.TrimSpace(raw[i])
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return lines
}

// auditBaselineGrowth enforces the HISS-13 monotonic debt invariant across commits: when
// --base is given, the working baseline must not record more infractions than the
// baseline committed on that ref. A growth that was recorded deliberately with
// `praetorctl baseline --record --allow-increase --reason=...` carries its rationale in
// the file; it is reported loudly but does not fail the gate.
func auditBaselineGrowth(ctx context.Context, opts *auditOptions, current *baseline.Baseline) error {
	if opts.baseRef == "" {
		return nil
	}
	topLevel, err := gitTopLevel(ctx, opts.rootDir)
	if err != nil {
		return fmt.Errorf("[FAIL] --base=%s requires a git repository: %w", opts.baseRef, err)
	}
	rel, err := gitRelativePath(topLevel, opts.baselinePath)
	if err != nil {
		return fmt.Errorf("[FAIL] baseline %s is not inside the repository %s: %w", opts.baselinePath, topLevel, err)
	}

	committed, found := readBaselineAtRef(ctx, opts.rootDir, opts.baseRef, rel)
	if !found {
		fmt.Printf("[INFO] No baseline committed at %s (%s); debt growth guard has nothing to compare.\n", opts.baseRef, firstLine(committed))
		return nil
	}
	previous, err := baseline.ParseBaseline([]byte(committed))
	if err != nil {
		return fmt.Errorf("[FAIL] baseline committed at %s is unreadable: %w", opts.baseRef, err)
	}

	if err := baseline.CheckMonotonic(previous, current); err != nil {
		if current.IncreaseRationale == "" {
			return fmt.Errorf("[FAIL] HISS-13 debt ratchet: %s grew versus %s (%w); fix the new violations or record the increase with 'praetorctl baseline --record --allow-increase --reason=<why>'",
				rel, opts.baseRef, err)
		}
		fmt.Printf("[WARN] HISS-13 debt ratchet: %s grew versus %s (%v); recorded rationale: %s\n",
			rel, opts.baseRef, err, current.IncreaseRationale)
		return nil
	}
	fmt.Printf("[PASS] HISS-13 debt ratchet: %s does not grow versus %s (%d -> %d).\n",
		rel, opts.baseRef, previous.TotalInfractions, current.TotalInfractions)
	return nil
}

// readBaselineAtRef returns the baseline document committed at ref, or found=false (with
// git's diagnostic as the content) when the ref carries no such file.
func readBaselineAtRef(ctx context.Context, dir, ref, rel string) (content string, found bool) {
	out, err := util.RunGit(ctx, dir, "show", ref+":"+rel)
	if err != nil {
		return out, false
	}
	return out, true
}

// gitRelativePath returns path relative to the git root in slash form, as `git show
// <ref>:<path>` expects, refusing paths outside the repository.
func gitRelativePath(topLevel, path string) (string, error) {
	absTop, err := resolveAbsolute(topLevel)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolvedDir, dirErr := filepath.EvalSymlinks(filepath.Dir(absPath)); dirErr == nil {
		absPath = filepath.Join(resolvedDir, filepath.Base(absPath))
	}
	rel, err := filepath.Rel(absTop, absPath)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s escapes %s", path, topLevel)
	}
	slashed := filepath.ToSlash(rel)
	if err := util.ValidateExecArg(slashed); err != nil {
		return "", err
	}
	return slashed, nil
}

// firstLine returns the first non-empty line of s, for compact diagnostics.
func firstLine(s string) string {
	lines := splitLines(s, 1)
	if len(lines) == 0 {
		return "no output"
	}
	return lines[0]
}
