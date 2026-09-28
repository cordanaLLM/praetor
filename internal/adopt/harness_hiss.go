// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"

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

// hissFacts is what this repository's HISS rows depend on: the languages its verification plan
// found, and, once the policy-catalog step resolved the effective policy, the function length
// its audit enforces (adoptionScanLimit).
func (s *adoptSession) hissFacts() hisscatalog.Facts {
	facts := hisscatalog.Facts{Languages: planLanguages(s.verification)}
	if s.policy != nil {
		facts.MaxFuncLOC = adoptionScanLimit(s)
	}
	return facts
}

// RepositoryLanguages reports the languages of the repository at root as the verification
// planner detects them, for a caller outside an adoption run (`praetorctl paperclip harness`)
// that renders HISS directives the way adoption does. It reads project markers only.
func RepositoryLanguages(ctx context.Context, root string) (hisscatalog.Language, error) {
	plan, err := ObserveVerificationPlan(ctx, root)
	if err != nil {
		return 0, fmt.Errorf("detect repository languages: %w", err)
	}
	return planLanguages(plan), nil
}
