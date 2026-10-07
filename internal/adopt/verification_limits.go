package adopt

import (
	"errors"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// VerificationLimits bounds metadata discovery during adoption. A nil value
// selects Praetor's conservative defaults; explicit values are still capped. As the bounds a run
// raises (ResolveVerificationLimits), a zero field raises none.
type VerificationLimits struct {
	MaxEntries    int   `json:"max_entries"`
	MaxFiles      int   `json:"max_files"`
	MaxDepth      int   `json:"max_depth"`
	MaxFileBytes  int64 `json:"max_file_bytes"`
	MaxTotalBytes int64 `json:"max_total_bytes"`
}

const (
	maxVerificationEntriesCeiling = util.DiscoveryEntriesCeiling
	maxVerificationFilesCeiling   = util.DiscoveryFilesCeiling
	maxVerificationDepthCeiling   = util.DiscoveryDepthCeiling
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

// exceeded wraps cause, a walk that stopped at limit, with the flag that raises the bound for one
// run and the manifest key that raises it for every run, the audit's hooks and CI jobs included
// (config.VerificationPolicy), or says the bound already sits at its ceiling, where no value
// raises it further (issue #535).
func (b discoveryBound) exceeded(limit int, cause error) error {
	if limit >= b.ceiling {
		return fmt.Errorf("%w; %s is already at its ceiling %d", cause, b.field, b.ceiling)
	}
	return fmt.Errorf("%w; raise %s with --%s, up to %d, or declare verification.%s in %s so every run, "+
		"the audit's hooks and CI included, reads it", cause, b.field, b.flag, b.ceiling, b.field, manifestFile)
}

// ResolveVerificationLimits returns the discovery bounds a walk of a repository runs under:
// requested, the bounds this run raises (its --verification-max-* flags; a zero field raises
// none), over declared, the manifest's verification section (config.VerificationPolicy), over
// DefaultVerificationLimits. Each bound is resolved on its own, so a flag raising the depth keeps
// the entry bound the manifest declares. Nil when neither raises a bound, which keeps the
// defaults. NormalizeVerificationLimits still validates the result against the ceilings.
func ResolveVerificationLimits(declared *config.VerificationPolicy, requested *VerificationLimits) *VerificationLimits {
	if declared == nil && requested == nil {
		return nil
	}
	limits := DefaultVerificationLimits()
	if declared != nil {
		limits = limits.raisedBy(VerificationLimits{MaxEntries: declared.MaxEntries, MaxFiles: declared.MaxFiles, MaxDepth: declared.MaxDepth})
	}
	if requested != nil {
		limits = limits.raisedBy(*requested)
	}
	return &limits
}

// raisedBy returns l with every bound raised sets, a non-zero field, replaced by raised's value.
func (l VerificationLimits) raisedBy(raised VerificationLimits) VerificationLimits {
	pick := func(base, set int64) int64 {
		if set != 0 {
			return set
		}
		return base
	}
	return VerificationLimits{
		MaxEntries:    int(pick(int64(l.MaxEntries), int64(raised.MaxEntries))),
		MaxFiles:      int(pick(int64(l.MaxFiles), int64(raised.MaxFiles))),
		MaxDepth:      int(pick(int64(l.MaxDepth), int64(raised.MaxDepth))),
		MaxFileBytes:  pick(l.MaxFileBytes, raised.MaxFileBytes),
		MaxTotalBytes: pick(l.MaxTotalBytes, raised.MaxTotalBytes),
	}
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
