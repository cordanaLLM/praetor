package config

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// ManifestFileName is the repository manifest a workspace root is resolved through.
const ManifestFileName = ".standards.yaml"

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
// The function-length limit is AuditMaxFuncLOC rather than the 75 HISS-04 documents: the audit
// caps every adopted repository at that length whatever its manifest declares, so a workspace
// told 75 would accept a function its first audit after adoption rejects (BUG-445).
func HISSComplexityCeiling() ComplexityPolicy {
	return ComplexityPolicy{MaxCyclomatic: 10, MaxCognitive: 15, MaxFuncLOC: AuditMaxFuncLOC, MaxStatements: 50}
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
//   - a manifest without a lock: no audit runs yet, and the first one after adoption caps
//     function length at AuditMaxFuncLOC, so the 100-line, cyclomatic-15 baseline would tell
//     an editor to accept what that audit rejects;
//   - a manifest or lock that cannot be resolved, including the lock `praetorctl init` writes
//     before any catalog is pinned: the returned warning names the cause. The projection keeps
//     working, as it did before it read policy at all, and `praetorctl audit` still fails on
//     the same state, so the fallback hides nothing.
//
// An error is returned only for a nil context or a resolution the caller's ctx interrupted;
// an interrupted resolution is never reported as a policy the repository declares.
func ResolveRepositoryComplexity(ctx context.Context, root string) (ComplexityPolicy, string, error) {
	if ctx == nil {
		return ComplexityPolicy{}, "", errors.New("resolving repository complexity requires a context")
	}
	if root == "" {
		root = "."
	}
	configPath := filepath.Join(root, ManifestFileName)
	if !util.FileExists(configPath) {
		return HISSComplexityCeiling(), "", nil
	}
	manifest, err := LoadManifest(configPath)
	if err != nil {
		return unresolvedComplexity(ctx, nil, err)
	}
	policy, notice, err := ResolveRepositoryPolicy(ctx, configPath, manifest)
	switch {
	case err != nil:
		return unresolvedComplexity(ctx, manifest, err)
	case policy == nil:
		// The manifest vanished between the two reads: the workspace is unadopted now.
		return HISSComplexityCeiling(), "", nil
	case notice == NoLockNotice:
		return ceilingWithOverrides(manifest), "", nil
	}
	return policy.Complexity.WithHISSDefaults(), "", nil
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
