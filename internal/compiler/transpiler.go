package compiler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/cordanaLLM/praetor/internal/agentcontext"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"path/filepath"
)

const MaxLineBudget = agentcontext.MaxLineBudget

type TargetFile = agentcontext.TargetFile
type CompileResult = agentcontext.CompileResult
type Transpiler agentcontext.Transpiler

func NewTranspiler() *Transpiler { return &Transpiler{MaxLines: MaxLineBudget} }

// Compile reads the canonical AGENTS.md and synthesizes vendor-specific files.
func (t *Transpiler) Compile(agentsMdPath string) (*CompileResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultAgentTimeout)
	defer cancel()
	return t.CompileContext(ctx, agentsMdPath)
}

func (t *Transpiler) CompileContext(ctx context.Context, agentsMdPath string) (*CompileResult, error) {
	contentBytes, err := contextopt.ReadSnapshot(ctx, agentsMdPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read source %s: %w", agentsMdPath, err)
	}
	res, err := t.CompileContent(string(contentBytes))
	if err != nil {
		return nil, err
	}
	res.SourcePath = agentsMdPath
	return res, nil
}

// CompileContent delegates pure rendering to the shared leaf renderer.
func (t *Transpiler) CompileContent(content string) (*CompileResult, error) {
	return (*agentcontext.Transpiler)(t).CompileContent(content)
}

// WriteOutputs writes compiled files to targetDir.
func (t *Transpiler) WriteOutputs(result *CompileResult, targetDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultAgentTimeout)
	defer cancel()
	return t.WriteOutputsContext(ctx, result, targetDir)
}

func (t *Transpiler) WriteOutputsContext(ctx context.Context, result *CompileResult, targetDir string) error {
	if ctx == nil {
		return errors.New("output writes require a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if result == nil || len(result.Files) > MaxAgentFiles {
		return errors.New("invalid or oversized compiled outputs")
	}
	for _, file := range result.Files {
		if _, err := projectionPath(targetDir, file.RelativePath); err != nil {
			return err
		}
	}
	for _, file := range result.Files {
		path, err := projectionPath(targetDir, file.RelativePath)
		if err != nil {
			return err
		}
		if err := writeVendorAgent(ctx, path, file.Content); err != nil {
			return err
		}
	}
	return nil
}

// Verify checks that existing target files match compiled output without modification.
func (t *Transpiler) Verify(agentsMdPath string, targetDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultAgentTimeout)
	defer cancel()
	return t.VerifyContext(ctx, agentsMdPath, targetDir)
}

func (t *Transpiler) VerifyContext(ctx context.Context, agentsMdPath, targetDir string) error {
	res, err := t.CompileContext(ctx, agentsMdPath)
	if err != nil {
		return err
	}

	for _, f := range res.Files {
		fullPath := filepath.Join(targetDir, f.RelativePath)
		existing, err := contextopt.ReadSnapshot(ctx, fullPath)
		if err != nil {
			return fmt.Errorf("target %s missing or unreadable: %w", f.RelativePath, err)
		}
		if !bytes.Equal(bytes.TrimSpace(existing), bytes.TrimSpace([]byte(f.Content))) {
			return fmt.Errorf("target %s is out of sync with %s; run 'praetorctl compile-context' to reconcile", f.RelativePath, agentsMdPath)
		}
	}
	return nil
}
