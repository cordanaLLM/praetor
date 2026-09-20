package compiler

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/router"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	registerManifestRel = ".standards.yaml"
	registerRoutingRel  = ".config/models/routing.yaml"
)

// ErrRegisterBlockOutOfSync is returned by a verifying SyncRegisterBlock when the block in
// AGENTS.md differs from what the manifest renders, a missing block included.
var ErrRegisterBlockOutOfSync = errors.New("AGENTS.md text register block is out of sync with .standards.yaml; run 'praetorctl compile-context'")

// LoadRegisterBlock resolves the text register policy of the repository at root and renders
// its block. The manifest and the routing configuration are both optional: without a
// manifest the defaults govern, and without a routing.yaml the router's built-in labels are
// the vocabulary. Only the rows the manifest wrote are checked against that vocabulary; a
// default row is never an error in a repository that declared its own labels.
func LoadRegisterBlock(ctx context.Context, root string) (config.RegisterPolicy, string, error) {
	if ctx == nil {
		return config.RegisterPolicy{}, "", errors.New("text register requires a context")
	}
	if err := ctx.Err(); err != nil {
		return config.RegisterPolicy{}, "", err
	}
	manifest, err := loadRegisterManifest(root)
	if err != nil {
		return config.RegisterPolicy{}, "", err
	}
	if manifest != nil && manifest.Register != nil {
		labels, err := registerTaskLabels(ctx, root)
		if err != nil {
			return config.RegisterPolicy{}, "", err
		}
		if err := manifest.Register.ValidateTaskLabels(labels); err != nil {
			return config.RegisterPolicy{}, "", fmt.Errorf("%s: %w", registerManifestRel, err)
		}
	}
	policy := manifest.EffectiveRegister()
	block, err := config.RenderRegisterBlock(policy)
	if err != nil {
		return config.RegisterPolicy{}, "", err
	}
	return policy, block, nil
}

// loadRegisterManifest returns nil when root carries no manifest; the defaults then govern.
func loadRegisterManifest(root string) (*config.Manifest, error) {
	path := filepath.Join(root, registerManifestRel)
	if !util.FileExists(path) {
		return nil, nil
	}
	return config.LoadManifest(path)
}

// registerTaskLabels returns the target_tasks vocabulary that governs root.
func registerTaskLabels(ctx context.Context, root string) ([]string, error) {
	path := filepath.Join(root, filepath.FromSlash(registerRoutingRel))
	if !util.FileExists(path) {
		return router.DefaultTaskLabels(), nil
	}
	cfg, err := router.LoadRoutingConfigContext(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("text register task labels: %w", err)
	}
	return router.DeclaredTaskLabels(cfg), nil
}

// SyncRegisterBlock reconciles the text register block of agentsMdPath with the manifest at
// root. It runs before compilation, so the six vendor files receive the block through the
// unchanged renderer. With write it splices the block and reports whether the file changed;
// without write it never touches the file and returns ErrRegisterBlockOutOfSync on drift.
func SyncRegisterBlock(ctx context.Context, root, agentsMdPath string, write bool) (changed bool, err error) {
	_, block, err := LoadRegisterBlock(ctx, root)
	if err != nil {
		return false, err
	}
	data, err := contextopt.ReadSnapshot(ctx, agentsMdPath)
	if err != nil {
		return false, fmt.Errorf("failed to read source %s: %w", agentsMdPath, err)
	}
	// A Windows checkout can hold the source with CRLF endings while the block renders
	// with LF. Compare and splice in LF, then write the file back in its own convention,
	// so --verify decides the same way on every platform (HISS-21).
	content, crlf := util.NormalizeLineEndings(string(data))
	first, _, err := util.FindMarkedBlock(content, config.RegisterBlockStart, config.RegisterBlockEnd)
	if err != nil {
		return false, fmt.Errorf("%s: %w", agentsMdPath, err)
	}
	if first < 0 {
		// A document without markers receives the whole section, heading included.
		block = config.RegisterSectionPrefix + block
	}
	out, changed, err := util.ReplaceMarkedBlock(content, config.RegisterBlockStart, config.RegisterBlockEnd, block, MaxLineBudget)
	if err != nil {
		return false, fmt.Errorf("%s: %w", agentsMdPath, err)
	}
	if !changed {
		return false, nil
	}
	if !write {
		return true, ErrRegisterBlockOutOfSync
	}
	if err := contextopt.WriteSnapshot(ctx, agentsMdPath, []byte(util.RestoreLineEndings(out, crlf)), 0o644); err != nil {
		return false, fmt.Errorf("failed to write %s: %w", agentsMdPath, err)
	}
	return true, nil
}
