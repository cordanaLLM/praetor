package dogfood

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/needs"
)

const maxDiscoveryRoots = 128

type discoveryPlannerResult struct {
	plan *adopt.VerificationPlan
	err  error
}

type discoveryObserver struct {
	limits       *InputLimits
	root         string
	tree         publicTree
	verification map[string]discoveryPlannerResult
	matched      map[string]bool
}

// ObserveCapabilities inspects selected capabilities without executing source
// commands. A snapshot digest identifies the input; RunDiscovery also checks
// that it has not changed before admitting any backlog candidates.
func ObserveCapabilities(ctx context.Context, root string, policy DiscoveryPolicy) (*CapabilityDiscovery, error) {
	if ctx == nil {
		return nil, errors.New("capability discovery requires context")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	result := &CapabilityDiscovery{Version: policy.Version, Status: "unknown", Observations: []CapabilityObservation{}}
	if err := ValidateDiscoveryPolicy(policy); err != nil {
		return discoveryFailure(result, err)
	}
	limits, err := normalizeInputLimits(policy.InputLimits)
	if err != nil {
		return discoveryFailure(result, err)
	}
	result.InputLimits = limits
	tree, snapshot, err := snapshotTreeWithLimits(ctx, root, true, &limits.Snapshot)
	result.Snapshot = &snapshot
	if err != nil {
		return discoveryFailure(result, err)
	}
	result.TreeSHA256 = tree.digest()
	for _, digest := range tree {
		if isRegularDiscoveryDigest(digest) {
			result.FilesObserved++
		}
	}
	if result.FilesObserved == 0 {
		return discoveryFailure(result, errors.New("discovery snapshot contains no regular files"))
	}
	observer := discoveryObserver{root: root, tree: tree, limits: limits, verification: make(map[string]discoveryPlannerResult), matched: make(map[string]bool)}
	err = observer.observe(ctx, policy, result)
	result.FilesMatched = len(observer.matched)
	result.Status = "observed"
	if err != nil {
		result.Status = "partial"
	}
	return result, err
}

func discoveryFailure(result *CapabilityDiscovery, err error) (*CapabilityDiscovery, error) {
	result.Errors = append(result.Errors, err.Error())
	return result, err
}

func (o *discoveryObserver) observe(ctx context.Context, policy DiscoveryPolicy, result *CapabilityDiscovery) error {
	var runErrors []error
	for _, rule := range policy.Rules {
		if err := ctx.Err(); err != nil {
			result.Errors = append(result.Errors, err.Error())
			return errors.Join(append(runErrors, err)...)
		}
		paths := matchingDiscoveryPaths(o.tree, rule.Kind, rule.Matches)
		for _, path := range paths {
			o.matched[path] = true
		}
		observation, err := o.observeRule(ctx, rule, paths)
		result.Observations = append(result.Observations, observation)
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
			runErrors = append(runErrors, err)
		}
	}
	return errors.Join(runErrors...)
}

func (o *discoveryObserver) observeRule(ctx context.Context, rule DiscoveryRule, paths []string) (CapabilityObservation, error) {
	obs := CapabilityObservation{Key: rule.Key, Title: rule.Title, Kind: rule.Kind, Status: "available", Basis: "not_applicable"}
	if len(paths) == 0 {
		return obs, nil
	}
	var statuses map[string][]string
	var bases map[string]string
	var err error
	if rule.Kind == discoveryScannerExtension {
		statuses, bases = scannerDiscovery(paths)
	} else {
		statuses, bases, err = o.markerDiscovery(ctx, rule, paths)
	}
	// An available context must never mask a missing or unknown one. Evidence
	// refers only to contexts with the reported status, not every matched file.
	for _, status := range []string{"unsupported", "unknown", "available"} {
		if len(statuses[status]) == 0 {
			continue
		}
		obs.Status, obs.Basis = status, bases[status]
		sort.Strings(statuses[status])
		obs.EvidenceCount = len(statuses[status])
		obs.Evidence = discoveryEvidence(o.tree, statuses[status])
		return obs, err
	}
	obs.Status, obs.Basis = "unknown", "observation-error"
	return obs, err
}

func scannerDiscovery(paths []string) (map[string][]string, map[string]string) {
	statuses := make(map[string][]string)
	for _, path := range paths {
		status := "unsupported"
		if hiss.SupportsExtension(filepath.Ext(path)) {
			status = "available"
		}
		statuses[status] = append(statuses[status], path)
	}
	return statuses, map[string]string{"available": "hiss-dispatch", "unsupported": "hiss-dispatch-unavailable"}
}

func (o *discoveryObserver) markerDiscovery(ctx context.Context, rule DiscoveryRule, paths []string) (map[string][]string, map[string]string, error) {
	byRoot := make(map[string][]string)
	for _, path := range paths {
		dir := filepath.Dir(path)
		byRoot[dir] = append(byRoot[dir], path)
	}
	if len(byRoot) > maxDiscoveryRoots {
		return map[string][]string{"unknown": paths}, map[string]string{"unknown": "marker-root-bound"}, errors.New("discovery rule exceeds 128 marker roots")
	}
	roots := make([]string, 0, len(byRoot))
	for dir := range byRoot {
		roots = append(roots, dir)
	}
	sort.Strings(roots)
	statuses := make(map[string][]string)
	bases := make(map[string]string)
	var runErrors []error
	for _, dir := range roots {
		status, basis, err := o.markerStatus(ctx, rule, dir)
		statuses[status] = append(statuses[status], byRoot[dir]...)
		bases[status] = basis
		if err != nil {
			runErrors = append(runErrors, err)
		}
	}
	return statuses, bases, errors.Join(runErrors...)
}

func (o *discoveryObserver) markerStatus(ctx context.Context, rule DiscoveryRule, dir string) (string, string, error) {
	if err := ctx.Err(); err != nil {
		return "unknown", "context-ended", err
	}
	projectRoot := filepath.Join(o.root, dir)
	if rule.Kind == discoveryNeedsMarker {
		for _, analyzer := range needs.DefaultRegistry().DetectAll(projectRoot) {
			if analyzer.Language() == rule.Analyzer {
				return "available", "needs-analyzer-detected", nil
			}
		}
		return "unsupported", "needs-analyzer-unavailable", nil
	}
	return o.verificationStatus(ctx, projectRoot)
}

func (o *discoveryObserver) verificationStatus(ctx context.Context, root string) (string, string, error) {
	cached, ok := o.verification[root]
	if !ok {
		if len(o.verification) >= maxDiscoveryRoots {
			return "unknown", "verification-root-bound", errors.New("discovery exceeds 128 verification roots")
		}
		limits, err := normalizeInputLimits(o.limits)
		if err != nil {
			return "unknown", "verification-limits-error", err
		}
		cached.plan, cached.err = adopt.ObserveVerificationPlanWithLimits(ctx, root, &limits.Verification)
		o.verification[root] = cached
	}
	if cached.err != nil {
		return "unknown", "verification-plan-error", cached.err
	}
	if cached.plan == nil || cached.plan.Status == "unavailable" {
		return "unknown", "verification-plan-declared-unavailable", nil
	}
	return "available", "verification-plan-declared", nil
}
