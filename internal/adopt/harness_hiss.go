// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
)

// runtimeLanguages maps the runtimes the verification plan detects to the languages a HISS
// directive clause can name. A native build (meson, CMake) is C or C++. Every other detected
// runtime (Node, .NET, the JVM builds, Flutter) is a language no clause names.
var runtimeLanguages = map[string]hisscatalog.Language{
	"go":               hisscatalog.LanguageGo,
	"cargo":            hisscatalog.LanguageRust,
	"python":           hisscatalog.LanguagePython,
	"meson.build":      hisscatalog.LanguageC,
	"core/meson.build": hisscatalog.LanguageC,
	"CMakeLists.txt":   hisscatalog.LanguageC,
}

// planLanguages returns the languages of the runtimes plan detected; none detected is the
// unknown set (zero), under which every language clause renders with its label.
func planLanguages(plan *VerificationPlan) hisscatalog.Language {
	var languages hisscatalog.Language
	if plan == nil {
		return languages
	}
	for _, runtime := range plan.Runtimes {
		language, ok := runtimeLanguages[runtime]
		if !ok {
			language = hisscatalog.LanguageOther
		}
		languages |= language
	}
	return languages
}

// repositoryFacts is what a repository's HISS rows depend on before its function length is
// known: the languages its verification plan found, the audit ceiling (config.AuditMaxFuncLOC)
// and the exceptions it declares and documents.
func repositoryFacts(plan *VerificationPlan, exceptions hisscatalog.Exception) hisscatalog.Facts {
	return hisscatalog.Facts{Languages: planLanguages(plan), CeilingFuncLOC: config.AuditMaxFuncLOC, Exceptions: exceptions}
}

// hissFacts is what this repository's AGENTS.md rows depend on: repositoryFacts and, once the
// policy-catalog step resolved the effective policy, the function length its audit enforces
// (adoptionScanLimit).
func (s *adoptSession) hissFacts() hisscatalog.Facts {
	facts := repositoryFacts(s.verification, s.exceptions)
	if s.policy != nil {
		facts.MaxFuncLOC = adoptionScanLimit(s)
	}
	return facts
}

// harnessExceptions is the HISS exceptions the harnesses state for the cleanup-goto exception the
// manifest declares and documents (config.Manifest.CleanupGotoException), the reading the audit's
// scan honours too, so the harness never states an exception the audit does not grant.
func harnessExceptions(cleanupGoto hiss.CleanupGoto) hisscatalog.Exception {
	if cleanupGoto.Enabled {
		return hisscatalog.ExceptionCleanupGoto
	}
	return 0
}

// RepositoryHISSFacts reports what the HISS directives of the repository at root depend on, for
// a caller outside an adoption run (`praetorctl paperclip harness`) that renders them the way
// adoption does: the languages the verification planner detects from project markers, the
// exceptions the manifest declares and documents, and the function length the audit enforces,
// resolved by config.ResolveRepositoryPolicy as `praetorctl audit` resolves it. A repository
// whose policy does not resolve (no manifest, no lock, or a resolution error) states the audit
// ceiling instead; each returned warning names a declaration or policy that was not read.
func RepositoryHISSFacts(ctx context.Context, root string) (hisscatalog.Facts, []string, error) {
	plan, err := ObserveVerificationPlan(ctx, root)
	if err != nil {
		return hisscatalog.Facts{}, nil, fmt.Errorf("detect repository languages: %w", err)
	}
	manifest, err := loadDeclaredManifest(ctx, root)
	if err != nil {
		return hisscatalog.Facts{}, nil, err
	}
	var warnings []string
	cleanupGoto, warning := manifest.CleanupGotoException(root)
	if warning != "" {
		warnings = append(warnings, warning)
	}
	facts := repositoryFacts(plan, harnessExceptions(cleanupGoto))
	if manifest == nil {
		return facts, warnings, nil
	}
	policy, notice, err := config.ResolveRepositoryPolicy(ctx, filepath.Join(root, manifestFile), manifest)
	switch {
	case err != nil:
		warnings = append(warnings, "function length not resolved, audit ceiling stated: "+err.Error())
	case policy != nil && notice == "":
		facts.MaxFuncLOC = policy.Complexity.MaxFuncLOC
	}
	return facts, warnings, nil
}
