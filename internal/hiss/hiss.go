package hiss

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanallm/praetor/internal/baseline"
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
		if err != nil || info.IsDir() {
			return nil
		}
		if len(rep.Violations) >= opts.Cap {
			return filepath.SkipDir
		}
		rel, err := filepath.Rel(repoPath, path)
		if err != nil {
			return nil
		}
		if ShouldIgnorePath(rel) {
			return nil
		}

		return dispatchFileScan(path, rel, rep, opts)
	})

	rep.TotalInfractions = len(rep.Violations)
	return rep, err
}

// ShouldIgnorePath filters out build, dependency, and tool directories.
func ShouldIgnorePath(rel string) bool {
	prefixes := []string{
		"vendor/", ".standards/", ".git/", "node_modules/",
		".venv/", "build/", "core/build/", "libvmaf/build/",
		"target/", ".cache/", ".idea/", ".vscode/",
		"compat/", "third_party/", ".claude/", ".workingdir/",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(rel, p) {
			return true
		}
	}
	return false
}

func dispatchFileScan(path, rel string, rep *ScanReport, opts ScanOptions) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	ext := strings.ToLower(filepath.Ext(path))

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
