// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// WorkflowRunState is a state a repository declares one of its workflows to be in on
// purpose, so the audit's workflow run check lists it without counting it (#612).
type WorkflowRunState string

const (
	// WorkflowRunFailing declares that the workflow's latest runs on the default branch fail
	// and are meant to, such as a build that refuses a step not implemented yet.
	WorkflowRunFailing WorkflowRunState = "failing"
	// WorkflowRunUnexercised declares that the workflow has never run and that this is
	// expected, such as a release workflow no tag has started yet.
	WorkflowRunUnexercised WorkflowRunState = "unexercised"
	// MaxWorkflowRunExpectations bounds workflow_runs.expected (HISS-02). It equals the
	// workflow inventory bound of internal/forge, so every workflow can be declared.
	MaxWorkflowRunExpectations = 64
	// maxWorkflowRunReason bounds one declared reason, in bytes.
	maxWorkflowRunReason = 500
)

// WorkflowRunsPolicy is the manifest's workflow_runs section: the workflows whose state the
// audit's run check (internal/forge.AuditWorkflowRunHealth) is told to expect, each with the
// reason and expiry a reader of the report needs. It is repository-only and stays out of
// ResolvedPolicy, like HISS: only the repository knows why one of its workflows fails or has not
// run yet.
type WorkflowRunsPolicy struct {
	Expected []WorkflowRunExpectation `yaml:"expected,omitempty"`
}

// WorkflowRunExpectation declares one workflow's known state. A declaration covers exactly
// the state it names: a workflow declared unexercised that runs and fails, or one declared
// failing that has never run, is still reported, so a declaration cannot hide a different
// fault.
type WorkflowRunExpectation struct {
	// Workflow is the workflow's file name under .github/workflows, such as build.yml.
	Workflow string `yaml:"workflow"`
	// State is WorkflowRunFailing or WorkflowRunUnexercised.
	State WorkflowRunState `yaml:"state"`
	// Reason says why, in one line of at most 500 bytes.
	Reason string `yaml:"reason"`
	// Expires is the date after which the declaration stops holding (YYYY-MM-DD); required
	// for failing and optional for unexercised.
	Expires string `yaml:"expires,omitempty"`
}

// Expired reports whether the declaration stopped holding before today; it still holds on its
// expires day. A declaration without an expires date (permitted for unexercised workflows)
// never expires. An unparseable date counts as expired.
func (e WorkflowRunExpectation) Expired(today time.Time) bool {
	if e.Expires == "" {
		return e.State == WorkflowRunFailing
	}
	expires, err := time.Parse(ExceptionDateLayout, e.Expires)
	return err != nil || expires.Before(ExceptionDay(today))
}

// Expectation returns the declaration for the workflow file name workflow, or nil when the
// section declares none. It is safe on a nil section.
func (p *WorkflowRunsPolicy) Expectation(workflow string) *WorkflowRunExpectation {
	if p == nil {
		return nil
	}
	for i := 0; i < len(p.Expected) && i < MaxWorkflowRunExpectations; i++ {
		if p.Expected[i].Workflow == workflow {
			return &p.Expected[i]
		}
	}
	return nil
}

// validate refuses more than MaxWorkflowRunExpectations declarations, a workflow that is not a
// bare .yml or .yaml file name, a workflow declared twice, an unknown state, a reason that
// is empty, longer than 500 bytes or spans lines, and an expires value that is missing on state
// failing, not a YYYY-MM-DD date, or more than MaxExceptionDays ahead. A nil section declares nothing.
func (p *WorkflowRunsPolicy) validate() error {
	return p.validateToday(time.Now())
}

func (p *WorkflowRunsPolicy) validateToday(today time.Time) error {
	if p == nil {
		return nil
	}
	if len(p.Expected) > MaxWorkflowRunExpectations {
		return fmt.Errorf("workflow_runs.expected declares %d workflows, at most %d allowed",
			len(p.Expected), MaxWorkflowRunExpectations)
	}
	seen := make(map[string]bool, len(p.Expected))
	for i := 0; i < len(p.Expected) && i < MaxWorkflowRunExpectations; i++ {
		entry := p.Expected[i]
		if err := entry.validateToday(today); err != nil {
			return fmt.Errorf("workflow_runs.expected[%d]: %w", i, err)
		}
		if seen[entry.Workflow] {
			return fmt.Errorf("workflow_runs.expected declares %s twice", entry.Workflow)
		}
		seen[entry.Workflow] = true
	}
	return nil
}

func (e WorkflowRunExpectation) validateToday(today time.Time) error {
	if !validWorkflowFileName(e.Workflow) {
		return fmt.Errorf("workflow %q must be the file name of a .yml or .yaml workflow under .github/workflows", e.Workflow)
	}
	if e.State != WorkflowRunFailing && e.State != WorkflowRunUnexercised {
		return fmt.Errorf("state %q of %s must be %q or %q", e.State, e.Workflow, WorkflowRunFailing, WorkflowRunUnexercised)
	}
	if err := e.validateReason(); err != nil {
		return err
	}
	return e.validateExpiry(today)
}

func (e WorkflowRunExpectation) validateReason() error {
	reason := strings.TrimSpace(e.Reason)
	switch {
	case reason == "":
		return fmt.Errorf("%s declares no reason; the report shows it in place of the finding", e.Workflow)
	case len(e.Reason) > maxWorkflowRunReason || !utf8.ValidString(e.Reason):
		return fmt.Errorf("the reason of %s must be valid UTF-8 of at most %d bytes", e.Workflow, maxWorkflowRunReason)
	case strings.ContainsAny(e.Reason, "\r\n"):
		return fmt.Errorf("the reason of %s must be one line", e.Workflow)
	}
	return nil
}

func (e WorkflowRunExpectation) validateExpiry(today time.Time) error {
	if e.State == WorkflowRunFailing && strings.TrimSpace(e.Expires) == "" {
		return fmt.Errorf("%s declares state %q with no expires; failing workflows require an expires date", e.Workflow, e.State)
	}
	if e.Expires != "" {
		if problem := exceptionExpiryProblem(e.Expires, today); problem != "" {
			return fmt.Errorf("%s %s", e.Workflow, problem)
		}
	}
	return nil
}

// validWorkflowFileName reports whether name is a bare workflow file name: no directory, and
// a .yml or .yaml extension after a nonempty stem.
func validWorkflowFileName(name string) bool {
	stem, ok := strings.CutSuffix(name, ".yml")
	if !ok {
		stem, ok = strings.CutSuffix(name, ".yaml")
	}
	return ok && stem != "" && !strings.ContainsAny(name, "/\\ \t\r\n") && !strings.HasPrefix(name, ".")
}

// validateManifestActions refuses an overrides.actions block whose default_workflow_permissions
// is not "read" or "write". The live check compares the value with the forge's, which is always
// one of the two, so an omitted or misspelled value would report drift on every repository that
// declares the block rather than the configuration error it is.
func validateManifestActions(m *Manifest) error {
	if m == nil {
		return nil
	}
	if actions := m.Overrides.Actions; actions != nil {
		switch actions.DefaultWorkflowPermissions {
		case "read", "write":
		default:
			return fmt.Errorf("overrides.actions.default_workflow_permissions %q must be \"read\" or \"write\"",
				actions.DefaultWorkflowPermissions)
		}
	}
	return m.WorkflowRuns.validate()
}
