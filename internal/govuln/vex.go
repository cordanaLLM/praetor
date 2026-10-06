// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package govuln

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"time"

	"github.com/cordanaLLM/praetor/internal/strictjson"
	"github.com/cordanaLLM/praetor/internal/util"
)

// OpenVEXContext is the @context of the OpenVEX version the gate reads, v0.2.0
// (https://github.com/openvex/spec/blob/main/OPENVEX-SPEC.md).
const OpenVEXContext = "https://openvex.dev/ns/v0.2.0"

// The bounds of one OpenVEX document (HISS-02).
const (
	maxVEXBytes      = 1 << 20
	maxVEXDepth      = 8
	maxStatements    = 1024
	maxProducts      = 256
	maxVEXClockSkew  = 5 * time.Minute
	statusNotAffect  = "not_affected"
	reviewDateLayout = "2006-01-02"
)

// The status labels and the not_affected justification labels OpenVEX v0.2.0 defines.
var (
	vexStatuses       = []string{statusNotAffect, "affected", "fixed", "under_investigation"}
	vexJustifications = []string{
		"component_not_present", "vulnerable_code_not_present", "vulnerable_code_not_in_execute_path",
		"vulnerable_code_cannot_be_controlled_by_adversary", "inline_mitigations_already_exist",
	}
	// absentJustifications claim the vulnerable code is not in the build at all. govulncheck
	// reporting a package of the advisory as imported proves them false, so they cover a
	// module-level finding only.
	absentJustifications = []string{"component_not_present", "vulnerable_code_not_present"}
)

// vexDocument is an OpenVEX v0.2.0 document: every field the specification defines, so the strict
// decode refuses a misspelled one instead of dropping it.
type vexDocument struct {
	Context     string          `json:"@context"`
	ID          string          `json:"@id"`
	Author      string          `json:"author"`
	Role        string          `json:"role,omitempty"`
	Timestamp   string          `json:"timestamp"`
	LastUpdated string          `json:"last_updated,omitempty"`
	Version     int             `json:"version"`
	Tooling     string          `json:"tooling,omitempty"`
	Statements  *[]vexStatement `json:"statements"`
}

type vexStatement struct {
	ID                       string           `json:"@id,omitempty"`
	Version                  int              `json:"version,omitempty"`
	Vulnerability            vexVulnerability `json:"vulnerability"`
	Timestamp                string           `json:"timestamp,omitempty"`
	LastUpdated              string           `json:"last_updated,omitempty"`
	Products                 []vexProduct     `json:"products,omitempty"`
	Status                   string           `json:"status"`
	Supplier                 string           `json:"supplier,omitempty"`
	StatusNotes              string           `json:"status_notes,omitempty"`
	Justification            string           `json:"justification,omitempty"`
	ImpactStatement          string           `json:"impact_statement,omitempty"`
	ActionStatement          string           `json:"action_statement,omitempty"`
	ActionStatementTimestamp string           `json:"action_statement_timestamp,omitempty"`
}

type vexVulnerability struct {
	ID          string   `json:"@id,omitempty"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Aliases     []string `json:"aliases,omitempty"`
}

type vexComponent struct {
	ID          string            `json:"@id,omitempty"`
	Identifiers map[string]string `json:"identifiers,omitempty"`
	Hashes      map[string]string `json:"hashes,omitempty"`
}

type vexProduct struct {
	vexComponent
	Subcomponents []vexComponent `json:"subcomponents,omitempty"`
}

// statement is one validated statement as the gate uses it.
type statement struct {
	index         int
	names         []string // the vulnerability's name, then its aliases
	status        string
	justification string
	reviewed      time.Time
}

// vexIndex is the repository's OpenVEX document as the gate reads it. found is false when no
// file exists at path, which covers nothing.
type vexIndex struct {
	path       string
	found      bool
	statements []statement
}

// loadVEX reads the document at the repository path rel inside dir. A missing file is an empty
// index; any other read failure and any document that does not validate is ErrInvalidVEX.
func loadVEX(dir, rel string, now time.Time) (*vexIndex, error) {
	data, err := util.ReadConfinedLimited(dir, filepath.FromSlash(rel), maxVEXBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return &vexIndex{path: rel}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("%w: read %s: %w", ErrInvalidVEX, rel, err)
	}
	index, err := parseVEX(rel, data, now)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrInvalidVEX, rel, err)
	}
	return index, nil
}

// parseVEX decodes and validates one document.
func parseVEX(rel string, data []byte, now time.Time) (*vexIndex, error) {
	var doc vexDocument
	if err := strictjson.Decode(data, &doc, strictjson.Options{MaxBytes: maxVEXBytes, MaxDepth: maxVEXDepth, RejectNull: true}); err != nil {
		return nil, err
	}
	reviewed, err := doc.validate(now)
	if err != nil {
		return nil, err
	}
	index := &vexIndex{path: rel, found: true, statements: make([]statement, 0, len(*doc.Statements))}
	for i := 0; i < len(*doc.Statements) && i < maxStatements; i++ {
		st, err := (*doc.Statements)[i].parse(i, reviewed, now)
		if err != nil {
			return nil, err
		}
		index.statements = append(index.statements, st)
	}
	return index, nil
}

// validate checks the document fields OpenVEX requires and returns the document's review time:
// last_updated when set, else timestamp.
func (d *vexDocument) validate(now time.Time) (time.Time, error) {
	switch {
	case d.Context != OpenVEXContext:
		return time.Time{}, fmt.Errorf("@context is %q; the gate reads OpenVEX %s", d.Context, OpenVEXContext)
	case d.ID == "" || d.Author == "":
		return time.Time{}, errors.New("@id and author are required")
	case d.Version < 1:
		return time.Time{}, errors.New("version must be a positive integer")
	case d.Statements == nil:
		return time.Time{}, errors.New("statements is required")
	case len(*d.Statements) > maxStatements:
		return time.Time{}, fmt.Errorf("%d statements; at most %d allowed", len(*d.Statements), maxStatements)
	}
	issued, err := vexTime("timestamp", d.Timestamp, true, now)
	if err != nil {
		return time.Time{}, err
	}
	updated, err := vexTime("last_updated", d.LastUpdated, false, now)
	if err != nil || updated.IsZero() {
		return issued, err
	}
	return updated, nil
}

// parse validates statement i and resolves its review time: its own last_updated, else its own
// timestamp, else the document's (inherited) review time. OpenVEX lets a not_affected statement
// carry a justification or an impact statement; the gate requires both, a label it can check
// against the finding and the reason a reviewer reads.
func (s *vexStatement) parse(i int, inherited, now time.Time) (statement, error) {
	label := fmt.Sprintf("statements[%d] (%s)", i, s.Vulnerability.Name)
	if err := s.validateFields(); err != nil {
		return statement{}, fmt.Errorf("%s: %w", label, err)
	}
	reviewed, err := s.reviewTime(inherited, now)
	if err != nil {
		return statement{}, fmt.Errorf("%s: %w", label, err)
	}
	names := append([]string{s.Vulnerability.Name}, s.Vulnerability.Aliases...)
	return statement{index: i, names: names, status: s.Status, justification: s.Justification, reviewed: reviewed}, nil
}

// validateFields checks the statement's labels, names and bounds.
func (s *vexStatement) validateFields() error {
	switch {
	case s.Vulnerability.Name == "":
		return errors.New("vulnerability.name is required")
	case len(s.Vulnerability.Aliases) > maxAliases || slices.Contains(s.Vulnerability.Aliases, ""):
		return fmt.Errorf("vulnerability.aliases must hold at most %d nonempty names", maxAliases)
	case len(s.Products) > maxProducts:
		return fmt.Errorf("%d products; at most %d allowed", len(s.Products), maxProducts)
	case !slices.Contains(vexStatuses, s.Status):
		return fmt.Errorf("status %q is not one of %v", s.Status, vexStatuses)
	case s.Justification != "" && !slices.Contains(vexJustifications, s.Justification):
		return fmt.Errorf("justification %q is not one of %v", s.Justification, vexJustifications)
	case s.Status == statusNotAffect && (s.Justification == "" || s.ImpactStatement == ""):
		return errors.New("a not_affected statement needs a justification and an impact_statement")
	}
	return nil
}

// reviewTime checks the statement's timestamps and returns the one its review is dated by.
func (s *vexStatement) reviewTime(inherited, now time.Time) (time.Time, error) {
	if _, err := vexTime("action_statement_timestamp", s.ActionStatementTimestamp, false, now); err != nil {
		return time.Time{}, err
	}
	issued, err := vexTime("timestamp", s.Timestamp, false, now)
	if err != nil {
		return time.Time{}, err
	}
	updated, err := vexTime("last_updated", s.LastUpdated, false, now)
	switch {
	case err != nil:
		return time.Time{}, err
	case !updated.IsZero():
		return updated, nil
	case !issued.IsZero():
		return issued, nil
	}
	return inherited, nil
}

// vexTime parses one RFC 3339 timestamp. An absent optional one is the zero time; a timestamp
// later than now (beyond a clock skew) is refused, since it would extend its statement's review
// past MaxStatementAge.
func vexTime(field, value string, required bool, now time.Time) (time.Time, error) {
	if value == "" {
		if required {
			return time.Time{}, fmt.Errorf("%s is required", field)
		}
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s %q is no RFC 3339 timestamp", field, value)
	}
	if parsed.After(now.Add(maxVEXClockSkew)) {
		return time.Time{}, fmt.Errorf("%s %s lies in the future", field, value)
	}
	return parsed, nil
}

// latest returns the statement that speaks for an advisory known by names: of every statement
// naming one of them, the one reviewed last. tie is set when two statements share that latest
// review time, which leaves the advisory without one answer.
func (v *vexIndex) latest(names []string) (best *statement, tie bool) {
	for i := 0; i < len(v.statements); i++ {
		candidate := &v.statements[i]
		if !namesOverlap(candidate.names, names) {
			continue
		}
		switch {
		case best == nil || candidate.reviewed.After(best.reviewed):
			best, tie = candidate, false
		case candidate.reviewed.Equal(best.reviewed):
			tie = true
		}
	}
	return best, tie
}

// namesOverlap reports whether a and b share a name.
func namesOverlap(a, b []string) bool {
	return slices.ContainsFunc(a, func(name string) bool { return slices.Contains(b, name) })
}
