package adopt

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
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
		Rule: config.ExceptionRuleSupplyChain,
		Path: gap.releaseWorkflow(),
		Reason: fmt.Sprintf("adoption found the release workflows short of the declared supply chain (%s); raise them as "+
			"%s in Praetor describes, then remove this entry", gap.summary(), supplyChainGuide),
		Expires: config.ExceptionDay(today).AddDate(0, 0, config.MaxExceptionDays).Format(config.ExceptionDateLayout),
	}
}

// supplyChainExceptionNote is what the manifest's report line says about the entry it declares.
func supplyChainExceptionNote(entry config.Exception) string {
	return fmt.Sprintf("; exceptions: recorded rule %s for %s until %s, because %s. praetorctl audit prints it and passes "+
		"until then", entry.Rule, entry.Path, entry.Expires, entry.Reason)
}
