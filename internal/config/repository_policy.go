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

// HISSComplexityCeiling is the ceiling a projection falls back to when no repository policy
// could be resolved at all, because the workspace carries no manifest. It states the HISS-04
// McCabe, cognitive and statement limits (<= 10, <= 15, <= 50) once, so the editor projections
// and the language server cannot drift from each other.
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

// ResolveRepositoryComplexity resolves the complexity ceilings a workspace root imposes and
// completes any limit the repository left unset from HISSComplexityCeiling. An unadopted
// workspace -- one with no <root>/.standards.yaml -- resolves to that ceiling itself, which
// is what the editor and LSP projections asserted unconditionally before this existed.
func ResolveRepositoryComplexity(ctx context.Context, root string) (ComplexityPolicy, error) {
	if root == "" {
		root = "."
	}
	policy, _, err := ResolveRepositoryPolicy(ctx, filepath.Join(root, ManifestFileName), nil)
	if err != nil {
		return ComplexityPolicy{}, err
	}
	if policy == nil {
		return HISSComplexityCeiling(), nil
	}
	return policy.Complexity.WithHISSDefaults(), nil
}
