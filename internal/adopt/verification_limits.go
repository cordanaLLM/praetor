package adopt

import (
	"errors"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// VerificationLimits bounds metadata discovery during adoption. A nil value
// selects Praetor's conservative defaults; explicit values are still capped.
type VerificationLimits struct {
	MaxEntries    int   `json:"max_entries"`
	MaxFiles      int   `json:"max_files"`
	MaxDepth      int   `json:"max_depth"`
	MaxFileBytes  int64 `json:"max_file_bytes"`
	MaxTotalBytes int64 `json:"max_total_bytes"`
}

const (
	maxVerificationEntriesCeiling = 200000
	maxVerificationFilesCeiling   = 512
	maxVerificationDepthCeiling   = 64
	maxVerificationTotalCeiling   = 16 << 20
)

// NormalizeVerificationLimits validates and fills the bounded reader policy.
func NormalizeVerificationLimits(input *VerificationLimits) (VerificationLimits, error) {
	defaults := VerificationLimits{MaxEntries: maxVerificationEntries, MaxFiles: maxVerificationInputs, MaxDepth: maxVerificationDepth, MaxFileBytes: maxVerificationInputBytes, MaxTotalBytes: maxVerificationTotalBytes}
	if input == nil {
		return defaults, nil
	}
	for _, bound := range [...]struct {
		name           string
		value, ceiling int64
	}{
		{"max_entries", int64(input.MaxEntries), maxVerificationEntriesCeiling},
		{"max_files", int64(input.MaxFiles), maxVerificationFilesCeiling},
		{"max_depth", int64(input.MaxDepth), maxVerificationDepthCeiling},
		{"max_file_bytes", input.MaxFileBytes, contextopt.MaxSourceBytes},
		{"max_total_bytes", input.MaxTotalBytes, maxVerificationTotalCeiling},
	} {
		if bound.value <= 0 || bound.value > bound.ceiling {
			return VerificationLimits{}, fmt.Errorf("verification %s must be 1..%d", bound.name, bound.ceiling)
		}
	}
	if input.MaxFileBytes > input.MaxTotalBytes {
		return VerificationLimits{}, errors.New("verification max_file_bytes cannot exceed max_total_bytes")
	}
	return *input, nil
}
