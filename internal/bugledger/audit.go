package bugledger

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// LedgerRel is the ledger's path inside a repository. The directory is private and git-ignored;
// the audit reads it and never writes it.
const LedgerRel = ".workingdir/BUGS.md"

const (
	ledgerDirRel   = ".workingdir"
	ledgerFileName = "BUGS.md"
)

// Report says what was read as well as what failed, so a clean run cannot be confused with a
// run that read nothing.
type Report struct {
	Rows      int
	Open      int
	Locatable int
	Findings  []Finding
}

// Audit checks every open row whose location names a Go file and line, and reports the rows
// whose location no longer resolves.
//
// A location that still resolves is not evidence the defect survives; it only means the row
// can still be found. The reverse is stronger: a row pointing past the end of its file has
// certainly not been re-read since the file changed.
func Audit(ctx context.Context, repoPath string) (*Report, error) {
	if ctx == nil {
		return nil, errors.New("bug ledger audit requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	data, err := readLedger(ctx, repoPath)
	if errors.Is(err, os.ErrNotExist) {
		return &Report{}, nil
	}
	if err != nil {
		return nil, err
	}
	rows, err := Parse(data)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNoRows
	}
	report := &Report{Rows: len(rows)}
	for i := 0; i < len(rows) && i < maxLedgerRows; i++ {
		if !rows[i].Open() {
			continue
		}
		report.Open++
		check := CheckLocation(repoPath, rows[i].Location)
		if !check.Checked {
			continue
		}
		report.Locatable++
		if check.Stale {
			report.Findings = append(report.Findings, Finding{Row: rows[i], Detail: check.Detail})
		}
	}
	return report, nil
}

// LocationCheck is the verdict of re-reading one recorded location against the tree.
type LocationCheck struct {
	// Checked is false when the location is not a Go file:line, so nothing re-read it.
	Checked bool
	// Stale reports a location that no longer resolves; Detail says why.
	Stale  bool
	Detail string
}

// CheckLocation re-reads one recorded location the way Audit does. It is the ledger's one
// re-check, shared with the backlog cap batches (internal/backlogcap). A location that still
// resolves is not evidence the defect survives; a stale one proves nobody re-read the row.
func CheckLocation(repoPath, location string) LocationCheck {
	path, line, ok := Row{Location: location}.FileLine()
	if !ok {
		return LocationCheck{}
	}
	detail, stale := checkLocation(repoPath, path, line)
	return LocationCheck{Checked: true, Stale: stale, Detail: detail}
}

// checkLocation reports whether a recorded file and line still resolve.
func checkLocation(repoPath, rel string, line int) (string, bool) {
	full, err := util.ConfinePath(repoPath, filepath.FromSlash(rel))
	if err != nil {
		return "location escapes the repository", true
	}
	info, err := os.Stat(full)
	if errors.Is(err, os.ErrNotExist) {
		return "file no longer exists", true
	}
	if err != nil || info.IsDir() {
		return "location is not a readable file", true
	}
	count, err := countLines(full)
	if err != nil {
		return fmt.Sprintf("file cannot be read: %v", err), true
	}
	if line > count {
		return fmt.Sprintf("line %d is past the end of a %d-line file", line, count), true
	}
	return "", false
}

func countLines(path string) (int, error) {
	// #nosec G304 -- path was confined to the repository root by ConfinePath.
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if len(data) > maxLedgerBytes {
		return 0, fmt.Errorf("source file exceeds %d bytes", maxLedgerBytes)
	}
	count := 0
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			count++
		}
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		count++
	}
	return count, nil
}

func readLedger(ctx context.Context, repoPath string) (_ []byte, err error) {
	// ReadRootSnapshot takes a flat name, so the ledger's directory is opened first and the
	// read stays confined to it.
	root, err := contextopt.OpenDirectoryIn(ctx, repoPath, filepath.FromSlash(ledgerDirRel))
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	return contextopt.ReadRootSnapshot(ctx, root, ledgerFileName)
}
