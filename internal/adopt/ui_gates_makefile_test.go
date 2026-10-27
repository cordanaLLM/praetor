// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

func TestUIGatesMakefile_VerifyAllRunsUIGates(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{"name":"ui-app","scripts":{"build":"vite build","test":"vitest run"}}`)
	mustWrite(t, filepath.Join(root, "svelte.config.js"), "export default {};\n")
	plan, err := resolveVerificationPlan(t.Context(), root)
	if err != nil {
		t.Fatalf("resolveVerificationPlan: %v", err)
	}
	makefile := buildMakefile(plan)
	if !util.MakefileHasTarget(makefile, "ui-gates") ||
		!strings.Contains(makefile, "verify-all: compile-context-verify caveman-sources audit ui-gates test") ||
		!strings.Contains(makefile, "$(PRAETORCTL) ui env") ||
		!strings.Contains(makefile, "npx playwright test tests/a11y.spec.ts tests/responsiveness.spec.ts") {
		t.Fatalf("UI Makefile lacks ui-gates target in verify-all:\n%s", makefile)
	}
}

func TestUIGatesMakefile_PackageManagerRunners(t *testing.T) {
	cases := []struct {
		name    string
		manager string
		want    string
	}{
		{name: "pnpm", manager: "pnpm@9.0.0", want: "pnpm exec playwright test tests/a11y.spec.ts tests/responsiveness.spec.ts"},
		{name: "yarn", manager: "yarn@4.1.0", want: "yarn playwright test tests/a11y.spec.ts tests/responsiveness.spec.ts"},
		{name: "bun", manager: "bun@1.1.0", want: "bunx playwright test tests/a11y.spec.ts tests/responsiveness.spec.ts"},
		{name: "npm", manager: "npm@10.5.0", want: "npx playwright test tests/a11y.spec.ts tests/responsiveness.spec.ts"},
	}
	for i := 0; i < len(cases); i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			mustWrite(t, filepath.Join(root, "package.json"), `{"name":"ui-app","packageManager":"`+tc.manager+`","scripts":{"build":"vite build","test":"vitest run"}}`)
			mustWrite(t, filepath.Join(root, "svelte.config.js"), "export default {};\n")
			plan, err := resolveVerificationPlan(t.Context(), root)
			if err != nil {
				t.Fatalf("resolveVerificationPlan: %v", err)
			}
			makefile := buildMakefile(plan)
			if !strings.Contains(makefile, tc.want) {
				t.Fatalf("Makefile for %s missing %q:\n%s", tc.manager, tc.want, makefile)
			}
		})
	}
}

func TestUIGatesMakefile_NonUIUnchanged(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{"name":"node-app","scripts":{"build":"tsc","test":"node --test"}}`)
	plan, err := resolveVerificationPlan(t.Context(), root)
	if err != nil {
		t.Fatalf("resolveVerificationPlan: %v", err)
	}
	if plan.UIGates {
		t.Fatalf("non-UI plan.UIGates = true, want false")
	}
	makefile := buildMakefile(plan)
	if util.MakefileHasTarget(makefile, "ui-gates") || strings.Contains(makefile, "ui-gates") {
		t.Fatalf("non-UI Makefile contains ui-gates:\n%s", makefile)
	}
}

func TestUIGatesMakefile_PriorRenderingReplacedOnRefresh(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{"name":"ui-app","scripts":{"build":"vite build","test":"vitest run"}}`)
	mustWrite(t, filepath.Join(root, "svelte.config.js"), "export default {};\n")
	plan, err := resolveVerificationPlan(t.Context(), root)
	if err != nil {
		t.Fatalf("resolveVerificationPlan: %v", err)
	}
	priorNoUI := buildMakefile(withoutUIGates(plan))
	if !isReplaceableVerificationMakefile(priorNoUI, plan) {
		t.Fatalf("prior UI Makefile without ui-gates isReplaceableVerificationMakefile = false, want true")
	}
	priorExecPrefix := buildMakefileWith(plan, priorVerificationRecipePrefix)
	if !isReplaceableVerificationMakefile(priorExecPrefix, plan) {
		t.Fatalf("prior UI Makefile with @ prefix isReplaceableVerificationMakefile = false, want true")
	}
	appended, err := appendVerificationTargets("custom:\n\t@true\n", plan)
	if err != nil {
		t.Fatalf("appendVerificationTargets: %v", err)
	}
	if !util.MakefileHasTarget(appended, "ui-gates") || !strings.Contains(appended, "verify-all: ui-gates") {
		t.Fatalf("appendVerificationTargets missing ui-gates:\n%s", appended)
	}
}
