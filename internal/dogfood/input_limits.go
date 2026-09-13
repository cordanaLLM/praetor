package dogfood

import (
	"encoding/json"

	"github.com/cordanaLLM/praetor/internal/adopt"
)

// InputLimits binds observation budgets to the selected suite or discovery policy.
// They govern input completeness, not the repository's compliance thresholds.
type InputLimits struct {
	Snapshot     SnapshotLimits           `json:"snapshot"`
	Verification adopt.VerificationLimits `json:"verification"`
}

func normalizeInputLimits(input *InputLimits) (*InputLimits, error) {
	var snapshot *SnapshotLimits
	var verification *adopt.VerificationLimits
	if input != nil {
		snapshot, verification = &input.Snapshot, &input.Verification
	}
	resolvedSnapshot, err := NormalizeSnapshotLimits(snapshot)
	if err != nil {
		return nil, err
	}
	resolvedVerification, err := adopt.NormalizeVerificationLimits(verification)
	if err != nil {
		return nil, err
	}
	return &InputLimits{Snapshot: resolvedSnapshot, Verification: resolvedVerification}, nil
}

func decodeInputLimits(data json.RawMessage) (*InputLimits, error) {
	if data == nil {
		return nil, nil
	}
	fields, err := suiteObject(data, []string{"snapshot", "verification"})
	if err != nil {
		return nil, err
	}
	if _, err := suiteObject(fields["snapshot"], []string{"max_entries", "max_file_bytes", "max_tree_bytes"}); err != nil {
		return nil, err
	}
	if _, err := suiteObject(fields["verification"], []string{"max_entries", "max_files", "max_depth", "max_file_bytes", "max_total_bytes"}); err != nil {
		return nil, err
	}
	var limits InputLimits
	if err := json.Unmarshal(data, &limits); err != nil {
		return nil, err
	}
	return normalizeInputLimits(&limits)
}
