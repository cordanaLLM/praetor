package paperclip

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cordanallm/praetor/internal/lockdown"
)

// DispositionStatus defines allowable terminal statuses for Paperclip runs.
type DispositionStatus string

const (
	StatusInReview DispositionStatus = "in_review"
	StatusBlocked  DispositionStatus = "blocked"
)

// Disposition represents an immutable Rule 0 terminal disposition record (ADR-0087).
type Disposition struct {
	IssueID       string                     `json:"issue_id"`
	Status        DispositionStatus          `json:"status"`
	Note          string                     `json:"note"`
	Proof         string                     `json:"proof,omitempty"`
	RecoveryOwner string                     `json:"recovery_owner,omitempty"`
	Actor         string                     `json:"actor"`
	Receipt       *lockdown.ExecutionReceipt `json:"receipt,omitempty"`
	Timestamp     time.Time                  `json:"timestamp"`
}

// CreateDisposition validates and synthesizes a structured terminal disposition.
func CreateDisposition(issueID, statusStr, note, proof, recoveryOwner, actor string, receipt *lockdown.ExecutionReceipt) (*Disposition, error) {
	status := DispositionStatus(strings.ToLower(strings.TrimSpace(statusStr)))

	// Invariant: Server never accepts "done" directly from an agent (review is mandatory)
	if status == "done" {
		status = StatusInReview
	}

	if status != StatusInReview && status != StatusBlocked {
		return nil, fmt.Errorf("invalid disposition status %q: must be 'in_review' or 'blocked'", statusStr)
	}

	if strings.TrimSpace(issueID) == "" {
		return nil, fmt.Errorf("disposition: issue_id cannot be empty")
	}
	if strings.TrimSpace(note) == "" {
		return nil, fmt.Errorf("disposition: note cannot be empty")
	}
	if status == StatusInReview && strings.TrimSpace(proof) == "" {
		return nil, fmt.Errorf("disposition: proof is required when status is 'in_review'")
	}
	if status == StatusBlocked && strings.TrimSpace(recoveryOwner) == "" {
		return nil, fmt.Errorf("disposition: recovery_owner is required when status is 'blocked'")
	}

	if actor == "" {
		actor = "praetor/agent"
	}

	return &Disposition{
		IssueID:       issueID,
		Status:        status,
		Note:          note,
		Proof:         proof,
		RecoveryOwner: recoveryOwner,
		Actor:         actor,
		Receipt:       receipt,
		Timestamp:     time.Now().UTC(),
	}, nil
}

// Validate verifies the internal invariants of a disposition record.
func (d *Disposition) Validate(ctx context.Context) error {
	if d == nil {
		return fmt.Errorf("disposition cannot be nil")
	}
	if ctx == nil {
		return fmt.Errorf("validation: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("validation cancelled: %w", err)
	}

	if d.Status != StatusInReview && d.Status != StatusBlocked {
		return fmt.Errorf("invalid status %q", d.Status)
	}
	if d.IssueID == "" {
		return fmt.Errorf("missing issue_id")
	}
	if d.Note == "" {
		return fmt.Errorf("missing note")
	}
	if d.Status == StatusInReview && d.Proof == "" {
		return fmt.Errorf("missing proof for in_review")
	}
	if d.Status == StatusBlocked && d.RecoveryOwner == "" {
		return fmt.Errorf("missing recovery_owner for blocked")
	}

	if d.Receipt != nil {
		if err := lockdown.VerifyReceipt(d.Receipt); err != nil {
			return fmt.Errorf("invalid exit-0 receipt attached to disposition: %w", err)
		}
	}

	return nil
}

// FormatJSON marshals the disposition to indented JSON bytes.
func (d *Disposition) FormatJSON() ([]byte, error) {
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal disposition: %w", err)
	}
	return append(data, '\n'), nil
}
