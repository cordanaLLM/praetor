package compiler

import (
	"context"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// ReadOnlyFile names the single canonical read-only context file.
const ReadOnlyFile = agentcontext.CanonicalReadOnlyFile

// CompileReadOnlyContext reads the canonical AGENTS.md at source, transforms it into the
// read-only projection (ReadOnlyProjection), and writes ReadOnlyFile below targetDir.
func CompileReadOnlyContext(ctx context.Context, source, targetDir string) error {
	contentBytes, err := contextopt.ReadSnapshot(ctx, source)
	if err != nil {
		return fmt.Errorf("read source %s: %w", source, err)
	}
	projection, err := agentcontext.ReadOnlyProjection(string(contentBytes))
	if err != nil {
		return fmt.Errorf("compile read-only projection: %w", err)
	}
	file := projectionFile{rel: ReadOnlyFile, data: []byte(projection)}
	if err := checkProjectionFiles(ctx, targetDir, []projectionFile{file}); err != nil {
		return err
	}
	return writeConfinedText(ctx, targetDir, ReadOnlyFile, file.data)
}

// VerifyReadOnlyContext checks that ReadOnlyFile below targetDir exists and matches the
// compiled read-only projection of source.
func VerifyReadOnlyContext(ctx context.Context, source, targetDir string) error {
	contentBytes, err := contextopt.ReadSnapshot(ctx, source)
	if err != nil {
		return fmt.Errorf("read source %s: %w", source, err)
	}
	want, err := agentcontext.ReadOnlyProjection(string(contentBytes))
	if err != nil {
		return fmt.Errorf("compile read-only projection: %w", err)
	}
	if _, err := LintContextText(ReadOnlyFile, want); err != nil {
		return err
	}
	existing, err := readConfinedText(ctx, targetDir, ReadOnlyFile)
	if err != nil {
		return fmt.Errorf("target %s missing or unreadable: %w", ReadOnlyFile, err)
	}
	if equal, strict := projectionMatches(existing, []byte(want)); !equal {
		return fmt.Errorf("target %s is out of sync with %s%s; run 'praetorctl compile-context' to reconcile",
			ReadOnlyFile, source, util.ByteExactNote(strict))
	}
	return nil
}

// ContextForBrief returns the read-only context projection if brief is marked read-only,
// or the full content unchanged if brief is not read-only.
func ContextForBrief(content, brief string) (string, error) {
	return agentcontext.ContextForBrief(content, brief)
}

// ContextForRole returns the read-only context projection if role represents a read-only agent,
// or the full content unchanged otherwise.
func ContextForRole(content, role string) (string, error) {
	return agentcontext.ContextForRole(content, role)
}
