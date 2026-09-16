// Package bugledger reads the repository's bug ledger and checks the parts of a row that a
// machine can check.
//
// A row records where a defect lives. Nothing has ever re-read that location, so a row keeps
// pointing at a line that moved, or at a file that shrank past it, and stays "open" long after
// the defect is gone. Measured on this repository: of the first forty open rows carrying a Go
// file:line, five pointed beyond the end of their file, and every one of five sampled p1 rows
// was already fixed (#158).
//
// This package decides only what is decidable without semantics: whether the recorded location
// still resolves. It does not claim a row is fixed, because a location that still exists says
// nothing about whether the defect does.
package bugledger

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	// maxLedgerRows bounds the scan (HISS-02).
	maxLedgerRows = 4096
	// maxLedgerBytes bounds the read.
	maxLedgerBytes = 8 << 20
)

// rowPattern matches a ledger row and captures id, severity, status and location.
var rowPattern = regexp.MustCompile(`^\|\s*` + "`" + `(BUG-\d+)` + "`" + `\s*\|(.*)$`)

// locationPattern matches a "path/file.go:123" location.
var locationPattern = regexp.MustCompile(`^([^\s:]+\.go):(\d+)$`)

// Row is one ledger entry.
type Row struct {
	ID       string
	Title    string
	Severity string
	Status   string
	Location string
}

// Finding is one row whose recorded location no longer resolves.
type Finding struct {
	Row    Row
	Detail string
}

func (f Finding) String() string {
	return fmt.Sprintf("%s [%s/%s] %s: %s", f.Row.ID, f.Row.Severity, f.Row.Status, f.Row.Location, f.Detail)
}

// Parse reads every row of a ledger.
func Parse(data []byte) ([]Row, error) {
	if len(data) > maxLedgerBytes {
		return nil, fmt.Errorf("bug ledger exceeds %d bytes", maxLedgerBytes)
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), maxLedgerBytes)
	var rows []Row
	for scanner.Scan() {
		if len(rows) >= maxLedgerRows {
			return nil, fmt.Errorf("bug ledger exceeds %d rows", maxLedgerRows)
		}
		match := rowPattern.FindStringSubmatch(scanner.Text())
		if match == nil {
			continue
		}
		row, ok := parseRow(match[1], match[2])
		if ok {
			rows = append(rows, row)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read bug ledger: %w", err)
	}
	return rows, nil
}

// parseRow splits the remaining cells of one row. A row too short to carry a location is
// skipped rather than guessed at.
func parseRow(id, rest string) (Row, bool) {
	cells := strings.Split(rest, "|")
	if len(cells) < 4 {
		return Row{}, false
	}
	return Row{
		ID:       id,
		Title:    strings.TrimSpace(cells[0]),
		Severity: strings.TrimSpace(cells[1]),
		Status:   strings.TrimSpace(cells[2]),
		Location: strings.TrimSpace(cells[3]),
	}, true
}

// FileLine splits a row's location into a path and a line number. It reports false when the
// location is not a Go file:line, which many rows legitimately are not: "core" and free text
// are used for defects that are not at one place.
func (r Row) FileLine() (string, int, bool) {
	match := locationPattern.FindStringSubmatch(r.Location)
	if match == nil {
		return "", 0, false
	}
	line, err := strconv.Atoi(match[2])
	if err != nil || line <= 0 {
		return "", 0, false
	}
	return match[1], line, true
}

// Open reports whether the row is still marked open.
func (r Row) Open() bool { return strings.EqualFold(r.Status, "open") }

// ErrNoRows reports a ledger that parsed to nothing, which means the format changed and the
// audit is reading no rows rather than finding no problems.
var ErrNoRows = errors.New("bug ledger parsed to zero rows")
