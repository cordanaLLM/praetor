package compiler

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/agenthook"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// ErrRegisterBlockOutOfSync is matched (errors.Is) by the error a verifying
// SyncRegisterBlock returns when the block in AGENTS.md differs from what the register policy
// renders, a missing block included. The returned error's text says which of the two it is
// and names the policy's origin (config.RegisterAuthority.PolicyOrigin).
var ErrRegisterBlockOutOfSync = errors.New("text register block is out of sync; run 'praetorctl compile-context'")

// registerBlockDrift is the error a verifying SyncRegisterBlock returns. It used to be the bare
// sentinel, which named .standards.yaml for every repository, including one without that file,
// and called a block that was never written "out of sync" (#572).
type registerBlockDrift struct {
	file    string // base name of the verified source, such as AGENTS.md
	origin  string // config.RegisterAuthority.PolicyOrigin
	missing bool   // the source carries no register block at all
}

func (e *registerBlockDrift) Error() string {
	if e.missing {
		return fmt.Sprintf("%s has no text register block; run 'praetorctl compile-context' to render it from %s", e.file, e.origin)
	}
	return fmt.Sprintf("%s text register block is out of sync with %s; run 'praetorctl compile-context'", e.file, e.origin)
}

func (e *registerBlockDrift) Unwrap() error { return ErrRegisterBlockOutOfSync }

// LoadRegisterBlock resolves the checked text register policy of the repository at root
// (config.LoadCheckedRegisterAuthority) and renders its block. The block says a registered hook
// denies a subagent brief without `task:` only where root registers the pre-dispatch hook in a
// client hook file (agenthook.DispatchGateRegistered); elsewhere nothing enforces the label, and
// the block does not claim it (#504).
func LoadRegisterBlock(ctx context.Context, root string) (config.RegisterPolicy, string, error) {
	authority, block, err := loadRegister(ctx, root)
	if err != nil {
		return config.RegisterPolicy{}, "", err
	}
	return authority.Policy(), block, nil
}

// loadRegister is LoadRegisterBlock returning the authority itself, so SyncRegisterBlock can
// name the policy's origin in a drift error.
func loadRegister(ctx context.Context, root string) (config.RegisterAuthority, string, error) {
	authority, err := config.LoadCheckedRegisterAuthority(ctx, root)
	if err != nil {
		return config.RegisterAuthority{}, "", err
	}
	gated, err := agenthook.DispatchGateRegistered(ctx, root)
	if err != nil {
		return config.RegisterAuthority{}, "", fmt.Errorf("text register: %w", err)
	}
	block, err := config.RenderRegisterBlock(authority.Policy(), gated)
	if err != nil {
		return config.RegisterAuthority{}, "", err
	}
	return authority, block, nil
}

// SyncRegisterBlock reconciles the text register block of agentsMdPath with the manifest at
// root. It runs before compilation, so the six vendor files receive the block through the
// unchanged renderer. With write it splices the block and reports whether the file changed;
// without write it never touches the file and returns an error matching
// ErrRegisterBlockOutOfSync on drift.
func SyncRegisterBlock(ctx context.Context, root, agentsMdPath string, write bool) (changed bool, err error) {
	authority, block, err := loadRegister(ctx, root)
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
		return true, &registerBlockDrift{file: filepath.Base(agentsMdPath), origin: authority.PolicyOrigin(), missing: first < 0}
	}
	if err := contextopt.WriteSnapshot(ctx, agentsMdPath, []byte(util.RestoreLineEndings(out, crlf)), 0o644); err != nil {
		return false, fmt.Errorf("failed to write %s: %w", agentsMdPath, err)
	}
	return true, nil
}
