package adopt

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
)

// CompileAgentContext is the agent context write every command shares: it makes Git ignore the
// evidence directory the text register block names (config.RegisterPolicy.EvidenceDir, through
// ReconcileEvidenceIgnore) for the repository holding source, then writes and lints every agent
// context surface (compiler.CompileContextProjections). A register policy that does not load or
// a refused ignore reconciliation writes no context file. The CLI's compile-context and init and
// the MCP standards_compile_context write all call it, so each one leaves a repository
// compile-context --verify accepts, or fails saying why.
func CompileAgentContext(ctx context.Context, w io.Writer, tr *compiler.Transpiler, source, targetDir string) error {
	dir := filepath.Dir(source)
	authority, err := config.LoadCheckedRegisterAuthority(ctx, dir)
	if err != nil {
		return fmt.Errorf("compiled no agent context: %w", err)
	}
	if err := ReconcileEvidenceIgnore(ctx, w, dir, authority.Policy().EvidenceDir()); err != nil {
		return fmt.Errorf("compiled no agent context: %w", err)
	}
	return compiler.CompileContextProjections(ctx, w, tr, source, targetDir)
}
