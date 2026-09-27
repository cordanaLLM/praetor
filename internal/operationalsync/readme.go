package operationalsync

import (
	"fmt"
	"maps"

	"github.com/cordanaLLM/praetor/internal/readmegovernance"
)

// The README governance overlay. The engine's README carries the managed governance block
// (internal/readmegovernance) rendered for the public source; with the documentation contract
// enabled its badge links to the source repository. A fork's own audit verifies the block for
// the fork's manifest identity, so plan and prepare render it for the owner with the renderer
// adoption uses (BUG-1023). README.md is already a funding surface: the rebound block rides
// that flow, rendered into op.surfaces, written, staged and verified byte for byte.

// rebindReadme renders the managed governance block of files' README for the owner identity,
// keeping every fact the source block records (debt baseline, documentation contract) and
// every byte outside the block. A tree without a README, or a README without a managed block,
// is left alone: like adoption, the overlay never invents a README, and a block the source
// does not carry has no recorded state to render. A source block this renderer cannot read
// back, hand-edited or written by another engine version, stops the operation: the fork's
// audit would refuse whatever the overlay rendered from it.
func rebindReadme(files map[string][]byte, source, owner identity) error {
	raw, ok := files[readmegovernance.File]
	if !ok {
		return nil
	}
	state, managed, err := readmegovernance.RecordedState(string(raw), source.Owner, source.Name)
	if err != nil {
		return fmt.Errorf("%s governance block of the reviewed source: %w; run prepare with the praetorctl built at the reviewed source", readmegovernance.File, err)
	}
	if !managed {
		return nil
	}
	if state.DocumentationEnabled {
		state.RepositoryOwner, state.RepositoryName = owner.Owner, owner.Name
	}
	out, _, err := readmegovernance.Reconcile(string(raw), state)
	if err != nil {
		return fmt.Errorf("render %s governance block for the owner: %w", readmegovernance.File, err)
	}
	files[readmegovernance.File] = []byte(out)
	return nil
}

// withoutGovernanceBody returns files with its README's managed block body removed, for the
// owner surface comparison. The block is engine output the overlay renders again, never owner
// content it carries forward, so the owner's copy may be the source's rendering (a fork
// synced before BUG-1023), the owner rendering, or one an older engine wrote. Everything
// outside the block is still compared byte for byte.
func withoutGovernanceBody(files map[string][]byte) (map[string][]byte, error) {
	raw, ok := files[readmegovernance.File]
	if !ok {
		return files, nil
	}
	stripped, err := readmegovernance.WithoutBlockBody(string(raw))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", readmegovernance.File, err)
	}
	out := maps.Clone(files)
	out[readmegovernance.File] = []byte(stripped)
	return out, nil
}
