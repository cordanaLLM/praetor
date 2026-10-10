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

// CompileReadOnlyContext reads the canonical AGENTS.md at source once, projects it
// (agentcontext.ReadOnlyTarget), and writes ReadOnlyFile below targetDir after the writer's
// refusals pass. compile-context writes the same file with the vendor files instead
// (CompileVendorTargets); this writes it alone, for a caller that compiled the vendor files
// another way.
func CompileReadOnlyContext(ctx context.Context, source, targetDir string) error {
	contentBytes, err := contextopt.ReadSnapshot(ctx, source)
	if err != nil {
		return fmt.Errorf("read source %s: %w", source, err)
	}
	target, err := agentcontext.ReadOnlyTarget(string(contentBytes))
	if err != nil {
		return fmt.Errorf("compile read-only projection: %w", err)
	}
	file := projectionFile{rel: target.RelativePath, data: []byte(target.Content)}
	if err := checkProjectionFiles(ctx, targetDir, []projectionFile{file}); err != nil {
		return err
	}
	return writeConfinedText(ctx, targetDir, file.rel, file.data)
}

// VerifyReadOnlyContext checks that ReadOnlyFile below targetDir exists and matches the
// compiled read-only projection of source, and that the projection passes the caveman gate.
func VerifyReadOnlyContext(ctx context.Context, source, targetDir string) error {
	contentBytes, err := contextopt.ReadSnapshot(ctx, source)
	if err != nil {
		return fmt.Errorf("read source %s: %w", source, err)
	}
	want, err := agentcontext.ReadOnlyTarget(string(contentBytes))
	if err != nil {
		return fmt.Errorf("compile read-only projection: %w", err)
	}
	if _, err := LintContextText(ReadOnlyFile, want.Content); err != nil {
		return err
	}
	existing, err := readConfinedText(ctx, targetDir, ReadOnlyFile)
	if err != nil {
		return fmt.Errorf("target %s missing or unreadable: %w", ReadOnlyFile, err)
	}
	if equal, strict := projectionMatches(existing, []byte(want.Content)); !equal {
		return fmt.Errorf("target %s is out of sync with %s%s; run 'praetorctl compile-context' to reconcile",
			ReadOnlyFile, source, util.ByteExactNote(strict))
	}
	return nil
}
