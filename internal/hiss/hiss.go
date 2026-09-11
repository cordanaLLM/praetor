package hiss

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/baseline"
)

const (
	DefaultMaxFuncLOC  = 60 // Gerard Holzmann NASA JPL Power-of-10 Rule 4
	MaxInfractionsCap  = 10000
	DefaultScanTimeout = 30 * time.Second
)

// ScanOptions configures the static invariant scanner.
type ScanOptions struct {
	MaxFuncLOC int
	Cap        int
	Timeout    time.Duration
}

// InvariantViolation captures an individual HISS infraction.
type InvariantViolation struct {
	RuleID     string `json:"rule_id"`
	FilePath   string `json:"file_path"`
	LineNumber int    `json:"line_number"`
	Symbol     string `json:"symbol,omitempty"`
	Message    string `json:"message"`
}

// ScanReport aggregates all discovered invariant violations.
type ScanReport struct {
	TotalInfractions int                  `json:"total_infractions"`
	Breakdown        map[string]int       `json:"breakdown"`
	Violations       []InvariantViolation `json:"violations"`
}

// Scan audits repository source code against HISS-01, HISS-02, HISS-04, HISS-07, and HISS-09.
func Scan(ctx context.Context, repoPath string, opts ScanOptions) (*ScanReport, error) {
	if ctx == nil {
		return nil, fmt.Errorf("hiss: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("hiss cancelled: %w", err)
	}

	if opts.MaxFuncLOC <= 0 {
		opts.MaxFuncLOC = DefaultMaxFuncLOC
	}
	if opts.Cap <= 0 {
		opts.Cap = MaxInfractionsCap
	}

	rep := &ScanReport{
		Breakdown:  make(map[string]int),
		Violations: make([]InvariantViolation, 0),
	}

	err := filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, relErr := filepath.Rel(repoPath, path)
		if relErr != nil {
			return nil
		}
		if info.IsDir() {
			if rel != "." && ShouldIgnoreDir(info.Name(), rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if len(rep.Violations) >= opts.Cap {
			return filepath.SkipAll
		}
		if ShouldIgnorePath(rel) {
			return nil
		}
		if info.Size() > 2*1024*1024 {
			return nil
		}

		return dispatchFileScan(path, rel, rep, opts)
	})

	rep.TotalInfractions = len(rep.Violations)
	return rep, err
}

// ShouldIgnoreDir checks if a directory should be skipped completely during traversal.
func ShouldIgnoreDir(name, rel string) bool {
	if ShouldIgnorePath(rel + "/") {
		return true
	}
	norm := strings.ToLower(name)
	return norm == ".git" || norm == ".corpus" || norm == "vendor" ||
		norm == "node_modules" || norm == ".venv" || norm == "build" ||
		strings.HasPrefix(norm, "build-") || strings.HasPrefix(norm, "build_") ||
		norm == "target" || norm == ".cache" || norm == ".idea" || norm == ".vscode" ||
		norm == "compat" || norm == "third_party" || norm == ".claude" ||
		norm == ".workingdir" || norm == ".workingdir2" || norm == "harvest" ||
		norm == ".harvest" || norm == "testdata" || norm == "model" ||
		norm == ".pytest_cache" || norm == ".mypy_cache" || norm == ".ruff_cache"
}

// ShouldIgnorePath filters out build, dependency, and tool directories.
func ShouldIgnorePath(rel string) bool {
	norm := filepath.ToSlash(rel)
	prefixes := []string{
		"vendor/", ".standards/", ".git/", "node_modules/",
		".venv/", "build/", "build-", "build_", "core/build/", "libvmaf/build/",
		"target/", ".cache/", ".idea/", ".vscode/",
		"compat/", "third_party/", ".claude/", ".workingdir/", ".workingdir2/",
		".corpus/", "harvest/", ".harvest/", "testdata/", "model/",
		".pytest_cache/", ".mypy_cache/", ".ruff_cache/",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(norm, p) || strings.Contains(norm, "/"+p) {
			return true
		}
	}
	return false
}

func isScannableExt(ext string) bool {
	return isNativeExt(ext) || ext == ".py" || ext == ".go" || ext == ".rs"
}

func dispatchFileScan(path, rel string, rep *ScanReport, opts ScanOptions) error {
	ext := strings.ToLower(filepath.Ext(path))
	if !isScannableExt(ext) {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")

	switch {
	case isNativeExt(ext):
		scanNativeLines(lines, rel, rep, opts)
	case ext == ".py":
		scanPythonLines(lines, rel, rep, opts)
	case ext == ".go":
		scanGoLines(lines, rel, rep, opts)
	case ext == ".rs":
		scanRustLines(lines, rel, rep, opts)
	}
	return nil
}

func isNativeExt(ext string) bool {
	return ext == ".c" || ext == ".cpp" || ext == ".cc" || ext == ".cxx" ||
		ext == ".h" || ext == ".hpp" || ext == ".cu" || ext == ".hip"
}

func recordViolation(rep *ScanReport, ruleID, file string, line int, symbol, msg string) {
	rep.Violations = append(rep.Violations, InvariantViolation{
		RuleID:     ruleID,
		FilePath:   file,
		LineNumber: line,
		Symbol:     symbol,
		Message:    msg,
	})
	rep.Breakdown[ruleID]++
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
