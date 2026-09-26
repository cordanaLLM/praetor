package paperclip

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/lockdown"
)

// DispositionStatus defines allowable terminal statuses for Paperclip runs.
type DispositionStatus string

const (
	StatusInReview DispositionStatus = "in_review"
	StatusBlocked  DispositionStatus = "blocked"
)

// Disposition represents an immutable Rule 0 terminal disposition record (ADR-0087).
// Receipt is the on-disk receipt envelope: the signed Exit-0 receipt plus the verbatim gate
// output whose SHA-256 it certifies, so the output binding travels with the disposition.
type Disposition struct {
	IssueID       string                `json:"issue_id"`
	Status        DispositionStatus     `json:"status"`
	Note          string                `json:"note"`
	Proof         string                `json:"proof,omitempty"`
	RecoveryOwner string                `json:"recovery_owner,omitempty"`
	Actor         string                `json:"actor"`
	Receipt       *lockdown.ReceiptFile `json:"receipt,omitempty"`
	Timestamp     time.Time             `json:"timestamp"`
}

// ParseStatus canonicalizes a disposition status: surrounding whitespace and letter case are
// ignored, and "done" maps to in_review because an agent's completion claim still needs
// review (ADR-0087). CreateDisposition and Validate both resolve status through it, so a value
// one accepts the other accepts too.
func ParseStatus(raw string) (DispositionStatus, error) {
	status := DispositionStatus(strings.ToLower(strings.TrimSpace(raw)))
	if status == "done" {
		return StatusInReview, nil
	}
	if status != StatusInReview && status != StatusBlocked {
		return "", fmt.Errorf("invalid disposition status %q: must be 'in_review' or 'blocked'", raw)
	}
	return status, nil
}

// CreateDisposition validates and synthesizes a structured terminal disposition.
func CreateDisposition(issueID, statusStr, note, proof, recoveryOwner, actor string, receipt *lockdown.ReceiptFile) (*Disposition, error) {
	status, err := ParseStatus(statusStr)
	if err != nil {
		return nil, err
	}
	if actor == "" {
		actor = "praetor/agent"
	}
	d := &Disposition{
		IssueID:       issueID,
		Status:        status,
		Note:          note,
		Proof:         proof,
		RecoveryOwner: recoveryOwner,
		Actor:         actor,
		Receipt:       receipt,
		Timestamp:     time.Now().UTC(),
	}
	if err := d.validateFields(status); err != nil {
		return nil, err
	}
	return d, nil
}

// Validate verifies the internal invariants of a disposition record. An attached receipt must
// be signed by pinned, the Ed25519 key pinned in the repository manifest (see
// lockdown.PinnedPublicKey), and must certify the envelope's gate output; the key embedded in
// the receipt is never a trust anchor on its own. pinned may be nil only when no receipt is
// attached. Validate has no repository to compare against, so it does not bind the receipt to a
// commit; VerifyRun does, and a caller that trusts a receipt must go through VerifyRun.
func (d *Disposition) Validate(ctx context.Context, pinned ed25519.PublicKey) error {
	_, err := d.validate(ctx, pinned)
	return err
}

// validate runs Validate and returns the canonical status so callers branch on the same
// value that passed validation, never on the raw field.
func (d *Disposition) validate(ctx context.Context, pinned ed25519.PublicKey) (DispositionStatus, error) {
	if d == nil {
		return "", fmt.Errorf("disposition cannot be nil")
	}
	if ctx == nil {
		return "", fmt.Errorf("validation: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("validation cancelled: %w", err)
	}

	status, err := ParseStatus(string(d.Status))
	if err != nil {
		return "", err
	}
	if err := d.validateFields(status); err != nil {
		return "", err
	}
	if err := d.verifyReceipt(pinned); err != nil {
		return "", err
	}
	return status, nil
}

// verifyReceipt checks an attached receipt against the pinned key and its gate output.
func (d *Disposition) verifyReceipt(pinned ed25519.PublicKey) error {
	if d.Receipt == nil {
		return nil
	}
	if len(pinned) == 0 {
		return fmt.Errorf("cannot verify the exit-0 receipt attached to disposition: %w", lockdown.ErrNoPinnedKey)
	}
	if err := lockdown.VerifyPinnedReceipt(&d.Receipt.ExecutionReceipt, pinned, []byte(d.Receipt.GateOutput)); err != nil {
		return fmt.Errorf("invalid exit-0 receipt attached to disposition: %w", err)
	}
	return nil
}

// validateFields checks the required text fields for status; whitespace-only values count
// as empty.
func (d *Disposition) validateFields(status DispositionStatus) error {
	if strings.TrimSpace(d.IssueID) == "" {
		return fmt.Errorf("disposition: issue_id cannot be empty")
	}
	if strings.TrimSpace(d.Note) == "" {
		return fmt.Errorf("disposition: note cannot be empty")
	}
	if status == StatusInReview && strings.TrimSpace(d.Proof) == "" {
		return fmt.Errorf("disposition: proof is required when status is 'in_review'")
	}
	if status == StatusBlocked && strings.TrimSpace(d.RecoveryOwner) == "" {
		return fmt.Errorf("disposition: recovery_owner is required when status is 'blocked'")
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
