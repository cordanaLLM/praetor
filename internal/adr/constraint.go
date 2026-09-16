// Package adr makes an architectural decision checkable instead of merely recorded.
//
// An ADR states a decision in prose, and prose is only enforced by whoever reads it. An agent
// that reasons from memory instead of reading reintroduces exactly what the decision abolished,
// and nothing fails. This package lets an ADR carry the machine-checkable part of its decision
// so the repository is measured against it on every run.
package adr

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// maxConstraintsPerRecord bounds one record so a malformed document cannot make the
	// scan iterate without limit.
	maxConstraintsPerRecord = 32
	maxRecordBytes          = 1 << 20
	constraintFence         = "adr-constraint"
)

// constraintBlock matches the fenced block an ADR uses to carry its checkable clauses. The
// fence is labelled, like the receipt fence, so an example inside a code sample is not read
// as a declaration.
var constraintBlock = regexp.MustCompile("(?s)```" + constraintFence + "\\s*\n(.*?)\n```")

// Kind is the class of check a constraint asks for. A kind this package does not implement
// is an error rather than a skip: see Parse.
type Kind string

const (
	// KindUniversalScope declares that a gate covers every file it can reach, with no tier
	// exempted by origin. It is the machine-checkable half of a decision like "there is no
	// upstream-code tier, no GPU-code tier and no test-code tier".
	KindUniversalScope Kind = "universal-scope"
	// KindForbiddenPath declares that certain paths must not exist at all, which is how a
	// decision to remove a mechanism stays removed.
	KindForbiddenPath Kind = "forbidden-path"
)

// Constraint is one checkable clause of a decision record.
type Constraint struct {
	ID   string `yaml:"id"`
	Kind Kind   `yaml:"kind"`
	// Gate names the gate a universal-scope constraint governs, for the report.
	Gate string `yaml:"gate,omitempty"`
	// Forbids lists glob patterns that must not appear as an exclusion (universal-scope) or
	// must not exist as a path (forbidden-path).
	Forbids []string `yaml:"forbids"`
	// Rationale is required. A constraint without one is unreviewable a year later, and the
	// reason is the part a reader needs when the check fires.
	Rationale string `yaml:"rationale"`
	// Record is the file the constraint came from, filled in by Parse.
	Record string `yaml:"-"`
}

// Parse reads every constraint block in one decision record.
//
// An unrecognised kind is an error. A record that declares a check this engine cannot perform
// would otherwise pass silently, which is worse than declaring nothing: the decision would
// appear enforced while nothing measured it.
func Parse(record string, content []byte) ([]Constraint, error) {
	if len(content) > maxRecordBytes {
		return nil, fmt.Errorf("%s: decision record exceeds %d bytes", record, maxRecordBytes)
	}
	blocks := constraintBlock.FindAllSubmatch(content, maxConstraintsPerRecord+1)
	if len(blocks) > maxConstraintsPerRecord {
		return nil, fmt.Errorf("%s: more than %d constraint blocks", record, maxConstraintsPerRecord)
	}
	constraints := make([]Constraint, 0, len(blocks))
	for i := 0; i < len(blocks) && i < maxConstraintsPerRecord; i++ {
		var constraint Constraint
		if err := yaml.Unmarshal(blocks[i][1], &constraint); err != nil {
			return nil, fmt.Errorf("%s: constraint %d: %w", record, i+1, err)
		}
		constraint.Record = record
		if err := constraint.validate(); err != nil {
			return nil, fmt.Errorf("%s: %w", record, err)
		}
		constraints = append(constraints, constraint)
	}
	return constraints, nil
}

func (c Constraint) validate() error {
	if strings.TrimSpace(c.ID) == "" {
		return errors.New("constraint requires an id")
	}
	switch c.Kind {
	case KindUniversalScope, KindForbiddenPath:
	case "":
		return fmt.Errorf("constraint %q requires a kind", c.ID)
	default:
		return fmt.Errorf("constraint %q declares kind %q, which this engine cannot check; "+
			"a declared check that no code performs reports a decision as enforced while nothing measures it",
			c.ID, c.Kind)
	}
	if len(c.Forbids) == 0 {
		return fmt.Errorf("constraint %q forbids nothing, so it can never fail", c.ID)
	}
	if len(c.Forbids) > maxConstraintsPerRecord {
		return fmt.Errorf("constraint %q forbids more than %d patterns", c.ID, maxConstraintsPerRecord)
	}
	if strings.TrimSpace(c.Rationale) == "" {
		return fmt.Errorf("constraint %q requires a rationale; the reason is what a reader needs when it fires", c.ID)
	}
	return nil
}
