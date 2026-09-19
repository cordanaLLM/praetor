package adopt

import (
	"context"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	documentationMakefileBegin = "# BEGIN praetor documentation gate"
	documentationMakefileEnd   = "# END praetor documentation gate"
	maxMakefileLines           = 4096
)

// DocumentationMakefileBlock is the exact local-gate wiring audit requires.
func DocumentationMakefileBlock() string {
	return documentationMakefileBegin + "\n" +
		".PHONY: docs-lint\n" +
		"verify-all: docs-lint\n" +
		"docs-lint:\n" +
		"\t@node tools/markdownlint/verify.mjs\n" +
		documentationMakefileEnd + "\n"
}

type documentationMarkerState struct {
	lines                []string
	begin, end           int
	beginCount, endCount int
}

// DocumentationMakefileMarkersPresent reports whether a Makefile carries an
// exact Praetor marker line. Prose merely mentioning a marker is operator data.
func DocumentationMakefileMarkersPresent(data string) (bool, error) {
	normalized, _, err := util.NormalizeLineEndingsStrict(data)
	if err != nil {
		return false, fmt.Errorf("makefile line endings are inconsistent: %w", err)
	}
	state, err := scanDocumentationMakefileMarkers(normalized)
	if err != nil {
		return false, err
	}
	return state.beginCount > 0 || state.endCount > 0, nil
}

func scanDocumentationMakefileMarkers(data string) (documentationMarkerState, error) {
	state := documentationMarkerState{lines: strings.Split(data, "\n"), begin: -1, end: -1}
	if len(state.lines) > maxMakefileLines {
		return state, fmt.Errorf("makefile exceeds %d lines", maxMakefileLines)
	}
	for index := 0; index < len(state.lines) && index < maxMakefileLines; index++ {
		switch state.lines[index] {
		case documentationMakefileBegin:
			state.begin, state.beginCount = index, state.beginCount+1
		case documentationMakefileEnd:
			state.end, state.endCount = index, state.endCount+1
		}
	}
	return state, nil
}

func mergeDocumentationMakefile(existing string, force bool) (string, error) {
	normalized, crlf, err := util.NormalizeLineEndingsStrict(existing)
	if err != nil {
		return "", fmt.Errorf("makefile line endings are inconsistent: %w", err)
	}
	merged, err := mergeDocumentationMakefileLF(normalized, force)
	if err != nil {
		return "", err
	}
	return util.RestoreLineEndings(merged, crlf), nil
}

func mergeDocumentationMakefileLF(existing string, force bool) (string, error) {
	block := DocumentationMakefileBlock()
	state, err := scanDocumentationMakefileMarkers(existing)
	if err != nil {
		return "", err
	}
	if documentationMakefileBlockExact(state, block) {
		return existing, nil
	}
	if state.beginCount > 1 || state.endCount > 1 {
		return "", fmt.Errorf("makefile contains duplicate Praetor documentation gate markers")
	}
	if state.beginCount == 1 || state.endCount == 1 {
		return replaceDocumentationMakefileBlock(existing, block, force)
	}
	if mayDefineTarget(existing, "docs-lint") {
		return "", fmt.Errorf("makefile may define target docs-lint outside the Praetor-managed block")
	}
	base := strings.TrimRight(existing, "\n")
	if base == "" {
		return block, nil
	}
	return base + "\n\n" + block, nil
}

func documentationMakefileBlockExact(state documentationMarkerState, block string) bool {
	return state.beginCount == 1 && state.endCount == 1 && state.end >= state.begin &&
		state.end < len(state.lines)-1 &&
		strings.Join(state.lines[state.begin:state.end+1], "\n") == strings.TrimSuffix(block, "\n")
}

func replaceDocumentationMakefileBlock(existing, block string, force bool) (string, error) {
	state, err := scanDocumentationMakefileMarkers(existing)
	if err != nil {
		return "", err
	}
	if state.beginCount != 1 || state.endCount != 1 || state.end < state.begin {
		return "", fmt.Errorf("makefile contains an incomplete Praetor documentation gate block")
	}
	if !force {
		return "", fmt.Errorf("makefile Praetor documentation gate block was edited; review it and rerun adopt --force")
	}
	replacement := strings.Split(strings.TrimSuffix(block, "\n"), "\n")
	lines := make([]string, 0, len(state.lines)-(state.end-state.begin+1)+len(replacement))
	lines = append(lines, state.lines[:state.begin]...)
	lines = append(lines, replacement...)
	lines = append(lines, state.lines[state.end+1:]...)
	return strings.Join(lines, "\n"), nil
}

func removeDocumentationMakefileBlock(existing string) (string, bool, error) {
	normalized, crlf, err := util.NormalizeLineEndingsStrict(existing)
	if err != nil {
		return "", false, fmt.Errorf("makefile line endings are inconsistent: %w", err)
	}
	cleaned, removed, err := removeDocumentationMakefileBlockLF(normalized)
	if err != nil {
		return "", false, err
	}
	return util.RestoreLineEndings(cleaned, crlf), removed, nil
}

func removeDocumentationMakefileBlockLF(existing string) (string, bool, error) {
	state, err := scanDocumentationMakefileMarkers(existing)
	if err != nil {
		return "", false, err
	}
	if state.beginCount == 0 && state.endCount == 0 {
		return existing, false, nil
	}
	block := DocumentationMakefileBlock()
	if state.beginCount != 1 || state.endCount != 1 || state.end < state.begin || state.end >= len(state.lines)-1 ||
		strings.Join(state.lines[state.begin:state.end+1], "\n") != strings.TrimSuffix(block, "\n") {
		return "", false, fmt.Errorf("refusing to remove ambiguous or edited Praetor documentation gate block")
	}
	prefix := strings.TrimRight(strings.Join(state.lines[:state.begin], "\n"), "\n")
	suffix := strings.TrimLeft(strings.Join(state.lines[state.end+1:], "\n"), "\n")
	switch {
	case prefix == "":
		return suffix, true, nil
	case suffix == "":
		return prefix + "\n", true, nil
	default:
		return prefix + "\n" + suffix, true, nil
	}
}

func reconcileDocumentationMakefile(ctx context.Context, s *adoptSession) error {
	full, err := repoFile(s.repoPath, makefileName)
	if err != nil {
		return err
	}
	data, exists, err := contextopt.ObserveSnapshot(ctx, full)
	if err != nil {
		return err
	}
	merged, err := mergeDocumentationMakefile(string(data), s.opts.Force)
	if err != nil {
		return err
	}
	if merged == string(data) {
		s.report.recordReconciled(makefileName, "Documentation gate already attached to verify-all")
		return nil
	}
	if !s.opts.DryRun {
		err = contextopt.ReplaceSnapshot(ctx, full, []byte(merged),
			contextopt.ReplaceOptions{Expected: data, Exists: exists, Mode: filePerm})
	}
	if err != nil {
		return err
	}
	s.report.recordReconciledAs(makefileName, actionAppend, "Attached locked documentation gate to verify-all")
	return nil
}

// Only exact historical Praetor output is eligible for automatic replacement.
// Arbitrary user recipes, including edited generated files, remain untouched.
func isLegacyVerificationMakefile(data string) bool {
	data = withoutDocumentationMakefileBlock(data)
	if data == legacyVerificationStub || data == strings.TrimPrefix(legacyVerificationStub, "\n") {
		return true
	}
	for _, commands := range [][2]string{
		{"go test -v -race ./...", "go build -v ./..."},
		{"meson test -C core/build --suite=fast", "meson compile -C core/build"},
	} {
		if data == legacyVerificationMakefile(commands[0], commands[1]) {
			return true
		}
	}
	return false
}

// isPriorGeneratedMakefile reports whether data is exactly the Makefile plan rendered before
// command lines began with exec. Like the historical forms above it is replaced, so repositories
// adopted earlier receive the corrected recipes; an edited copy is not exact and stays untouched.
// Where both renderings coincide -- a plan with no runnable commands -- the file is already current.
func isPriorGeneratedMakefile(data string, plan *VerificationPlan) bool {
	data = withoutDocumentationMakefileBlock(data)
	prior := buildMakefileWith(plan, priorVerificationRecipePrefix)
	return data == prior && prior != buildMakefile(plan)
}

// isReplaceableVerificationMakefile reports whether data is earlier Praetor output that adoption
// replaces with the current rendering.
func isReplaceableVerificationMakefile(data string, plan *VerificationPlan) bool {
	return isLegacyVerificationMakefile(data) || isPriorGeneratedMakefile(data, plan)
}

const legacyVerificationStub = "\n.PHONY: all verify-all audit compile-context build test\n\nverify-all:\n\t@echo \"Running verification...\"\n\ncompile-context:\n\t@standardsctl compile-context\n\naudit:\n\t@standardsctl audit\n\ntest:\n\t@go test -v -race ./...\n\nbuild:\n\t@go build -v ./...\n"

func legacyVerificationMakefile(test, build string) string {
	return ".PHONY: all verify-all compile-context compile-context-verify audit build test\n\n" +
		"all: build\n\nverify-all: compile-context-verify audit test\n\n" +
		"compile-context:\n\t@standardsctl compile-context\n\n" +
		"compile-context-verify:\n\t@standardsctl compile-context --verify\n\n" +
		"audit:\n\t@standardsctl audit\n\ntest:\n\t@" + test + "\n\nbuild:\n\t@" + build + "\n"
}

func preserveCustomVerification(plan *VerificationPlan, data []byte) {
	text := withoutDocumentationMakefileBlock(string(data))
	if !mayDefineVerificationTarget(text) || isReplaceableVerificationMakefile(text, plan) || text == buildMakefile(plan) {
		return
	}
	plan.Status = verificationPreserved
	plan.Reasons = append(plan.Reasons, "Existing custom verify-all is preserved; execute and review it before claiming project verification.")
	plan.Build = [][]string{}
	plan.Test = [][]string{{"make", "verify-all"}}
}

func withoutDocumentationMakefileBlock(data string) string {
	cleaned, removed, err := removeDocumentationMakefileBlock(data)
	if err == nil && removed {
		return cleaned
	}
	return data
}

// makefileTargetNames returns the target names a Makefile line declares, and none when the line
// declares no rule. Make reads a run of colons followed by "=" as a variable assignment -- ":=",
// "::=" and ":::=" -- so "verify-all := x" binds a variable and leaves make with no verify-all
// rule, while "verify-all:: dep" is a double-colon rule and does declare one.
func makefileTargetNames(line string) []string {
	if strings.HasPrefix(line, "\t") || strings.HasPrefix(strings.TrimSpace(line), "#") {
		return nil
	}
	left, rest, ok := strings.Cut(line, ":")
	if !ok || strings.HasPrefix(strings.TrimLeft(rest, ":"), "=") {
		return nil
	}
	return strings.Fields(left)
}

func hasVerificationTarget(data, target string) bool {
	lines := strings.Split(data, "\n")
	for index := 0; index < len(lines) && index < maxMakefileLines; index++ {
		for _, name := range makefileTargetNames(lines[index]) {
			if name == target {
				return true
			}
		}
	}
	return false
}

func appendVerificationTargets(existing string, plan *VerificationPlan) (string, error) {
	normalized, crlf, err := util.NormalizeLineEndingsStrict(existing)
	if err != nil {
		return "", fmt.Errorf("makefile line endings are inconsistent: %w", err)
	}
	var result strings.Builder
	result.WriteString(normalized)
	result.WriteString("\n# Praetor declared verification; existing project recipes remain unchanged.\n" +
		util.MakefileCLIVariable + ".PHONY: verify-all\nverify-all:\n\t@$(PRAETORCTL) compile-context --verify\n\t@$(PRAETORCTL) audit\n")
	result.WriteString(verificationRecipe(plan, plan.Build))
	result.WriteString(verificationRecipe(plan, plan.Test))
	for _, target := range []string{"compile-context", "audit"} {
		if !hasVerificationTarget(normalized, target) {
			result.WriteString("\n" + target + ":\n\t@$(PRAETORCTL) " + target + "\n")
		}
	}
	return util.RestoreLineEndings(result.String(), crlf), nil
}

// makefileLineIsAmbiguous reports whether a line may define targets only Make can resolve: an
// include, a directive, an $(eval ...) call, or a computed or pattern target name. The line is
// already trimmed and is not a recipe line.
func makefileLineIsAmbiguous(line string) bool {
	fields := strings.Fields(line)
	if len(fields) == 0 || strings.HasPrefix(line, "#") {
		return false
	}
	switch fields[0] {
	case "include", "-include", "sinclude", "define", "override":
		return true
	}
	if strings.Contains(line, "$(eval") || strings.Contains(line, "${eval") {
		return true
	}
	for _, name := range makefileTargetNames(line) {
		if strings.ContainsAny(name, "$%") {
			return true
		}
	}
	return false
}

// Includes, generated target names and pattern rules require Make evaluation.
// Never append a potentially overriding recipe when ownership is ambiguous.
func mayDefineVerificationTarget(data string) bool {
	return mayDefineTarget(data, "verify-all")
}

func mayDefineTarget(data, target string) bool {
	if hasVerificationTarget(data, target) {
		return true
	}
	lines := strings.Split(data, "\n")
	if len(lines) > maxMakefileLines {
		return true
	}
	for index := 0; index < len(lines) && index < maxMakefileLines; index++ {
		if strings.HasPrefix(lines[index], "\t") {
			continue
		}
		if makefileLineIsAmbiguous(strings.TrimSpace(lines[index])) {
			return true
		}
	}
	return false
}
