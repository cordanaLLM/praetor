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

// earlierPersonaModelTierRemedy adds the recovery path to a model-tier refusal of a generated
// persona (PersonaModelTierRemedy): adoption replaces an unedited earlier copy and adds the tier
// to an edited one. Any other refusal is returned unchanged.
func earlierPersonaModelTierRemedy(ctx context.Context, targetDir string, err error) error {
	if !errors.Is(err, compiler.ErrPersonaModelTier) {
		return err
	}
	personas := generatedPersonas()
	for i := 0; i < len(personas) && i < maxTranspileTargets; i++ {
		rel := personas[i].rel
		if strings.Contains(err.Error(), rel+":") {
			return fmt.Errorf("%w: %s", err, PersonaModelTierRemedy(ctx, targetDir, rel))
		}
	}
	return err
}
