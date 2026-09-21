package adr

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

const (
	recordDirectory        = "docs/adr"
	maxRecords             = 4096
	maxDecisionConstraints = 16384
	maxScanFiles           = 20000
)

// Finding is one way the repository contradicts a decision it records.
type Finding struct {
	Constraint Constraint
	Detail     string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s [%s]: %s\n    because: %s",
		f.Constraint.Record, f.Constraint.ID, f.Detail, f.Constraint.Rationale)
}

// Report says what was checked as well as what failed. A verifier that prints only findings
// cannot be distinguished from one that read nothing, which is the failure mode that makes a
// green gate meaningless.
type Report struct {
	Records     int
	Constraints int
	Findings    []Finding
}

// Verify replays every constraint declared by the repository's decision records.
func Verify(ctx context.Context, repoPath string) (*Report, error) {
	if ctx == nil {
		return nil, errors.New("decision record verification requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	constraints, records, err := loadConstraints(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	report := &Report{Records: records, Constraints: len(constraints)}
	if len(constraints) == 0 {
		return report, nil
	}
	tracked, err := trackedPaths(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	for i := 0; i < len(constraints) && i < maxDecisionConstraints; i++ {
		report.Findings = append(report.Findings, checkConstraint(constraints[i], tracked)...)
	}
	return report, nil
}

// loadConstraints reads every decision record and returns the constraints they declare.
func loadConstraints(ctx context.Context, repoPath string) (_ []Constraint, records int, err error) {
	root, err := contextopt.OpenDirectoryIn(ctx, repoPath, recordDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	names, err := recordNames(root)
	if err != nil {
		return nil, 0, err
	}
	var constraints []Constraint
	for i := 0; i < len(names) && i < maxRecords; i++ {
		data, readErr := contextopt.ReadRootSnapshot(ctx, root, names[i])
		if readErr != nil {
			return nil, 0, fmt.Errorf("%s/%s: %w", recordDirectory, names[i], readErr)
		}
		parsed, parseErr := Parse(path.Join(recordDirectory, names[i]), data)
		if parseErr != nil {
			return nil, 0, parseErr
		}
		if len(parsed) > maxDecisionConstraints-len(constraints) {
			return nil, 0, fmt.Errorf("decision constraints exceed %d entries", maxDecisionConstraints)
		}
		constraints = append(constraints, parsed...)
	}
	return constraints, len(names), nil
}

func recordNames(root *os.Root) (_ []string, err error) {
	directory, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, directory.Close()) }()
	entries, err := directory.ReadDir(maxScanFiles + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > maxScanFiles {
		return nil, fmt.Errorf("decision record directory exceeds %d entries", maxScanFiles)
	}
	var names []string
	for i := 0; i < len(entries) && i < maxScanFiles; i++ {
		if !entries[i].IsDir() && strings.HasSuffix(entries[i].Name(), ".md") {
			if len(names) >= maxRecords {
				return nil, fmt.Errorf("decision record inventory exceeds %d entries", maxRecords)
			}
			names = append(names, entries[i].Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// readTracked reads one declared exclusion source through the confined reader, so a symlink
// pointing outside the repository cannot feed the scope check content it does not govern.
func readTracked(ctx context.Context, repoPath, relative string) (_ string, err error) {
	root, err := contextopt.OpenDirectory(ctx, repoPath)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	data, err := contextopt.ReadRootSnapshot(ctx, root, relative)
	if err != nil {
		return "", fmt.Errorf("%s: %w", relative, err)
	}
	return string(data), nil
}
