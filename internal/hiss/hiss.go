package hiss

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	DefaultMaxFuncLOC = 60 // Gerard Holzmann NASA JPL Power-of-10 Rule 4
	MaxInfractionsCap = 10000
	// DefaultScanTimeout bounds a scan whose caller context carries no deadline (HISS-02).
	DefaultScanTimeout = 30 * time.Second
	// MaxScanFileSize bounds the bytes read from any single source file. Larger files
	// are counted in ScanReport.Skips.Oversize and never scanned.
	MaxScanFileSize = 2 * 1024 * 1024
	// maxReportedSkippedDirs bounds the ignored-directory list carried by a report.
	maxReportedSkippedDirs = 128
	// maxPathSegments bounds the per-segment ignore match (HISS-02).
	maxPathSegments = 256
)

// ErrScanTruncated is wrapped by callers that refuse to certify a partial report.
var ErrScanTruncated = errors.New("hiss: scan truncated at an analysis bound; the report is a lower bound")

// ScanOptions configures the static invariant scanner.
type ScanOptions struct {
	MaxFuncLOC int
	Cap        int
	// Timeout bounds the scan. Zero keeps the caller's deadline when it has one and
	// applies DefaultScanTimeout otherwise.
	Timeout time.Duration
	// IgnoreDirs lists additional directory names to skip at any depth, matched per
	// path segment and case-insensitively, on top of the built-in generated,
	// dependency and tool-state directories.
	IgnoreDirs []string
}

// InvariantViolation captures an individual HISS infraction.
type InvariantViolation struct {
	RuleID     string `json:"rule_id"`
	FilePath   string `json:"file_path"`
	LineNumber int    `json:"line_number"`
	Symbol     string `json:"symbol,omitempty"`
	Message    string `json:"message"`
}

// ScanSkips records what the scanner deliberately left out, so that a clean report
// can never be mistaken for a complete one.
type ScanSkips struct {
	// Dirs lists the first maxReportedSkippedDirs ignored directories, relative to the root.
	Dirs []string `json:"dirs,omitempty"`
	// DirCount is the total number of ignored directories, including unlisted ones.
	DirCount int `json:"dir_count"`
	// Symlinks counts symbolic links that were not followed.
	Symlinks int `json:"symlinks"`
	// Oversize counts files above MaxScanFileSize that were not read.
	Oversize int `json:"oversize"`
}

// ScanReport aggregates all discovered invariant violations.
type ScanReport struct {
	TotalInfractions int                  `json:"total_infractions"`
	Breakdown        map[string]int       `json:"breakdown"`
	Violations       []InvariantViolation `json:"violations"`
	// Truncated is true when an infraction or AST-depth bound prevented a complete
	// scan. TotalInfractions is a lower bound, including when zero were observed.
	Truncated bool `json:"truncated,omitempty"`
	// Skips accounts for directories and files excluded from the scan.
	Skips ScanSkips `json:"skips"`
	// Coverage is present on newly executed scans. A missing value in a retained
	// report means coverage was not recorded, not that every source was analyzed.
	Coverage *ScanCoverage `json:"coverage,omitempty"`

	capLimit int
}

// Scan audits repository source code against HISS-01, HISS-02, HISS-04, HISS-07,
// HISS-08 and HISS-09.
//
// HISS-02: the walk always runs under a deadline (ScanOptions.Timeout, the caller's
// own deadline, or DefaultScanTimeout). HISS-07: an unreadable root or subtree fails
// the scan instead of producing an empty, error-free report.
func Scan(ctx context.Context, repoPath string, opts ScanOptions) (*ScanReport, error) {
	if ctx == nil {
		return nil, errors.New("hiss: context cannot be nil")
	}
	opts = opts.withDefaults()
	ctx, cancel := scanContext(ctx, opts.Timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("hiss cancelled: %w", err)
	}

	rep := newScanReport(opts.Cap)
	w := &scanWalker{ctx: ctx, root: repoPath, opts: opts, rep: rep, extra: ignoreSet(opts.IgnoreDirs)}
	if err := filepath.Walk(repoPath, w.visit); err != nil {
		return nil, fmt.Errorf("hiss: scan %q: %w", repoPath, err)
	}
	rep.TotalInfractions = len(rep.Violations)
	return rep, nil
}

// withDefaults fills the zero-valued limits.
func (o ScanOptions) withDefaults() ScanOptions {
	if o.MaxFuncLOC <= 0 {
		o.MaxFuncLOC = DefaultMaxFuncLOC
	}
	if o.Cap <= 0 {
		o.Cap = MaxInfractionsCap
	}
	return o
}

// scanContext derives the bounded scan context: an explicit timeout wins, a caller
// deadline is preserved, and a deadline-free context receives DefaultScanTimeout.
func scanContext(ctx context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(ctx, timeout)
	}
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, DefaultScanTimeout)
}

func newScanReport(capLimit int) *ScanReport {
	return &ScanReport{
		Breakdown:  make(map[string]int),
		Violations: make([]InvariantViolation, 0),
		Coverage:   &ScanCoverage{UnscannedByExtension: make(map[string]int)},
		capLimit:   capLimit,
	}
}

// scanWalker carries the per-scan state through the filepath.Walk callback.
type scanWalker struct {
	ctx   context.Context
	root  string
	opts  ScanOptions
	rep   *ScanReport
	extra map[string]struct{}
}

func (w *scanWalker) visit(path string, info os.FileInfo, err error) error {
	if ctxErr := w.ctx.Err(); ctxErr != nil {
		return fmt.Errorf("cancelled at %q: %w", path, ctxErr)
	}
	rel, relErr := filepath.Rel(w.root, path)
	if relErr != nil {
		return fmt.Errorf("relativize %q: %w", path, relErr)
	}
	if err != nil {
		// An ignored directory that cannot be listed is skipped like any other; every
		// other walk error (missing root, unreadable subtree) fails the scan (HISS-07).
		if w.isIgnoredDir(info, rel) {
			w.rep.recordSkippedDir(rel)
			return filepath.SkipDir
		}
		return fmt.Errorf("walk %q: %w", path, err)
	}
	if info.IsDir() {
		if w.isIgnoredDir(info, rel) {
			w.rep.recordSkippedDir(rel)
			return filepath.SkipDir
		}
		return nil
	}
	return w.visitFile(path, rel, info)
}

func (w *scanWalker) isIgnoredDir(info os.FileInfo, rel string) bool {
	return info != nil && info.IsDir() && rel != "." && ignoreDir(info.Name(), rel, w.extra)
}

func (w *scanWalker) visitFile(path, rel string, info os.FileInfo) error {
	if w.rep.Truncated {
		return filepath.SkipAll
	}
	if ShouldIgnorePath(rel) {
		return nil
	}
	if !isScannableExt(strings.ToLower(filepath.Ext(rel))) {
		w.rep.Coverage.recordUnscanned(rel)
		return nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		w.rep.Skips.Symlinks++
		return nil
	}
	if info.Size() > MaxScanFileSize {
		w.rep.Skips.Oversize++
		return nil
	}
	return scanFile(w.root, rel, w.rep, w.opts)
}

// ShouldIgnoreDir reports whether a directory is skipped completely during traversal.
// name is the directory's base name and rel its path relative to the scan root; a match
// on any segment of rel exempts the directory.
func ShouldIgnoreDir(name, rel string) bool {
	return ignoreDir(name, rel, nil)
}

func ignoreDir(name, rel string, extra map[string]struct{}) bool {
	if isIgnoredDirName(name, extra) {
		return true
	}
	return hasIgnoredSegment(rel, true, extra)
}

// ShouldIgnorePath reports whether a file path (relative to the scan root) lies under
// an ignored directory. Only directory segments are matched: a file whose own name
// resembles an ignored directory (pkg/build_helpers.go) is still scanned.
func ShouldIgnorePath(rel string) bool {
	return hasIgnoredSegment(rel, false, nil)
}

// ignoredDirNames are the built-in directory names skipped at any depth: generated
// output, dependency trees, caches and tool state. They are matched per path segment,
// never as substrings. Project-specific names belong in ScanOptions.IgnoreDirs.
var ignoredDirNames = map[string]struct{}{
	".git": {}, ".corpus": {}, ".standards": {}, ".harvest": {},
	"vendor": {}, "node_modules": {}, "third_party": {},
	".venv": {}, "build": {}, "target": {}, "testdata": {},
	".cache": {}, ".idea": {}, ".vscode": {}, ".claude": {},
	".workingdir": {}, ".workingdir2": {},
	".pytest_cache": {}, ".mypy_cache": {}, ".ruff_cache": {},
}

// ignoredDirPrefixes are directory-name prefixes skipped at any depth (CMake-style
// build-<variant> output trees).
var ignoredDirPrefixes = []string{"build-", "build_"}

func isIgnoredDirName(name string, extra map[string]struct{}) bool {
	norm := strings.ToLower(name)
	if _, ok := ignoredDirNames[norm]; ok {
		return true
	}
	if _, ok := extra[norm]; ok {
		return true
	}
	for _, p := range ignoredDirPrefixes {
		if strings.HasPrefix(norm, p) {
			return true
		}
	}
	return false
}

// hasIgnoredSegment matches every directory segment of rel against the ignore set.
// includeLast treats the final segment as a directory too.
func hasIgnoredSegment(rel string, includeLast bool, extra map[string]struct{}) bool {
	segments := strings.Split(filepath.ToSlash(rel), "/")
	n := len(segments)
	if !includeLast {
		n--
	}
	for i := 0; i < n && i < maxPathSegments; i++ {
		if segments[i] != "" && isIgnoredDirName(segments[i], extra) {
			return true
		}
	}
	return false
}

func ignoreSet(names []string) map[string]struct{} {
	if len(names) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		if trimmed := strings.ToLower(strings.TrimSpace(n)); trimmed != "" {
			set[trimmed] = struct{}{}
		}
	}
	return set
}

func isScannableExt(ext string) bool {
	return isNativeExt(ext) || ext == ".py" || ext == ".go" || ext == ".rs"
}

// SupportsExtension reports whether the HISS scanner has a language dispatch for ext.
// The answer is based on the same dispatch table used by Scan.
func SupportsExtension(ext string) bool {
	return isScannableExt(strings.ToLower(ext))
}

// scanFile reads one source file under a byte bound and dispatches it to the language
// scanner. The path is re-confined to the root before it is opened.
func scanFile(root, rel string, rep *ScanReport, opts ScanOptions) error {
	data, err := readBounded(root, rel)
	if err != nil {
		if errors.Is(err, errOversize) {
			rep.Skips.Oversize++
			return nil
		}
		return err
	}
	rep.Coverage.FilesRead++
	ext := strings.ToLower(filepath.Ext(rel))
	if ext == ".go" {
		scanGoSource(data, rel, rep, opts)
		return nil
	}
	lines := strings.Split(string(data), "\n")
	switch {
	case isNativeExt(ext):
		scanNativeLines(lines, rel, rep, opts)
	case ext == ".py":
		scanPythonLines(lines, rel, rep, opts)
	case ext == ".rs":
		scanRustLines(lines, rel, rep, opts)
	}
	return nil
}

var errOversize = errors.New("hiss: file exceeds MaxScanFileSize")

// readBounded reads at most MaxScanFileSize bytes of the file at root/rel and refuses
// anything larger, so a file that grew after its Walk stat (or a special file that never
// reaches EOF) cannot exhaust memory.
func readBounded(root, rel string) (data []byte, err error) {
	path, err := util.ConfinePath(root, rel)
	if err != nil {
		return nil, fmt.Errorf("confine %q: %w", rel, err)
	}
	// #nosec G304 -- path was produced by filepath.Walk under the scan root and re-confined
	// through util.ConfinePath, which rejects symlink escapes; the read is byte-bounded.
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", rel, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close %q: %w", rel, cerr)
		}
	}()
	data, err = io.ReadAll(io.LimitReader(f, MaxScanFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", rel, err)
	}
	if len(data) > MaxScanFileSize {
		return nil, errOversize
	}
	return data, nil
}

func isNativeExt(ext string) bool {
	return ext == ".c" || ext == ".cpp" || ext == ".cc" || ext == ".cxx" ||
		ext == ".h" || ext == ".hpp" || ext == ".cu" || ext == ".hip"
}

// recordViolation appends a violation unless the cap is reached, in which case the
// report is marked truncated so no caller can mistake it for a complete result.
func recordViolation(rep *ScanReport, ruleID, file string, line int, symbol, msg string) {
	if rep.capLimit > 0 && len(rep.Violations) >= rep.capLimit {
		rep.Truncated = true
		return
	}
	if rep.Breakdown == nil {
		rep.Breakdown = make(map[string]int)
	}
	rep.Violations = append(rep.Violations, InvariantViolation{
		RuleID:     ruleID,
		FilePath:   file,
		LineNumber: line,
		Symbol:     symbol,
		Message:    msg,
	})
	rep.Breakdown[ruleID]++
}

func (r *ScanReport) recordSkippedDir(rel string) {
	r.Skips.DirCount++
	if len(r.Skips.Dirs) < maxReportedSkippedDirs {
		r.Skips.Dirs = append(r.Skips.Dirs, filepath.ToSlash(rel))
	}
}

// ConvertToBaseline converts an InvariantViolation slice to baseline infractions.
func ConvertToBaseline(violations []InvariantViolation) []baseline.Infraction {
	result := make([]baseline.Infraction, 0, len(violations))
	for _, v := range violations {
		result = append(result, baseline.Infraction{
			RuleID:     v.RuleID,
			FilePath:   v.FilePath,
			LineNumber: v.LineNumber,
			Symbol:     v.Symbol,
			Message:    v.Message,
		})
	}
	return result
}
