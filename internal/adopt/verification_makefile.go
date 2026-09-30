package adopt

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	documentationMakefileBegin = "# BEGIN praetor documentation gate"
	documentationMakefileEnd   = "# END praetor documentation gate"
	maxMakefileLines           = 4096
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
func documentationTargetCollision(outside, inside string) error {
	for _, target := range documentationMakefileTargets {
		if !hasVerificationTarget(inside, target) && mayDefineTarget(outside, target) {
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
		return replaceDocumentationMakefileBlock(existing, block, force || documentationMakefileBlockPraetors(state))
	}
	if err := documentationTargetCollision(existing, ""); err != nil {
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
	outside := append(append(make([]string, 0, len(state.lines)), state.lines[:state.begin]...), state.lines[state.end+1:]...)
	if err := documentationTargetCollision(strings.Join(outside, "\n"),
		strings.Join(state.lines[state.begin:state.end+1], "\n")); err != nil {
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
	merged, err := mergeDocumentationMakefile(string(data), s.opts.Force)
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
	prior := buildMakefileWith(plan, priorVerificationRecipePrefix)
	return data == prior && prior != buildMakefile(plan)
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
	return data == buildMakefile(placeholder) || data == priorSourceGateMakefile(placeholder)
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
	return isLegacyVerificationMakefile(normalized) || isPriorGeneratedMakefile(normalized, plan) || normalized == priorSourceGateMakefile(plan) ||
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
	plan.Test = [][]string{{"make", "verify-all"}}
}

func withoutDocumentationMakefileBlock(data string) string {
	cleaned, removed, err := removeDocumentationMakefileBlock(data)
	if err == nil && removed {
		return cleaned
	}
	return data
}

// A Makefile line is read token by token; these name what a token turned out to be.
const (
	makefileAssignToken    = "assign"
	makefileRuleToken      = "rule"
	makefileEndToken       = "end"
	makefileReferenceToken = "reference"
)

// maxMakefileLineBytes bounds the token scan of a single Makefile line (HISS-02). Real declarations
// are far shorter; a line past the bound is read only in part, so its ownership is unresolved, and
// makefileLineIsAmbiguous reports it as ambiguous so adoption preserves instead of appending.
const maxMakefileLineBytes = 8192

// makefileReferenceWidth returns the byte length of the variable reference text opens with, so the
// colon and the "=" inside "$(SRCS:.c=.o)" or "$(call rule,$(P)x,A=b)" are not read as operators.
// "$x" and "$$" span two bytes. Like GNU Make 4.4.1 it counts nested brackets of the kind the
// reference opens with: "$(a $(b),c=d)" ends at its last ")", a "}" inside "$(" is plain text, and
// a reference that is never closed spans the rest of text.
func makefileReferenceWidth(text string) int {
	if len(text) < 2 {
		return 1
	}
	var closer byte
	switch text[1] {
	case '(':
		closer = ')'
	case '{':
		closer = '}'
	default:
		return 2
	}
	depth := 0
	for end := 1; end < len(text) && end < maxMakefileLineBytes; end++ {
		switch text[end] {
		case text[1]:
			depth++
		case closer:
			depth--
		}
		if depth == 0 {
			return end + 1
		}
	}
	return len(text)
}

// makefileReference classifies the "$" text opens with: "$$" is an escaped dollar sign that
// expands to a literal "$", anything else a variable reference Make expands.
func makefileReference(text string) (string, int) {
	if strings.HasPrefix(text, "$$") {
		return "", 2
	}
	return makefileReferenceToken, makefileReferenceWidth(text)
}

// makefileColonOperator classifies the run of colons starting at line[i]. A run followed by "=" is
// an assignment operator (":=", "::=", ":::="); any other run separates targets from prerequisites.
func makefileColonOperator(line string, i int) (string, int) {
	run := i
	for run < len(line) && run < maxMakefileLineBytes && line[run] == ':' {
		run++
	}
	if run < len(line) && line[run] == '=' {
		return makefileAssignToken, run - i + 1
	}
	return makefileRuleToken, run - i
}

// makefileOperatorAt classifies the token starting at line[i] and reports the bytes it spans. Make
// ends a variable name at "=", "+=", "?=", "!=" or a run of colons followed by "="; it stops
// reading the line at an unescaped "#" (a comment) or ";" (the inline recipe), and a backslash
// escapes the byte behind it.
func makefileOperatorAt(line string, i int) (string, int) {
	switch line[i] {
	case '=':
		return makefileAssignToken, 1
	case '+', '?', '!':
		if i+1 < len(line) && line[i+1] == '=' {
			return makefileAssignToken, 2
		}
	case ':':
		return makefileColonOperator(line, i)
	case '$':
		return makefileReference(line[i:])
	case '#', ';':
		return makefileEndToken, 1
	case '\\':
		return "", 2
	}
	return "", 1
}

// makefileSplit returns the byte offsets of the first variable-assignment operator, of the first
// rule colon and of the first variable reference on line, each -1 when the line holds none. Make
// reads whichever operator comes first: an assignment first binds a variable whose value may
// itself contain colons ("V = a:b"), a colon first opens a rule ("t: dep"). The scan ends where
// Make stops reading the line -- at a comment or at the ";" that opens an inline recipe -- and at
// maxMakefileLineBytes (HISS-02).
func makefileSplit(line string) (assign, colon, reference int) {
	assign, colon, reference = -1, -1, -1
	for i := 0; i < len(line) && i < maxMakefileLineBytes; {
		kind, width := makefileOperatorAt(line, i)
		switch {
		case kind == makefileEndToken:
			return assign, colon, reference
		case kind == makefileAssignToken && assign < 0:
			assign = i
		case kind == makefileRuleToken && colon < 0:
			colon = i
		case kind == makefileReferenceToken && reference < 0:
			reference = i
		}
		i += width
	}
	return assign, colon, reference
}

// makefileBindsVariable reports whether text binds a variable rather than opening a rule, decided
// by whichever operator Make reaches first.
func makefileBindsVariable(text string) bool {
	assign, colon, _ := makefileSplit(text)
	return assign >= 0 && (colon < 0 || assign < colon)
}

// makefileExportDirective reports whether line is an export or unexport directive: its first word,
// followed by whitespace, is "export" or "unexport". Make reads such a line as a list of variables
// to export and never as a rule, colon or not. Measured against GNU Make 4.4.1: "export
// verify-all: dep", "unexport verify-all: dep" and "export : dep" declare no target, while
// "export: dep" declares the target "export" and "override verify-all: dep", "private verify-all:
// dep" and "override export verify-all: dep" declare verify-all -- only the first word decides.
func makefileExportDirective(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	end := strings.IndexAny(trimmed, " \t")
	if end < 0 {
		return false
	}
	word := trimmed[:end]
	return word == "export" || word == "unexport"
}

// makefileTargetNames returns the target names a Makefile line declares, and none when the line
// declares no rule. Make cuts the line at the first comment or inline-recipe ";" and decides on
// what is left, so the test runs twice over text Make still reads: once over the whole line and
// once over the prerequisites. Measured against GNU Make 4.4.1: "verify-all := x",
// "verify-all = a:b", "verify-all ?= a:b", "verify-all += x:y", "verify-all != date" and
// "override verify-all := x" all bind a variable, and "verify-all: CFLAGS := -g" -- with or
// without a trailing comment or ";" recipe -- binds a target-specific variable; each answers
// "make verify-all" with "No rule to make target". "verify-all:: dep", "verify-all: $(SRCS:.c=.o)",
// "verify-all: lint ## run gates (FAST=1)" and "verify-all: ; FOO=1 echo c" all declare the target:
// an "=" a comment or a recipe carries is not an assignment operator.
func makefileTargetNames(line string) []string {
	if strings.HasPrefix(line, "\t") || makefileExportDirective(line) {
		return nil
	}
	_, colon, _ := makefileSplit(line)
	if colon < 0 || makefileBindsVariable(line) {
		return nil
	}
	_, width := makefileOperatorAt(line, colon)
	if makefileBindsVariable(line[colon+width:]) {
		return nil
	}
	return strings.Fields(line[:colon])
}

// hasVerificationTarget reports whether data declares a rule for target. A define body is variable
// text, not rules, so a "target:" line inside one declares nothing; mayDefineTarget reports the
// files where Make may still parse such a body as rules.
func hasVerificationTarget(data, target string) bool {
	return verificationTargetLine(strings.Split(data, "\n"), target) >= 0
}

// verificationTargetLine returns the index of the first line that declares a rule for target, or
// -1 when none within the scan bound does.
func verificationTargetLine(lines []string, target string) int {
	var define makefileDefineTracker
	for index := 0; index < len(lines) && index < maxMakefileLines; index++ {
		if define.body(lines[index]) {
			continue
		}
		if slices.Contains(makefileTargetNames(lines[index]), target) {
			return index
		}
	}
	return -1
}

// verificationTargetRecipe returns the tab-prefixed recipe lines, each ending in "\n", that follow
// the first rule data declares for target, and whether data declares one. A rule without recipe
// lines, such as "test: build" alone, returns an empty recipe.
func verificationTargetRecipe(data, target string) (string, bool) {
	lines := strings.Split(data, "\n")
	index := verificationTargetLine(lines, target)
	if index < 0 {
		return "", false
	}
	var recipe strings.Builder
	for next := index + 1; next < len(lines) && next < maxMakefileLines && strings.HasPrefix(lines[next], "\t"); next++ {
		recipe.WriteString(lines[next])
		recipe.WriteByte('\n')
	}
	return recipe.String(), true
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
		if !hasVerificationTarget(normalized, target) {
			result.WriteString("\n" + target + ":\n\t@$(PRAETORCTL) " + target + "\n")
		}
	}
	return util.RestoreLineEndings(result.String(), crlf), nil
}

// makefileDirective returns the first word of a Makefile line that carries meaning, skipping the
// modifiers Make allows in front of a variable assignment or a define. "override", "export",
// "unexport" and "private" take an assignment or a "define" and nothing else, so none of them can
// introduce a rule; "override define recipe" is still a define.
func makefileDirective(fields []string) string {
	if index := makefileDirectiveIndex(fields); index < len(fields) {
		return fields[index]
	}
	return ""
}

// makefileDirectiveIndex returns the index of the first field that is not one of those modifiers,
// or len(fields) when every field is one.
func makefileDirectiveIndex(fields []string) int {
	for index, field := range fields {
		switch field {
		case "override", "export", "unexport", "private":
			continue
		}
		return index
	}
	return len(fields)
}

// makefileLineIsAmbiguous reports whether a line may define targets only Make can resolve: an
// include, an $(eval ...) or ${eval ...} call, a computed or pattern target name, or a line longer
// than the scan bound, which is read in part and therefore unresolved. The line is already trimmed
// and is neither a recipe line nor part of a define body. A define alone is not ambiguous: it only
// binds a variable, and makefileOwnershipScan reports the files that may expand it into rules. A
// bare modifier is not ambiguous either: measured against GNU Make 4.4.1, a Makefile holding
// "override verify-all := x" or "override CFLAGS += -Wall" beside an "all:" rule answers
// "make verify-all" with "No rule to make target".
func makefileLineIsAmbiguous(line string) bool {
	if strings.HasPrefix(line, "#") {
		return false
	}
	if len(line) > maxMakefileLineBytes {
		return true
	}
	switch makefileDirective(strings.Fields(line)) {
	case "include", "-include", "sinclude":
		return true
	}
	if makefileCallsEval(line) {
		return true
	}
	for _, name := range makefileTargetNames(line) {
		if strings.ContainsAny(name, "$%") {
			return true
		}
	}
	return false
}

// Includes, eval calls, expanded defines, generated target names and pattern rules require Make
// evaluation. Never append a potentially overriding recipe when ownership is ambiguous.
func mayDefineVerificationTarget(data string) bool {
	return mayDefineTarget(data, "verify-all")
}

// mayDefineTarget reports whether data may already own target: a rule for it, a line only Make can
// resolve, a define Make may expand into rules or that is never closed, or more than
// maxMakefileLines lines, whose unread tail may hold any of these (HISS-02).
func mayDefineTarget(data, target string) bool {
	if hasVerificationTarget(data, target) {
		return true
	}
	lines := strings.Split(data, "\n")
	if len(lines) > maxMakefileLines {
		return true
	}
	var scan makefileOwnershipScan
	for index := 0; index < len(lines) && index < maxMakefileLines; index++ {
		if scan.read(lines[index]) {
			return true
		}
	}
	return scan.unresolved()
}
