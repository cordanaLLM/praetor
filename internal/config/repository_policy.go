package config

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/util"
)

// ManifestFileName is the repository manifest a workspace root is resolved through.
const ManifestFileName = ".standards.yaml"

// LockFileName is the repository lockfile that pins the manifest's profiles and facets.
const LockFileName = ".standards.lock"

// NoLockNotice is returned by ResolveRepositoryPolicy for a repository that carries a
// manifest but no .standards.lock: there are no pinned profiles to resolve, so the result is
// built-in defaults plus the repository's own overrides rather than a degraded stand-in.
const NoLockNotice = "no .standards.lock: built-in defaults and repository overrides only"

// repositoryPolicyTimeout bounds the policy read (HISS-02).
const repositoryPolicyTimeout = 30 * time.Second

// HISSComplexityCeiling is the ceiling a projection states when the workspace has no resolvable
// lock-backed policy: no manifest, a manifest without a lock, or a policy that does not resolve
// (ResolveRepositoryComplexity tightens it by the manifest's overrides where it can). It states
// the HISS-04 McCabe, cognitive and statement limits (<= 10, <= 15, <= 50) once, so the editor
// projections, the language server and the MCP inspection cannot drift from each other.
//
// The function-length limit is AuditMaxFuncLOC (hiss.DefaultMaxFuncLOC, the one source every
// function-length default derives from) rather than the 75 HISS-04 documents: the audit
// caps every adopted repository at that length whatever its manifest declares, so a workspace
// told 75 would accept a function its first audit after adoption rejects (BUG-445).
func HISSComplexityCeiling() ComplexityPolicy {
	return ComplexityPolicy{
		MaxCyclomatic: hiss.DefaultMaxCyclomatic, MaxCognitive: hiss.DefaultMaxCognitive,
		MaxFuncLOC: AuditMaxFuncLOC, MaxStatements: hiss.DefaultMaxStatements,
	}
}

// Limits returns the cyclomatic, cognitive and statement limits of c in the form the scanner
// measures against. A non-positive limit falls back to the HISS-04 default there.
func (c ComplexityPolicy) Limits() hiss.ComplexityLimits {
	return hiss.ComplexityLimits{MaxCyclomatic: c.MaxCyclomatic, MaxCognitive: c.MaxCognitive, MaxStatements: c.MaxStatements}
}

// ScanOptions returns opts with every HISS-04 limit of c: the function length the scanner
// enforces and the complexity limits it measures against without enforcing. Every entry
// point that scans under a resolved policy derives its options here, so the audit, the gate,
// the MCP audit and public verification measure against the same limits.
func (c ComplexityPolicy) ScanOptions(opts hiss.ScanOptions) hiss.ScanOptions {
	opts.MaxFuncLOC = c.MaxFuncLOC
	opts.Complexity = c.Limits()
	return opts
}

// WithHISSDefaults completes every limit the caller left unset (non-positive) from
// HISSComplexityCeiling. A limit that was resolved is returned unchanged; this never widens
// a repository's own ceiling.
func (c ComplexityPolicy) WithHISSDefaults() ComplexityPolicy {
	ceiling := HISSComplexityCeiling()
	for _, limit := range [][2]*int{
		{&c.MaxCyclomatic, &ceiling.MaxCyclomatic},
		{&c.MaxCognitive, &ceiling.MaxCognitive},
		{&c.MaxFuncLOC, &ceiling.MaxFuncLOC},
		{&c.MaxStatements, &ceiling.MaxStatements},
	} {
		if *limit[0] <= 0 {
			*limit[0] = *limit[1]
		}
	}
	return c
}

// ResolveRepositoryPolicy resolves the policy the manifest at configPath imposes; the
// manifest's directory is the repository root. It is the single resolver for every consumer
// that has to report the ceilings `praetorctl audit` enforces -- the dry run, the editor
// projections and the language server -- because three implementations of one question
// answered it three different ways (issue #360).
//
// It returns (nil, "", nil) when configPath does not exist: a workspace may legitimately be
// unadopted, and the caller decides what an unadopted workspace falls back to. manifest may
// carry an already-decoded manifest for configPath; it is read from disk when nil.
//
// Audit is set, so this resolves through the same audit-compatibility ceiling the audit path
// applies. Leaving it unset would report the uncapped value and reproduce the same
// disagreement one number further along.
func ResolveRepositoryPolicy(ctx context.Context, configPath string, manifest *Manifest) (*ResolvedPolicy, string, error) {
	return ResolveRepositoryPolicyFromCatalog(ctx, configPath, "", manifest)
}

// ResolveRepositoryPolicyFromCatalog is ResolveRepositoryPolicy with the pinned profiles and
// facets read from catalogRoot, for commands that accept --catalog-root. An empty catalogRoot
// is the repository root.
func ResolveRepositoryPolicyFromCatalog(ctx context.Context, configPath, catalogRoot string, manifest *Manifest) (*ResolvedPolicy, string, error) {
	if ctx == nil {
		return nil, "", errors.New("resolving a repository policy requires a context")
	}
	if !util.FileExists(configPath) {
		return nil, "", nil
	}
	root, err := filepath.Abs(filepath.Dir(configPath))
	if err != nil {
		return nil, "", fmt.Errorf("resolve planned root: %w", err)
	}
	if manifest == nil {
		if manifest, err = LoadManifest(configPath); err != nil {
			return nil, "", err
		}
	}
	// Without a lockfile there are no pinned profiles to resolve, so defaults plus the
	// repository's own overrides is the whole policy rather than a degraded stand-in. This is
	// the ungoverned case -- a repository before it is adopted -- and it must keep working, so
	// the absence is checked for explicitly instead of being inferred from a read error, which
	// would also swallow a corrupt or unreadable lock.
	if !util.PathExists(filepath.Join(root, ".standards.lock")) {
		policy := DefaultPolicy()
		policy.ApplyOverrides(manifest.Overrides)
		return policy, NoLockNotice, nil
	}
	ctx, cancel := context.WithTimeout(ctx, repositoryPolicyTimeout)
	defer cancel()
	effective, err := LoadEffectivePolicyContext(ctx, EffectiveOptions{
		Root:         root,
		ManifestPath: configPath,
		CatalogRoot:  catalogRoot,
		Audit:        true,
	})
	if err != nil {
		return nil, "", fmt.Errorf("resolve effective policy: %w", err)
	}
	return &effective.Policy, "", nil
}

// ResolveRepositoryComplexity resolves the complexity ceilings a workspace root imposes, for
// the projections that restate them: the editor configurations, the language server and the
// MCP symbol inspection. A locked repository resolves exactly as ResolveRepositoryPolicy does,
// with any limit it left unset completed from HISSComplexityCeiling.
//
// Every other state resolves to HISSComplexityCeiling tightened by whatever complexity
// overrides the manifest declares, never to the DefaultPolicy baseline the plan preview shows:
//   - no <root>/.standards.yaml: the workspace is unadopted;
//   - a manifest without a lock: no audit runs yet, and the DefaultPolicy baseline's
//     cyclomatic 15, cognitive 20 and 75 statements would tell an editor to accept what the
//     HISS-04 ceiling rejects (function length is hiss.DefaultMaxFuncLOC in both);
//   - a manifest or lock that cannot be resolved, including the lock `praetorctl init` writes
//     before any catalog is pinned: the returned warning names the cause. The projection keeps
//     working, as it did before it read policy at all, and `praetorctl audit` still fails on
//     the same state, so the fallback hides nothing.
//
// An error is returned only for a nil context or a resolution the caller's ctx interrupted;
// an interrupted resolution is never reported as a policy the repository declares.
func ResolveRepositoryComplexity(ctx context.Context, root string) (ComplexityPolicy, string, error) {
	complexity, _, warning, err := resolveRepositoryComplexity(ctx, root)
	return complexity, warning, err
}

// ResolveRepositoryScanOptions returns opts with every scan input the repository at root
// imposes: the complexity limits ResolveRepositoryComplexity resolves and the HISS exceptions
// its manifest declares and documents (Manifest.CleanupGotoException). The gate's HISS stage
// and `praetorctl baseline` scan with it, so they judge the tree as `praetorctl audit` does
// (EffectivePolicy.HISSScanOptions). The warning joins the unresolved-policy notice and an
// unhonoured exception; an error is returned only when ResolveRepositoryComplexity returns one.
func ResolveRepositoryScanOptions(ctx context.Context, root string, opts hiss.ScanOptions) (hiss.ScanOptions, string, error) {
	complexity, manifest, warning, err := resolveRepositoryComplexity(ctx, root)
	if err != nil {
		return opts, "", err
	}
	opts = complexity.ScanOptions(opts)
	cleanupGoto, exceptionWarning := manifest.CleanupGotoException(root)
	opts.CleanupGoto = cleanupGoto
	warnings := slices.DeleteFunc([]string{warning, exceptionWarning}, func(w string) bool { return w == "" })
	return opts, strings.Join(warnings, "; "), nil
}

// resolveRepositoryComplexity is ResolveRepositoryComplexity returning, too, the manifest it
// read at root, or nil when there is none or it does not parse.
func resolveRepositoryComplexity(ctx context.Context, root string) (ComplexityPolicy, *Manifest, string, error) {
	if ctx == nil {
		return ComplexityPolicy{}, nil, "", errors.New("resolving repository complexity requires a context")
	}
	if root == "" {
		root = "."
	}
	configPath := filepath.Join(root, ManifestFileName)
	if !util.FileExists(configPath) {
		return HISSComplexityCeiling(), nil, "", nil
	}
	manifest, err := LoadManifest(configPath)
	if err != nil {
		complexity, warning, err := unresolvedComplexity(ctx, nil, err)
		return complexity, nil, warning, err
	}
	policy, notice, err := ResolveRepositoryPolicy(ctx, configPath, manifest)
	switch {
	case err != nil:
		complexity, warning, err := unresolvedComplexity(ctx, manifest, err)
		return complexity, manifest, warning, err
	case policy == nil:
		// The manifest vanished between the two reads: the workspace is unadopted now.
		return HISSComplexityCeiling(), nil, "", nil
	case notice == NoLockNotice:
		return ceilingWithOverrides(manifest), manifest, "", nil
	}
	return policy.Complexity.WithHISSDefaults(), manifest, "", nil
}

// ceilingWithOverrides is HISSComplexityCeiling tightened by the manifest's complexity
// overrides. Overrides only ever tighten (applyOverride), so a manifest cannot use this path
// to state a limit looser than the ceiling.
func ceilingWithOverrides(manifest *Manifest) ComplexityPolicy {
	ceiling := HISSComplexityCeiling()
	if manifest != nil && manifest.Overrides.Complexity != nil {
		ceiling.applyOverride(manifest.Overrides.Complexity)
	}
	return ceiling
}

// unresolvedComplexity is the projection fallback for a policy that exists but cannot be
// resolved. A cause the caller's context produced is returned as an error instead.
func unresolvedComplexity(ctx context.Context, manifest *Manifest, cause error) (ComplexityPolicy, string, error) {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ComplexityPolicy{}, "", fmt.Errorf("resolve repository complexity: %w", ctxErr)
	}
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		return ComplexityPolicy{}, "", fmt.Errorf("resolve repository complexity: %w", cause)
	}
	ceiling := ceilingWithOverrides(manifest)
	warning := fmt.Sprintf("repository policy unresolved (%v); stating the HISS-04 ceiling "+
		"(cyclomatic %d, cognitive %d, %d lines, %d statements) until it resolves",
		cause, ceiling.MaxCyclomatic, ceiling.MaxCognitive, ceiling.MaxFuncLOC, ceiling.MaxStatements)
	return ceiling, warning, nil
}
