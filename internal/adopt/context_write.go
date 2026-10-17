package adopt

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
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
	err = compiler.CompileContextProjections(ctx, w, tr, source, targetDir)
	return earlierPersonaModelTierRemedy(ctx, targetDir, err)
}

// earlierPersonaModelTierRemedy adds the recovery path only when the rejected canonical persona
// is byte-for-byte one of adoption's earlier generated texts. An edited persona keeps the compiler
// error unchanged because adoption preserves it rather than replacing it.
func earlierPersonaModelTierRemedy(ctx context.Context, targetDir string, err error) error {
	if !errors.Is(err, compiler.ErrPersonaModelTier) {
		return err
	}
	personas := generatedPersonas()
	for i := 0; i < len(personas) && i < maxTranspileTargets; i++ {
		rel := personas[i].rel
		if !strings.Contains(err.Error(), rel+":") {
			continue
		}
		data, exists, readErr := contextopt.ObserveSnapshotIn(ctx, targetDir, filepath.FromSlash(rel))
		if readErr != nil || !exists || !isPriorRendering(data, priorPersonaDigests[rel]) {
			return err
		}
		return fmt.Errorf("%w: the file is an unedited copy of an earlier engine's template; "+
			"praetorctl adopt replaces it, or run the engine the repository pins", err)
	}
	return err
}
