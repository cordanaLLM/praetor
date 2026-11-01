package adopt

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
)

// supplyChainException returns the HISS-11 exceptions entry the manifest a fresh adoption
// creates declares, or nil. The default facet security:high and most profiles declare SLSA Build
// Level 3 with cosign signing and an SBOM, and adoption scaffolds no release workflow, so without
// the entry every fresh repository failed its first audit. The entry declares that gap with what
// was measured, for the longest the exceptions list allows (config.MaxExceptionDays): the audit
// prints it and passes until it expires, and then fails until the release workflow is raised or
// the entry renewed (AGENTS.md rule 14).
//
// The policy is the one this run leaves the repository under (prospectivePolicy). A run that
// cannot resolve it without --lock-source-root records nothing, as its policy-catalog step
// reports; any other failure to resolve or measure is a warning, so the first audit names it.
func (s *adoptSession) supplyChainException(ctx context.Context) *config.Exception {
	if s.supplyChain.resolved {
		return s.supplyChain.entry
	}
	s.supplyChain.resolved = true
	policy, err := prospectivePolicy(ctx, s, s.opts.LockSourceRoot)
	if err != nil {
		if !errors.Is(err, ErrLockSourceRequired) {
			s.report.addWarning("no %s exception recorded: the policy this adoption declares did not resolve: %v",
				config.ExceptionRuleSupplyChain, err)
		}
		return nil
	}
	declared := policy.Policy.SupplyChain
	if !declaresSupplyChain(declared) {
		return nil
	}
	gap, err := measureSupplyChainGap(ctx, s.repoPath, declared)
	if err != nil {
		s.report.addWarning("no %s exception recorded: %v", config.ExceptionRuleSupplyChain, err)
		return nil
	}
	if len(gap.Shortfalls) == 0 {
		return nil
	}
	entry := newSupplyChainException(gap, time.Now())
	s.supplyChain.entry = &entry
	return s.supplyChain.entry
}

// newSupplyChainException is the entry adoption records for gap on today: the release workflow
// the measurement read, a reason naming the gap and the guide that closes it, and the latest
// expiry the list allows.
func newSupplyChainException(gap supplyChainGap, today time.Time) config.Exception {
	return config.Exception{
		Rule:     config.ExceptionRuleSupplyChain,
		Path:     gap.releaseWorkflow(),
		Category: "D",
		Reason: fmt.Sprintf("adoption found the release workflows short of the declared supply chain (%s); raise them as "+
			"%s in Praetor describes, then remove this entry", gap.summary(), supplyChainGuide),
		Expires: config.ExceptionDay(today).AddDate(0, 0, config.MaxExceptionDays).Format(config.ExceptionDateLayout),
	}
}

// PlannedReleaseWorkflow returns the release workflow a HISS-11 entry may name before the file
// exists: while no workflow under root writes provenance, the path newSupplyChainException
// records for a fresh repository (supplyChainGap.releaseWorkflow), and "" once one does. The
// exceptions audit asks it, so a planned file is told from a stale one by the code that writes
// the entry, not by a second copy of the path (#1259).
func PlannedReleaseWorkflow(ctx context.Context, root string) (string, error) {
	measured, err := forge.MeasureProvenance(ctx, root)
	if err != nil {
		return "", fmt.Errorf("measure the release workflows: %w", err)
	}
	if measured.LevelWorkflow != "" {
		return "", nil
	}
	return supplyChainGap{Measured: measured}.releaseWorkflow(), nil
}

// withSupplyChainException returns exceptions with the HISS-11 entry a first adoption records
// (supplyChainException) appended, and that entry, when exceptions declares no HISS-11 entry and
// the release workflows fall short of the declared supply chain. A re-adoption over a manifest an
// older engine wrote used to record nothing and report success over a tree its next audit
// refused (#1069). An entry the manifest already declares, renewed or narrowed by hand, stands.
func (s *adoptSession) withSupplyChainException(ctx context.Context, exceptions []config.Exception) ([]config.Exception, *config.Exception) {
	for i := 0; i < len(exceptions) && i < config.MaxExceptions; i++ {
		if exceptions[i].Rule == config.ExceptionRuleSupplyChain {
			return exceptions, nil
		}
	}
	entry := s.supplyChainException(ctx)
	if entry == nil {
		return exceptions, nil
	}
	return append(slices.Clone(exceptions), *entry), entry
}

// supplyChainExceptionNote is what the manifest's report line says about the entry it declares.
func supplyChainExceptionNote(entry config.Exception) string {
	return fmt.Sprintf("; exceptions: recorded rule %s for %s until %s, because %s. praetorctl audit prints it and passes "+
		"until then", entry.Rule, entry.Path, entry.Expires, entry.Reason)
}
