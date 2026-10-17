// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// addPersonaModelTier gives sc's persona, a generated persona the repository edited, the model
// tier of its current template when its front matter declares none (#1426): the line goes at
// the end of the front matter (compiler.AddPersonaModelTier) and every other byte stays, so the
// repository's edits survive the key the audit requires since #1341. The write is bound to the
// bytes read (publishPriorRefresh), so a file edited in between is not overwritten. A dry run
// records the change it would make.
func (s *adoptSession) addPersonaModelTier(ctx context.Context, sc scaffold) error {
	full, err := repoFile(s.repoPath, sc.rel)
	if err != nil {
		return err
	}
	actual, exists, err := contextopt.ObserveSnapshot(ctx, full)
	if err != nil || !exists || !compiler.PersonaLacksModelTier(actual) {
		return ctx.Err()
	}
	tier, err := compiler.PersonaModelTier(sc.content)
	if err != nil {
		return fmt.Errorf("model tier of the %s template: %w", sc.rel, err)
	}
	content, ok := compiler.AddPersonaModelTier(actual, tier)
	if !ok {
		return nil
	}
	if !s.opts.DryRun {
		if err := s.publishPriorRefresh(ctx, full, actual, content, sc); err != nil {
			return fmt.Errorf("add the model tier to %s: %w", sc.rel, err)
		}
	}
	s.planDryRunWrite(sc.rel, content)
	s.report.recordReconciled(sc.rel, fmt.Sprintf("Added the model tier the audit requires (%s: %s) to the edited persona; "+
		"every other line kept", compiler.PersonaModelKey, tier))
	return nil
}

// PersonaModelTierRemedy says how the canonical persona at rel (a slash path below rootDir),
// whose front matter is complete except for its model tier, gets one: adoption refreshes an
// unedited earlier generated persona and adds the tier to an edited one that declares none; a
// persona the repository wrote itself, or one declaring a value outside the tiers, takes the
// line by hand. compile-context and audit both name it (#1426).
func PersonaModelTierRemedy(ctx context.Context, rootDir, rel string) string {
	byHand := "add " + compiler.PersonaModelKey + ": " + compiler.PersonaModelFlash + " or " +
		compiler.PersonaModelKey + ": " + compiler.PersonaModelPro + " to its front matter"
	personas := generatedPersonas()
	for i := 0; i < len(personas) && i < maxTranspileTargets; i++ {
		if personas[i].rel != rel {
			continue
		}
		data, exists, err := contextopt.ObserveSnapshotIn(ctx, rootDir, filepath.FromSlash(rel))
		switch {
		case err != nil || !exists:
			return byHand
		case isPriorRendering(data, priorPersonaDigests[rel]):
			return "the file is an unedited copy of an earlier engine's template; praetorctl adopt replaces it, " +
				"or run the engine the repository pins"
		case compiler.PersonaLacksModelTier(data):
			return "praetorctl adopt (no --force needed) adds the template's model tier and keeps every other line"
		}
		return byHand
	}
	return byHand
}
