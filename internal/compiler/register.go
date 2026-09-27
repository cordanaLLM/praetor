package compiler

import (
	"context"
	"errors"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/agenthook"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// ErrRegisterBlockOutOfSync is returned by a verifying SyncRegisterBlock when the block in
// AGENTS.md differs from what the manifest renders, a missing block included.
var ErrRegisterBlockOutOfSync = errors.New("AGENTS.md text register block is out of sync with .standards.yaml; run 'praetorctl compile-context'")

// LoadRegisterBlock resolves the checked text register policy of the repository at root
// (config.LoadCheckedRegisterAuthority) and renders its block. The block says a registered hook
// denies a subagent brief without `task:` only where root registers the pre-dispatch hook in a
// client hook file (agenthook.DispatchGateRegistered); elsewhere nothing enforces the label, and
// the block does not claim it (#504).
func LoadRegisterBlock(ctx context.Context, root string) (config.RegisterPolicy, string, error) {
	authority, err := config.LoadCheckedRegisterAuthority(ctx, root)
	if err != nil {
		return config.RegisterPolicy{}, "", err
	}
	gated, err := agenthook.DispatchGateRegistered(ctx, root)
	if err != nil {
		return config.RegisterPolicy{}, "", fmt.Errorf("text register: %w", err)
	}
	policy := authority.Policy()
	block, err := config.RenderRegisterBlock(policy, gated)
	if err != nil {
		return config.RegisterPolicy{}, "", err
	}
	return policy, block, nil
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
