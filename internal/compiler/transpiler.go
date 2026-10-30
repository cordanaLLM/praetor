package compiler

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

const MaxLineBudget = agentcontext.MaxLineBudget

type TargetFile = agentcontext.TargetFile
type CompileResult = agentcontext.CompileResult

// Transpiler compiles canonical AGENTS.md into vendor-native agent configurations and writes or
// verifies the target files under a bounded deadline.
type Transpiler struct {
	MaxLines int
	Clients  []string
	Env      *agentcontext.RenderEnv
	// Bound overrides DefaultAgentBound for Compile, WriteOutputs and Verify when positive.
	Bound           time.Duration
	writeTarget     func(ctx context.Context, root, rel string, data []byte) error
	readTarget      func(ctx context.Context, root, rel string) ([]byte, error)
	readImport      func(string) (string, error)
	projectDocBytes int
	projectDocChain string
}

func NewTranspiler() *Transpiler {
	return &Transpiler{MaxLines: MaxLineBudget, Bound: DefaultAgentBound}
}

func (t *Transpiler) bound() time.Duration {
	if t != nil && t.Bound > 0 {
		return t.Bound
	}
	return DefaultAgentBound
}

func (t *Transpiler) targetWriter() func(ctx context.Context, root, rel string, data []byte) error {
	if t != nil && t.writeTarget != nil {
		return t.writeTarget
	}
	return writeConfinedText
}

func (t *Transpiler) targetReader() func(ctx context.Context, root, rel string) ([]byte, error) {
	if t != nil && t.readTarget != nil {
		return t.readTarget
	}
	return readConfinedText
}

// NotApplicableLine is the report line, without a newline, for a context file or persona
// directory agent_clients leaves out. compile-context and the MCP standards_compile_context
// tool print the same line.
func NotApplicableLine(rel string) string {
	return fmt.Sprintf("  [NOT_APPLICABLE] %-35s (not selected by agent_clients)", rel)
}

// Compile reads the canonical AGENTS.md and synthesizes vendor-specific files.
func (t *Transpiler) Compile(agentsMdPath string) (*CompileResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), t.bound())
	defer cancel()
	return t.CompileContext(ctx, agentsMdPath)
}

// CompileContext reads agentsMdPath and compiles the projections its repository selects.
// Unless t.Clients is set, the selection is agent_clients in the manifest beside the source,
// the same manifest that governs the text register block, so compile, verify and audit agree
// on which projections exist.
func (t *Transpiler) CompileContext(ctx context.Context, agentsMdPath string) (*CompileResult, error) {
	bound := t.bound()
	contentBytes, err := contextopt.ReadSnapshot(ctx, agentsMdPath)
	if dErr, _ := targetDeadlineError(ctx, bound, agentsMdPath, err); dErr != nil {
		return nil, fmt.Errorf("failed to read source %s: %w", agentsMdPath, dErr)
	}
	selected, err := t.forRepository(ctx, filepath.Dir(agentsMdPath))
	if dErr, _ := targetDeadlineError(ctx, bound, agentsMdPath, err); dErr != nil {
		return nil, dErr
	}
	res, err := selected.CompileContent(string(contentBytes))
	if err != nil {
		return nil, err
	}
	res.SourcePath = agentsMdPath
	return res, nil
}

// forRepository binds the client selection, confined import reader and known nested project
// document sizes to a copy. Compile and verify use this same repository view.
func (t *Transpiler) forRepository(ctx context.Context, root string) (*Transpiler, error) {
	selected := *t
	if selected.Clients == nil {
		clients, err := declaredAgentClients(ctx, root)
		if err != nil {
			return nil, err
		}
		selected.Clients = clients
	}
	selected.readImport = func(rel string) (string, error) {
		data, err := readConfinedText(ctx, root, rel)
		return string(data), err
	}
	size, chain, err := largestNestedProjectDocChain(ctx, root)
	if err != nil {
		return nil, err
	}
	selected.projectDocBytes, selected.projectDocChain = size, chain
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
	leaf := agentcontext.Transpiler{MaxLines: t.MaxLines, Clients: t.Clients, Env: t.Env,
		ReadImport: t.readImport, ProjectDocBytes: t.projectDocBytes, ProjectDocChain: t.projectDocChain}
	return leaf.CompileContent(content)
}

// maxContextChainDepth bounds ancestor directory traversal for one nested context file (HISS-02).
const maxContextChainDepth = 64

// largestNestedProjectDocChain returns the bytes of the largest chain of tracked nested AGENTS.md
// files on one path down from the project root, and the deepest file of that chain. A client
// loads the root file and the files on the path to its working directory, never a sibling
// package's (Codex: codex-rs/core/src/agents_md.rs, rust-v0.165.0), so siblings do not add up
// (order engine-golusoris-gate-skew-20261030). Files removed from the working tree contribute
// no bytes, matching the nested context lint.
func largestNestedProjectDocChain(ctx context.Context, root string) (int, string, error) {
	paths, err := ListNestedContextFiles(ctx, root)
	if err != nil {
		return 0, "", err
	}
	sizes := make(map[string]int, len(paths))
	for i := 0; i < len(paths) && i < MaxNestedContextFiles; i++ {
		data, err := readConfinedText(ctx, root, paths[i])
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return 0, "", fmt.Errorf("read project document %s: %w", paths[i], err)
		}
		sizes[paths[i]] = len(data)
	}
	largest, deepest := 0, ""
	for i := 0; i < len(paths) && i < MaxNestedContextFiles; i++ {
		if chain := projectDocChainBytes(paths[i], sizes); chain > largest {
			largest, deepest = chain, paths[i]
		}
	}
	return largest, deepest, nil
}

// projectDocChainBytes sums the nested AGENTS.md files from rel's directory up to the root.
func projectDocChainBytes(rel string, sizes map[string]int) int {
	chain := 0
	dir := path.Dir(rel)
	for depth := 0; depth < maxContextChainDepth && dir != "." && dir != "/"; depth++ {
		chain += sizes[path.Join(dir, contextFileName)]
		dir = path.Dir(dir)
	}
	return chain
}

// WriteOutputs writes compiled files to targetDir.
func (t *Transpiler) WriteOutputs(result *CompileResult, targetDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), t.bound())
	defer cancel()
	return t.WriteOutputsContext(ctx, result, targetDir)
}

func (t *Transpiler) WriteOutputsContext(ctx context.Context, result *CompileResult, targetDir string) error {
	if ctx == nil {
		return errors.New("output writes require a context")
	}
	if result == nil || len(result.Files) > MaxAgentFiles {
		return errors.New("invalid or oversized compiled outputs")
	}
	bound := t.bound()
	if dErr, _ := targetDeadlineError(ctx, bound, firstTargetPath(result.Files, targetDir), ctx.Err()); dErr != nil {
		return dErr
	}
	files := make([]projectionFile, 0, len(result.Files))
	for _, file := range result.Files {
		files = append(files, projectionFile{rel: file.RelativePath, data: []byte(file.Content)})
	}
	if err := checkProjectionFilesBounded(ctx, bound, targetDir, files); err != nil {
		return err
	}
	// The target directory is the operator's chosen boundary, so it is created the way every
	// other root is (contextopt.EnsureDirectory); everything below it is written confined.
	if err := contextopt.EnsureDirectory(ctx, targetDir, projectedDirPerm); err != nil {
		dErr, _ := targetDeadlineError(ctx, bound, firstTargetPath(result.Files, targetDir), err)
		return dErr
	}
	return writeProjectionFilesWith(ctx, bound, targetDir, files, t.targetWriter())
}

func firstTargetPath(files []TargetFile, fallback string) string {
	if len(files) > 0 && files[0].RelativePath != "" {
		return files[0].RelativePath
	}
	return fallback
}

// Verify checks that existing target files match compiled output without modification.
func (t *Transpiler) Verify(agentsMdPath string, targetDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), t.bound())
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
	bound := t.bound()
	read := t.targetReader()
	for _, f := range res.Files {
		existing, err := read(ctx, targetDir, f.RelativePath)
		if err := verifyTargetReadError(ctx, bound, f.RelativePath, err); err != nil {
			return nil, err
		}
		if equal, strict := projectionMatches(existing, []byte(f.Content)); !equal {
			return nil, fmt.Errorf("target %s is out of sync with %s%s; run 'praetorctl compile-context' to reconcile",
				f.RelativePath, agentsMdPath, util.ByteExactNote(strict))
		}
	}
	return res, nil
}
