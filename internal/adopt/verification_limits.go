package adopt

import (
	"errors"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
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
	maxVerificationEntriesCeiling = util.DiscoveryEntriesCeiling
	maxVerificationFilesCeiling   = 512
	maxVerificationDepthCeiling   = 64
	maxVerificationTotalCeiling   = 16 << 20
)

// Ceilings an operator-raised discovery bound may not exceed.
const (
	VerificationEntriesCeiling = maxVerificationEntriesCeiling
	VerificationFilesCeiling   = maxVerificationFilesCeiling
	VerificationDepthCeiling   = maxVerificationDepthCeiling
)

// Flags that raise the discovery bounds. `praetorctl adopt` and `praetorctl paperclip harness`
// register them under these names, and a walk that stops at a bound names the one that raises it.
const (
	VerificationEntriesFlag = "verification-max-entries"
	VerificationFilesFlag   = "verification-max-files"
	VerificationDepthFlag   = "verification-max-depth"
)

// discoveryBound is one operator-raisable discovery bound: its VerificationLimits field, which is
// also the key a dogfood suite's input_limits.verification carries, the flag that raises it on the
// command line, and the ceiling both accept.
type discoveryBound struct {
	field   string
	flag    string
	ceiling int
}

var (
	entriesBound = discoveryBound{field: "max_entries", flag: VerificationEntriesFlag, ceiling: maxVerificationEntriesCeiling}
	filesBound   = discoveryBound{field: "max_files", flag: VerificationFilesFlag, ceiling: maxVerificationFilesCeiling}
	depthBound   = discoveryBound{field: "max_depth", flag: VerificationDepthFlag, ceiling: maxVerificationDepthCeiling}
)

// exceeded wraps cause, a walk that stopped at limit, with the field and flag that raise the bound,
// or says the bound already sits at its ceiling, where no value raises it further (issue #535).
func (b discoveryBound) exceeded(limit int, cause error) error {
	if limit >= b.ceiling {
		return fmt.Errorf("%w; %s is already at its ceiling %d", cause, b.field, b.ceiling)
	}
	return fmt.Errorf("%w; raise %s with --%s, up to %d", cause, b.field, b.flag, b.ceiling)
}

// DefaultVerificationLimits is the bounded reader policy adoption applies when the caller
// declares none. A caller raising one bound keeps the others at these defaults.
func DefaultVerificationLimits() VerificationLimits {
	return VerificationLimits{MaxEntries: maxVerificationEntries, MaxFiles: maxVerificationInputs, MaxDepth: maxVerificationDepth, MaxFileBytes: maxVerificationInputBytes, MaxTotalBytes: maxVerificationTotalBytes}
}

// NormalizeVerificationLimits validates and fills the bounded reader policy. A value past its
// ceiling is refused, never clamped, and the refusal names the flag that sets it.
func NormalizeVerificationLimits(input *VerificationLimits) (VerificationLimits, error) {
	defaults := DefaultVerificationLimits()
	if input == nil {
		return defaults, nil
	}
	for _, bound := range [...]struct {
		name, flag     string
		value, ceiling int64
	}{
		{entriesBound.field, entriesBound.flag, int64(input.MaxEntries), maxVerificationEntriesCeiling},
		{filesBound.field, filesBound.flag, int64(input.MaxFiles), maxVerificationFilesCeiling},
		{depthBound.field, depthBound.flag, int64(input.MaxDepth), maxVerificationDepthCeiling},
		{"max_file_bytes", "", input.MaxFileBytes, contextopt.MaxSourceBytes},
		{"max_total_bytes", "", input.MaxTotalBytes, maxVerificationTotalCeiling},
	} {
		if bound.value > 0 && bound.value <= bound.ceiling {
			continue
		}
		if bound.flag != "" {
			return VerificationLimits{}, fmt.Errorf("verification %s (--%s) must be 1..%d", bound.name, bound.flag, bound.ceiling)
		}
		return VerificationLimits{}, fmt.Errorf("verification %s must be 1..%d", bound.name, bound.ceiling)
	}
	if input.MaxFileBytes > input.MaxTotalBytes {
		return VerificationLimits{}, errors.New("verification max_file_bytes cannot exceed max_total_bytes")
	}
	return *input, nil
}
