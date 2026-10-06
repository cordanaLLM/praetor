package main

import (
	"context"
	"flag"
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
	// maxStaleFlag names the audit flag that bounds stale baseline entries (#349).
	maxStaleFlag = "max-stale-baseline-entries"
)

// resolveStaleBound returns the --max-stale-baseline-entries bound: -1 when the flag was not
// given, so stale entries are only reported, and the value when it was, which must not be
// negative.
func resolveStaleBound(fs *flag.FlagSet, value int) (int, error) {
	if !flagWasSet(fs, maxStaleFlag) {
		return -1, nil
	}
	if value < 0 {
		return 0, fmt.Errorf("--%s must be 0 or more, got %d", maxStaleFlag, value)
	}
	return value, nil
}

// auditRatchetPassed runs what a passing ratchet leaves to check: the stale baseline entries
// against the stated bound, then the growth guard versus --base.
func auditRatchetPassed(ctx context.Context, opts *auditOptions, base *baseline.Baseline, ratchet *baseline.RatchetResult) error {
	if err := auditStaleBaseline(ratchet, opts.maxStale); err != nil {
		return err
	}
	return auditBaselineGrowth(ctx, opts, base)
}

// auditStaleBaseline reports the baseline entries no current violation accounts for (#349): a
// cleanup that landed without a re-record leaves them, the ratchet passes, and each one is room a
// new finding can take. It fails only past bound, the stated --max-stale-baseline-entries; a
// negative bound means none was stated, so it warns and passes.
func auditStaleBaseline(ratchet *baseline.RatchetResult, bound int) error {
	notice := ratchet.StaleNotice()
	if notice == "" {
		return nil
	}
	if bound >= 0 && ratchet.Stale > bound {
		return fmt.Errorf("[FAIL] %s; --%s=%d allows at most %d", notice, maxStaleFlag, bound, bound)
	}
	limit := fmt.Sprintf("reported only; --%s=<n> fails the audit past n", maxStaleFlag)
	if bound >= 0 {
		limit = fmt.Sprintf("within --%s=%d", maxStaleFlag, bound)
	}
	fmt.Printf("[WARN] %s (%s)\n", notice, limit)
	return nil
}

// describeRejection renders a failed ratchet for `praetorctl audit` and `praetorctl baseline
// --verify`: it first attributes each unbaselined finding against the baseline's commits, the one
// it was recorded at and the one that last committed baselinePath (hiss.AttributeRatchet, #599),
// under the scan policy that produced current, then lists every violation when all is set
// (--all-violations) and the bounded Summary otherwise (#598).
func describeRejection(ctx context.Context, root, baselinePath string, scanOpts hiss.ScanOptions, base *baseline.Baseline,
	current []baseline.Infraction, ratchet *baseline.RatchetResult, all bool) string {
	hiss.AttributeRatchet(ctx, root, baselinePath, scanOpts, base, current, ratchet)
	if all {
		return ratchet.FullSummary()
	}
	return ratchet.Summary()
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
		commit, err := util.ResolveGitCommit(ctx, dir, baseRef)
		if err != nil {
			return nil, fmt.Errorf("[FAIL] base ref %q in %s: %w", baseRef, dir, err)
		}
		if commit == "" {
			return nil, fmt.Errorf("[FAIL] base ref %q does not resolve to a commit in %s", baseRef, dir)
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
