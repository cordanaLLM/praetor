package config

import (
	"errors"
	"fmt"
	"strings"
)

const (
	// RegisterBlockHeading is the H2 that carries the rendered block in AGENTS.md. It is
	// not a vendor name, so every compiled target keeps the section.
	RegisterBlockHeading = "## Text Register"
	// RegisterSectionPrefix precedes the block wherever the whole section is written at
	// once: a document that has no markers yet, and the adopt harness.
	RegisterSectionPrefix = RegisterBlockHeading + "\n\n"
	// RegisterBlockStart and RegisterBlockEnd delimit the tool-written region. Between
	// them .standards.yaml is the source and AGENTS.md only the carrier.
	RegisterBlockStart = "<!-- praetor:register:start -->"
	RegisterBlockEnd   = "<!-- praetor:register:end -->"
	// MaxRegisterBlockLines bounds the section prefix plus the rendered block, so the
	// section can never eat the 300-line vendor budget.
	MaxRegisterBlockLines = 15
	// evidencePointerDigestHex is how much of a SHA-256 digest a pointer line carries.
	evidencePointerDigestHex = 12
	// maxEvidencePointerPathBytes bounds the path embedded in a pointer line.
	maxEvidencePointerPathBytes = 4096
)

// registerOrder fixes the row order of every rendered list.
var registerOrder = []TextRegister{TextRegisterSocial, TextRegisterDocs, TextRegisterInternal}

// registerForms is the single statement of what each register demands. The compiled block
// and the prompt directives both read it, so the two cannot drift apart.
var registerForms = map[TextRegister]string{
	TextRegisterSocial:   "`social-text` skill: BLUF, full sentences, scannable, enough and no more; PR template, receipt fence, conventional commit subject and changelog fragment unchanged",
	TextRegisterDocs:     "complete without bloat: newcomer path first, expert reference after; every claim points at a file, command or test; no restated code",
	TextRegisterInternal: "telegraphic: no filler, no preamble, no restatement; facts, paths, commands, verdict",
}

// surfaceAudiences describes who reads each surface, in rendering order.
var surfaceAudiences = []struct {
	surface  RegisterSurface
	audience string
}{
	{SurfaceForge, "forge: issues, PR bodies, review comments, commit bodies"},
	{SurfaceDocs, "docs/, README, ADR bodies"},
	{SurfaceAgent, "briefs, agent-to-agent traffic, research fan-outs, workflow returns"},
}

// RegisterDirective returns the one-sentence instruction a prompt builder appends for a
// register. An unknown or empty register yields "", so a caller that appends the result
// leaves its prompt byte-identical.
func RegisterDirective(r TextRegister) string {
	form, ok := registerForms[r]
	if !ok {
		return ""
	}
	return fmt.Sprintf("Text register %s: %s.", r, form)
}

// RenderRegisterBlock renders the marker-delimited block that compile-context splices
// under RegisterBlockHeading. The table, the task-row line and the evidence numbers come
// from the policy; the rest is fixed text. Task budgets are deliberately not printed: they
// are dispatch parameters, not writing guidance. Blank lines surround the table so that it
// renders as a table on the forge and passes an adopter's Markdown lint.
func RenderRegisterBlock(p RegisterPolicy) (string, error) {
	lines := []string{
		RegisterBlockStart,
		"Register follows the audience, then the task label of your brief (`register:` in `.standards.yaml`; labels are the router's `target_tasks`).",
		"",
	}
	lines = append(lines, renderRegisterTable(p)...)
	lines = append(lines, "",
		"- "+renderRegisterTaskRows(p),
		"- "+renderEvidenceRule(p.Evidence),
		"- An internal return carries verdict, changed paths, commands run, evidence pointers and open questions, nothing else.",
		RegisterBlockEnd)
	block := strings.Join(lines, "\n")
	prefixLines := strings.Count(RegisterSectionPrefix, "\n")
	if count := strings.Count(block, "\n") + 1 + prefixLines; count > MaxRegisterBlockLines {
		return "", fmt.Errorf("text register section renders %d lines, budget %d", count, MaxRegisterBlockLines)
	}
	return block, nil
}

// renderRegisterTable lists, per register, the surfaces the policy assigns to it.
func renderRegisterTable(p RegisterPolicy) []string {
	rows := []string{"| Register | Where | Form |", "| :--- | :--- | :--- |"}
	for _, register := range registerOrder {
		where := make([]string, 0, len(surfaceAudiences))
		for _, entry := range surfaceAudiences {
			if p.surfaceRegister(entry.surface) == register {
				where = append(where, entry.audience)
			}
		}
		if len(where) == 0 {
			where = append(where, "task rows only")
		}
		rows = append(rows, fmt.Sprintf("| %s | %s | %s |", register, strings.Join(where, "; "), registerForms[register]))
	}
	return rows
}

// renderRegisterTaskRows names every row that departs from the agent surface; a row that
// repeats it is the fallback already and is not printed.
func renderRegisterTaskRows(p RegisterPolicy) string {
	fallback := p.surfaceRegister(SurfaceAgent)
	grouped := make(map[TextRegister][]string, len(registerOrder))
	for _, label := range p.sortedTaskLabels() {
		if register := p.Tasks[label].Register; register != fallback && knownTextRegister(register) {
			grouped[register] = append(grouped[register], label)
		}
	}
	parts := make([]string, 0, len(registerOrder)+1)
	for _, register := range registerOrder {
		if labels := grouped[register]; len(labels) > 0 {
			parts = append(parts, fmt.Sprintf("%s = %s", register, strings.Join(labels, ", ")))
		}
	}
	parts = append(parts, fmt.Sprintf("every other label and any brief without one = %s.", fallback))
	return "Task rows: " + strings.Join(parts, "; ")
}

func renderEvidenceRule(e EvidenceBounds) string {
	bounds := DefaultRegisterPolicy().Evidence
	tightenPositive(&bounds.InlineMaxLines, e.InlineMaxLines)
	tightenPositive(&bounds.InlineMaxTokens, e.InlineMaxTokens)
	return fmt.Sprintf("Evidence above %d lines or %d tokens leaves the message as a file under `.workingdir/evidence/`; "+
		"return `evidence: <path> sha256:<12 hex> lines:<n>` and fetch it only when a decision needs it.",
		bounds.InlineMaxLines, bounds.InlineMaxTokens)
}

// EvidencePointer renders the one pointer format for evidence that left the token path:
// `evidence: <path> sha256:<first 12 hex> lines:<n>`. The reader fetches the file only
// when a decision depends on it.
func EvidencePointer(path, sha256Hex string, lines int) (string, error) {
	if path == "" || len(path) > maxEvidencePointerPathBytes || strings.ContainsAny(path, "\x00\r\n") {
		return "", errors.New("evidence pointer requires a single-line path")
	}
	if lines < 0 {
		return "", fmt.Errorf("evidence pointer line count %d is negative", lines)
	}
	digest := strings.ToLower(sha256Hex)
	if len(digest) < evidencePointerDigestHex || strings.Trim(digest, "0123456789abcdef") != "" {
		return "", fmt.Errorf("evidence pointer requires at least %d hex digits of a SHA-256 digest", evidencePointerDigestHex)
	}
	return fmt.Sprintf("evidence: %s sha256:%s lines:%d", path, digest[:evidencePointerDigestHex], lines), nil
}
