package adopt

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	documentationMakefileBegin = "# BEGIN praetor documentation gate"
	documentationMakefileEnd   = "# END praetor documentation gate"
	// maxMakefileLines bounds the marker scan with the bound the shared Makefile reader scans
	// under, so one Makefile is read to the same line by both.
	maxMakefileLines = util.MaxMakefileLines
	// verificationTarget is the Makefile target adoption declares as the verification entrypoint.
	verificationTarget = "verify-all"
)

// documentationMakefileTargets are the targets the documentation block defines: the Markdown
// gate and the figure checks (docs/adr/0016-figures-for-adopters.md, section 5).
var documentationMakefileTargets = []string{"docs-lint", "docs-figures"}

// DocumentationMakefileBlock is the exact local-gate wiring audit requires. docs-figures runs the
// figure engine's check and sources commands, which skip, saying why, in a repository without a
// figure, so the target needs no condition of its own.
func DocumentationMakefileBlock() string {
	return documentationMakefileBegin + "\n" +
		".PHONY: docs-lint docs-figures\n" +
		"verify-all: docs-lint docs-figures\n" +
		"docs-lint:\n" +
		"\t@node tools/markdownlint/verify.mjs\n" +
		"docs-figures:\n" +
		"\t@node tools/figures/build.mjs check\n" +
		"\t@node tools/figures/build.mjs sources\n" +
		documentationMakefileEnd + "\n"
}

// priorDocumentationMakefileBlocks are the exact blocks an earlier Praetor wrote. Adoption
// refreshes one without --force, recorded as a reconcile with no backup, and removes one on
// disable, as it does the current block; an edited block matches none and keeps the --force
// contract, which records its restoration as a replace with a backup.
var priorDocumentationMakefileBlocks = []string{
	// The Markdown gate alone, before the figure checks joined it.
	documentationMakefileBegin + "\n" +
		".PHONY: docs-lint\n" +
		"verify-all: docs-lint\n" +
		"docs-lint:\n" +
		"\t@node tools/markdownlint/verify.mjs\n" +
		documentationMakefileEnd + "\n",
}

// documentationMakefileBlockPraetors reports whether state holds exactly one complete block
// whose text is the current block or an earlier Praetor one.
func documentationMakefileBlockPraetors(state documentationMarkerState) bool {
	return documentationMakefileBlockExact(state, DocumentationMakefileBlock()) ||
		documentationMakefileBlockPrior(state)
}

// documentationMakefileBlockPrior reports whether state holds exactly one complete block whose
// text is an earlier Praetor block.
func documentationMakefileBlockPrior(state documentationMarkerState) bool {
	return slices.ContainsFunc(priorDocumentationMakefileBlocks, func(prior string) bool {
		return documentationMakefileBlockExact(state, prior)
	})
}

// documentationTargetCollision refuses writing the current block when a target it defines that
// inside, the block text it replaces ("" on a first attachment), does not define may already be
// defined by outside, the rest of the Makefile: Make would then warn "overriding recipe" and run
// only one of the two recipes.
func documentationTargetCollision(outside, inside string, expand makefileExpander) error {
	outside = withoutEngineMakefileInclude(outside)
	outside, notes := expand(outside)
	for _, target := range documentationMakefileTargets {
		if !util.MakefileHasTarget(inside, target) && util.MakefileMayDefineTarget(outside, target) {
			if len(notes) > 0 {
				return fmt.Errorf("makefile may define target %s outside the Praetor-managed block (%s)", target, strings.Join(notes, "; "))
			}
			return fmt.Errorf("makefile may define target %s outside the Praetor-managed block", target)
		}
	}
	return nil
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

// errDocumentationBlockEdited refuses an edited documentation gate block in the Makefile without
// --force; documentationMakefile names the forced re-adoption that restores it.
var errDocumentationBlockEdited = errors.New("makefile Praetor documentation gate block was edited")

// documentationMakefile merges the documentation gate block into existing for this session,
// following the tracked includes of the repository (includeExpander): a refused edited block names
// the forced re-adoption that restores it (forceCommand).
func (s *adoptSession) documentationMakefile(ctx context.Context, existing string, force bool) (string, error) {
	merged, err := mergeDocumentationMakefileWith(existing, force, s.includeExpander(ctx))
	if errors.Is(err, errDocumentationBlockEdited) {
		return "", fmt.Errorf("%w; review it and rerun %s", err, s.forceCommand())
	}
	return merged, err
}

// mergeDocumentationMakefileWith merges the documentation gate block into existing; expand
// resolves the includes the ownership check may follow (makefileExpander); the merged text never
// carries the expansion.
func mergeDocumentationMakefileWith(existing string, force bool, expand makefileExpander) (string, error) {
	normalized, crlf, err := util.NormalizeLineEndingsStrict(existing)
	if err != nil {
		return "", fmt.Errorf("makefile line endings are inconsistent: %w", err)
	}
	merged, err := mergeDocumentationMakefileLF(normalized, force, expand)
	if err != nil {
		return "", err
	}
	return util.RestoreLineEndings(merged, crlf), nil
}

func mergeDocumentationMakefileLF(existing string, force bool, expand makefileExpander) (string, error) {
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
		return replaceDocumentationMakefileBlock(existing, block, force || documentationMakefileBlockPraetors(state), expand)
	}
	if err := documentationTargetCollision(existing, "", expand); err != nil {
		return "", err
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

func replaceDocumentationMakefileBlock(existing, block string, force bool, expand makefileExpander) (string, error) {
	state, err := scanDocumentationMakefileMarkers(existing)
	if err != nil {
		return "", err
	}
	if state.beginCount != 1 || state.endCount != 1 || state.end < state.begin {
		return "", fmt.Errorf("makefile contains an incomplete Praetor documentation gate block")
	}
	if !force {
		return "", errDocumentationBlockEdited
	}
	outside := append(append(make([]string, 0, len(state.lines)), state.lines[:state.begin]...), state.lines[state.end+1:]...)
	if err := documentationTargetCollision(strings.Join(outside, "\n"),
		strings.Join(state.lines[state.begin:state.end+1], "\n"), expand); err != nil {
		return "", err
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
	if !documentationMakefileBlockPraetors(state) {
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
	merged, err := s.documentationMakefile(ctx, string(data), s.opts.Force)
	if err != nil {
		return err
	}
	if merged == string(data) {
		s.report.recordReconciled(makefileName, "Documentation gate already attached to verify-all")
		return nil
	}
	publish := func(ctx context.Context) error {
		return contextopt.ReplaceSnapshot(ctx, full, []byte(merged),
			contextopt.ReplaceOptions{Expected: data, Exists: exists, Mode: filePerm})
	}
	action, detail, err := documentationMakefileChange(string(data))
	if err != nil {
		return err
	}
	if action == actionReplace {
		return s.replaceExisting(ctx, replacement{
			rel: makefileName, before: data, after: []byte(merged), detail: detail, publish: publish,
		})
	}
	if !s.opts.DryRun {
		if err := publish(ctx); err != nil {
			return err
		}
	}
	s.planDryRunWrite(makefileName, []byte(merged))
	s.report.recordReconciledAs(makefileName, action, detail)
	return nil
}

// documentationMakefileBlockEdited reports whether data, a Makefile, carries a documentation gate
// marker around a block that is neither the current block nor an earlier Praetor one: the block
// only a forced run restores, a run without --force refusing it (errDocumentationBlockEdited).
func documentationMakefileBlockEdited(data string) (bool, error) {
	normalized, _, err := util.NormalizeLineEndingsStrict(data)
	if err != nil {
		return false, fmt.Errorf("makefile line endings are inconsistent: %w", err)
	}
	state, err := scanDocumentationMakefileMarkers(normalized)
	if err != nil {
		return false, err
	}
	return (state.beginCount > 0 || state.endCount > 0) && !documentationMakefileBlockPraetors(state), nil
}

// Action details of the documentation gate block's changes (documentationMakefileChange).
const (
	documentationMakefileAttached  = "Attached locked documentation gate to verify-all"
	documentationMakefileRefreshed = "Refreshed an earlier Praetor documentation gate block to the current locked block"
	documentationMakefileRestored  = "Restored the locked documentation gate block"
)

// documentationMakefileChange classifies the change reconcileDocumentationMakefile makes to data,
// a Makefile mergeDocumentationMakefile changes, as a report action and its detail. Without a
// marker the block is appended. An exact earlier Praetor block is refreshed, as every other
// earlier Praetor text is (replacePriorText): nobody edited it, so it takes no backup. Any other
// marker-present change restores an edited block under --force, which overwrites adopter lines,
// so it is a replace with a backup whose delta lists only in-block lines, since every line
// outside the block is kept.
func documentationMakefileChange(data string) (action, detail string, err error) {
	normalized, _, err := util.NormalizeLineEndingsStrict(data)
	if err != nil {
		return "", "", fmt.Errorf("makefile line endings are inconsistent: %w", err)
	}
	state, err := scanDocumentationMakefileMarkers(normalized)
	if err != nil {
		return "", "", err
	}
	switch {
	case state.beginCount == 0 && state.endCount == 0:
		return actionAppend, documentationMakefileAttached, nil
	case documentationMakefileBlockPrior(state):
		return actionReconcile, documentationMakefileRefreshed, nil
	}
	return actionReplace, documentationMakefileRestored, nil
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
	priorWithInclude := buildMakefileWith(plan, priorVerificationRecipePrefix)
	priorWithoutInclude := buildMakefileLegacy(plan, priorVerificationRecipePrefix, true)
	return (data == priorWithInclude || data == priorWithoutInclude) && data != buildMakefile(plan)
}

// isPlaceholderVerificationMakefile reports whether data is exactly the placeholder Makefile
// adoption writes for a plan with no runnable command -- the current rendering or the one from
// before the configured-sources gate joined verify-all -- while plan renders a different Makefile.
// An unavailable plan's recipes print a pointer to the report and exit 1 whatever commands it
// names, so each rendering has one placeholder text. Once the repository gains a runnable plan the
// placeholder is earlier Praetor output and is replaced like the historical forms above (#638);
// while the plan is still unavailable it is the current rendering and stays. An edited copy is
// not exact and stays untouched.
func isPlaceholderVerificationMakefile(data string, plan *VerificationPlan) bool {
	data = withoutDocumentationMakefileBlock(data)
	if data == buildMakefile(plan) {
		return false
	}
	placeholder := &VerificationPlan{Status: verificationUnavailable}
	return data == buildMakefile(placeholder) ||
		data == priorSourceGateMakefile(placeholder) ||
		data == priorPathResolvedMakefile(placeholder)
}

// isReplaceableVerificationMakefile reports whether data is earlier Praetor output that adoption
// replaces with the current rendering, in either consistent line-ending style: a CRLF checkout
// (core.autocrlf on Windows) holds the same output, so Linux, macOS and Windows re-runs agree. A
// text mixing both styles has been edited and is not replaceable.
func isReplaceableVerificationMakefile(data string, plan *VerificationPlan) bool {
	normalized, _, err := util.NormalizeLineEndingsStrict(data)
	if err != nil {
		return false
	}
	return isLegacyVerificationMakefile(normalized) ||
		isPriorGeneratedMakefile(normalized, plan) ||
		normalized == priorSourceGateMakefile(plan) ||
		normalized == priorPathResolvedMakefile(plan) ||
		isPlaceholderVerificationMakefile(normalized, plan)
}

const legacyVerificationStub = "\n.PHONY: all verify-all audit compile-context build test\n\nverify-all:\n\t@echo \"Running verification...\"\n\ncompile-context:\n\t@standardsctl compile-context\n\naudit:\n\t@standardsctl audit\n\ntest:\n\t@go test -v -race ./...\n\nbuild:\n\t@go build -v ./...\n"

func legacyVerificationMakefile(test, build string) string {
	return ".PHONY: all verify-all compile-context compile-context-verify audit build test\n\n" +
		"all: build\n\nverify-all: compile-context-verify audit test\n\n" +
		"compile-context:\n\t@standardsctl compile-context\n\n" +
		"compile-context-verify:\n\t@standardsctl compile-context --verify\n\n" +
		"audit:\n\t@standardsctl audit\n\ntest:\n\t@" + test + "\n\nbuild:\n\t@" + build + "\n"
}

// preserveCustomVerification keeps a verify-all adoption did not write as the project's own
// contract. One that still holds the failing recipe adoption writes for an unavailable plan is
// not a contract: it is the placeholder an earlier adoption left, rendered for another plan or
// appended to an existing Makefile, so the plan stays unavailable and says so, rather than
// reporting a preserved verify-all that can only exit 1 (#594). Earlier and current output are
// recognised in either consistent line-ending style, so a CRLF checkout of the current rendering
// is not mistaken for a custom verify-all or for a leftover placeholder.
func preserveCustomVerification(plan *VerificationPlan, data []byte) {
	text := string(data)
	if normalized, _, err := util.NormalizeLineEndingsStrict(text); err == nil {
		text = normalized
	}
	text = withoutDocumentationMakefileBlock(text)
	if !mayDefineVerificationTarget(text) || isReplaceableVerificationMakefile(text, plan) || text == buildMakefile(plan) {
		return
	}
	if normalized, _ := util.NormalizeLineEndings(text); strings.Contains(normalized, unavailableVerificationRecipe) {
		plan.Status = verificationUnavailable
		plan.unavailable("The Makefile still holds the failing placeholder recipe adoption writes; replace it with the project's build and test commands.")
		return
	}
	plan.Status = verificationPreserved
	plan.Reasons = append(plan.Reasons, "Existing custom verify-all is preserved; execute and review it before claiming project verification.")
	plan.Declared = append(append([][]string(nil), plan.Build...), plan.Test...)
	plan.Build = [][]string{}
	plan.Test = [][]string{{"make", verificationTarget}}
}

func withoutDocumentationMakefileBlock(data string) string {
	cleaned, removed, err := removeDocumentationMakefileBlock(data)
	if err == nil && removed {
		return cleaned
	}
	return data
}

func appendVerificationTargets(existing string, plan *VerificationPlan) (string, error) {
	normalized, crlf, err := util.NormalizeLineEndingsStrict(existing)
	if err != nil {
		return "", fmt.Errorf("makefile line endings are inconsistent: %w", err)
	}
	var result strings.Builder
	result.WriteString(normalized)
	result.WriteString("\n# Praetor declared verification; existing project recipes remain unchanged.\n" +
		util.MakefileCLIVariable + ".PHONY: verify-all\nverify-all:\n\t@$(PRAETORCTL) compile-context --verify\n\t@$(PRAETORCTL) caveman check --configured-sources\n\t@$(PRAETORCTL) audit\n")
	// One recipe for the build and test commands together: rendered once for each, an
	// unavailable plan wrote its failing pair twice (#594).
	result.WriteString(verificationRecipe(plan, plan.commands()))
	for _, target := range []string{"compile-context", "audit"} {
		if !util.MakefileHasTarget(normalized, target) {
			result.WriteString("\n" + target + ":\n\t@$(PRAETORCTL) " + target + "\n")
		}
	}
	return util.RestoreLineEndings(result.String(), crlf), nil
}

// Includes, eval calls, expanded defines, generated target names and pattern rules require Make
// evaluation. Never append a potentially overriding recipe when ownership is ambiguous. The
// answer is the shared Makefile reader's (util.MakefileMayDefineTarget), the one editor
// generation reads the same file with, so the two cannot disagree on what a line declares (#304).
func mayDefineVerificationTarget(data string) bool {
	return util.MakefileMayDefineTarget(withoutEngineMakefileInclude(data), verificationTarget)
}
