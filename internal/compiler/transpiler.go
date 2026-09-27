package compiler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
)

const MaxLineBudget = agentcontext.MaxLineBudget

type TargetFile = agentcontext.TargetFile
type CompileResult = agentcontext.CompileResult
type Transpiler agentcontext.Transpiler

func NewTranspiler() *Transpiler { return &Transpiler{MaxLines: MaxLineBudget} }

// NotApplicableLine is the report line, without a newline, for a context file or persona
// directory agent_clients leaves out. compile-context and the MCP standards_compile_context
// tool print the same line.
func NotApplicableLine(rel string) string {
	return fmt.Sprintf("  [NOT_APPLICABLE] %-35s (not selected by agent_clients)", rel)
}

// Compile reads the canonical AGENTS.md and synthesizes vendor-specific files.
func (t *Transpiler) Compile(agentsMdPath string) (*CompileResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultAgentTimeout)
	defer cancel()
	return t.CompileContext(ctx, agentsMdPath)
}

// CompileContext reads agentsMdPath and compiles the projections its repository selects.
// Unless t.Clients is set, the selection is agent_clients in the manifest beside the source,
// the same manifest that governs the text register block, so compile, verify and audit agree
// on which projections exist.
func (t *Transpiler) CompileContext(ctx context.Context, agentsMdPath string) (*CompileResult, error) {
	contentBytes, err := contextopt.ReadSnapshot(ctx, agentsMdPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read source %s: %w", agentsMdPath, err)
	}
	selected, err := t.forRepository(ctx, filepath.Dir(agentsMdPath))
	if err != nil {
		return nil, err
	}
	res, err := selected.CompileContent(string(contentBytes))
	if err != nil {
		return nil, err
	}
	res.SourcePath = agentsMdPath
	return res, nil
}

// forRepository returns t when it already carries a selection, otherwise a copy selecting the
// agent clients the manifest at root declares.
func (t *Transpiler) forRepository(ctx context.Context, root string) (*Transpiler, error) {
	if t.Clients != nil {
		return t, nil
	}
	clients, err := declaredAgentClients(ctx, root)
	if err != nil {
		return nil, err
	}
	selected := *t
	selected.Clients = clients
	return &selected, nil
}

// vendorTargetFiles lists, without content, the vendor file of every client t selects for the
// repository at root (forRepository): the paths checkProjectionFiles checks before anything is
// compiled or written.
func (t *Transpiler) vendorTargetFiles(ctx context.Context, root string) ([]projectionFile, error) {
	selected, err := t.forRepository(ctx, root)
	if err != nil {
		return nil, err
	}
	paths, _, err := agentcontext.TargetPaths(selected.Clients)
	if err != nil {
		return nil, err
	}
	files := make([]projectionFile, 0, len(paths))
	for _, rel := range paths {
		files = append(files, projectionFile{rel: rel})
	}
	return files, nil
}

// declaredAgentClients reads agent_clients from the manifest at root; nil means the key is
// absent and every client applies.
func declaredAgentClients(ctx context.Context, root string) ([]string, error) {
	selection, err := config.LoadDeclaredTooling(ctx, root)
	if err != nil {
		return nil, fmt.Errorf("agent client selection: %w", err)
	}
	return selection.AgentClients, nil
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
	files := make([]projectionFile, 0, len(result.Files))
	for _, file := range result.Files {
		files = append(files, projectionFile{rel: file.RelativePath, data: []byte(file.Content)})
	}
	if err := checkProjectionFiles(ctx, targetDir, files); err != nil {
		return err
	}
	// The target directory is the operator's chosen boundary, so it is created the way every
	// other root is (contextopt.EnsureDirectory); everything below it is written confined.
	if err := contextopt.EnsureDirectory(ctx, targetDir, projectedDirPerm); err != nil {
		return err
	}
	return writeProjectionFiles(ctx, targetDir, files)
}

// Verify checks that existing target files match compiled output without modification.
func (t *Transpiler) Verify(agentsMdPath string, targetDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultAgentTimeout)
	defer cancel()
	return t.VerifyContext(ctx, agentsMdPath, targetDir)
}

func (t *Transpiler) VerifyContext(ctx context.Context, agentsMdPath, targetDir string) error {
	_, err := t.VerifyCompiled(ctx, agentsMdPath, targetDir)
	return err
}

// VerifyCompiled is VerifyContext returning the result it verified, so a caller can name the
// projections it checked and the ones the client selection left out.
func (t *Transpiler) VerifyCompiled(ctx context.Context, agentsMdPath, targetDir string) (*CompileResult, error) {
	res, err := t.CompileContext(ctx, agentsMdPath)
	if err != nil {
		return nil, err
	}

	for _, f := range res.Files {
		existing, err := readConfinedText(ctx, targetDir, f.RelativePath)
		if err != nil {
			return nil, fmt.Errorf("target %s missing or unreadable: %w", f.RelativePath, err)
		}
		if !bytes.Equal(bytes.TrimSpace(existing), bytes.TrimSpace([]byte(f.Content))) {
			return nil, fmt.Errorf("target %s is out of sync with %s; run 'praetorctl compile-context' to reconcile", f.RelativePath, agentsMdPath)
		}
	}
	return res, nil
}
