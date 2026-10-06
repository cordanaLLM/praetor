// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
)

// runtimeLanguages maps the runtimes and source languages the verification plan detects to the
// languages a HISS directive clause can name. A native build (meson, CMake) is C or C++, and so
// are C or C++ sources without one (sourceLanguageC). Every other detected runtime (Node, .NET,
// the JVM builds, Flutter, Zig) is a language no clause names. A Zig build that compiles C or
// C++ is C/C++ through those sources, since build.zig is a program that declares no language.
var runtimeLanguages = map[string]hisscatalog.Language{
	"go":               hisscatalog.LanguageGo,
	"cargo":            hisscatalog.LanguageRust,
	"python":           hisscatalog.LanguagePython,
	"meson.build":      hisscatalog.LanguageC,
	"core/meson.build": hisscatalog.LanguageC,
	"CMakeLists.txt":   hisscatalog.LanguageC,
	sourceLanguageC:    hisscatalog.LanguageC,
}

// planLanguages returns the languages of the runtimes and source languages plan detected; none
// detected is the unknown set (zero), under which every language clause renders with its label.
func planLanguages(plan *VerificationPlan) hisscatalog.Language {
	var languages hisscatalog.Language
	if plan == nil {
		return languages
	}
	for _, runtime := range slices.Concat(plan.Runtimes, plan.SourceLanguages) {
		language, ok := runtimeLanguages[runtime]
		if !ok {
			language = hisscatalog.LanguageOther
		}
		languages |= language
	}
	return languages
}

// repositoryFacts is what a repository's HISS rows depend on before its policy is known: the
// languages its verification plan found, the audit ceiling (config.AuditMaxFuncLOC) and the
// exceptions it declares and documents.
func repositoryFacts(plan *VerificationPlan, exceptions hisscatalog.Exception) hisscatalog.Facts {
	return hisscatalog.Facts{Languages: planLanguages(plan), CeilingFuncLOC: config.AuditMaxFuncLOC, Exceptions: exceptions}
}

// withPolicy completes facts with every HISS-04 limit of complexity, the effective policy the
// audit resolves: the function length it enforces and the cyclomatic, cognitive and statement
// limits, so a stricter override of any of them reaches the rows, not the function length
// alone (#321). Every renderer of HISS rows reads the policy through it: the AGENTS.md harness
// (hissFacts), the Paperclip harness adoption writes (paperclipFacts), and the one
// `praetorctl paperclip harness` writes and `praetorctl audit` compares (RepositoryHISSFacts).
// A zero complexity is an unresolved policy and leaves facts as they were. The limits come from
// ComplexityPolicy.ScanOptions, the one mapping of a policy onto the HISS-04 limits the audit's
// scan measures against, so the rows state exactly what the scan reads.
func withPolicy(facts hisscatalog.Facts, complexity config.ComplexityPolicy) hisscatalog.Facts {
	limits := complexity.ScanOptions(hiss.ScanOptions{})
	facts.MaxFuncLOC, facts.Complexity = limits.MaxFuncLOC, limits.Complexity
	return facts
}

// hissFacts is what this repository's AGENTS.md rows depend on: repositoryFacts and, once the
// policy-catalog step resolved the effective policy, its HISS-04 limits (withPolicy).
func (s *adoptSession) hissFacts() hisscatalog.Facts {
	facts := repositoryFacts(s.verification, s.exceptions)
	if s.policy != nil {
		facts = withPolicy(facts, s.policy.Policy.Complexity)
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
// a caller outside an adoption run (`praetorctl paperclip harness`, and `praetorctl audit`, which
// compares the harness on disk with that synthesis) that renders them the way adoption does: the
// languages the verification planner detects from project markers and C/C++ sources, the
// exceptions the manifest declares and documents, and the HISS-04 limits the audit enforces
// (withPolicy), resolved by config.ResolveRepositoryPolicy as `praetorctl audit` resolves them.
// A repository whose policy does not resolve (no manifest, no lock, or a resolution error)
// states the audit ceiling and the HISS-04 defaults instead; each returned warning names a
// declaration or policy that was not read.
//
// The language walk runs under limits exactly as adoption's does: nil selects the defaults, and
// a caller passes the operator's --verification-max-* overrides so a large repository is read
// as far as adoption reads it (issue #535).
func RepositoryHISSFacts(ctx context.Context, root string, limits *VerificationLimits) (hisscatalog.Facts, []string, error) {
	plan, err := ObserveVerificationPlanWithLimits(ctx, root, limits)
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
		warnings = append(warnings, "HISS-04 limits not resolved, audit ceiling stated: "+err.Error())
	case policy != nil && notice == "":
		facts = withPolicy(facts, policy.Complexity)
	}
	return facts, warnings, nil
}
