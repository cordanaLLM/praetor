package compiler

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// maxQuotedLintFindings bounds the findings a lint error quotes; the error states the total,
// and `praetorctl caveman check` prints them all.
const maxQuotedLintFindings = 5

// ErrContextProse is returned by LintContext when the canonical AGENTS.md breaks the caveman
// lint. AGENTS.md is agent-only text, so it is held to config.ContextRegister (ADR-0010).
var ErrContextProse = errors.New("AGENTS.md fails the caveman lint")

// ErrAgentTextProse is returned by LintAgentText when a persona or a skill breaks the
// caveman lint or the AgentTextCeiling word budget. Personas and skills sit under
// config.SurfaceContext by its own doc comment ("AGENTS.md, the compiled vendor files,
// personas and skills"; internal/config/register.go), so they carry the same fixed,
// no-opt-out rule as AGENTS.md itself (ADR-0010 decision 11).
var ErrAgentTextProse = errors.New("agent text fails the caveman lint")

// AgentTextCeiling is the prose-word ceiling a persona or a skill is linted against, on top
// of the caveman rules AGENTS.md itself must pass. Measured in
// .workingdir/planning/register-gaps-20260919.md: it sits between the two skills that
// failed the lint at 532-561 prose words (social-text, caveman) and the shortest passing
// skill in the directory (185 words), low enough to force a caveman rewrite rather than a
// trim, high enough not to force splitting a skill with real rule tables.
const AgentTextCeiling = 600

// LintAgentText runs the caveman lint plus AgentTextCeiling over one persona or skill and
// wraps a failure the same way LintContext does, so the same 'praetorctl caveman check'
// command reproduces the gate's verdict. label names the file in the error and in the
// findings; it carries no meaning to the lint itself.
func LintAgentText(label, text string) (caveman.Report, error) {
	report := caveman.Check(text, caveman.Options{Kind: caveman.KindContext, MaxProseWords: AgentTextCeiling})
	if !report.Passed() {
		return report, fmt.Errorf("%w: %s", ErrAgentTextProse, describeLintFindings(label, report.Findings))
	}
	return report, nil
}

// ContextLint is the caveman verdict on one canonical AGENTS.md.
type ContextLint struct {
	Report caveman.Report
	// MaskedLines counts the lines of the rendered register block, which the lint leaves to
	// its renderer (MaskRegisterBlock).
	MaskedLines int
}

// Summary is the one-line verdict printed on a pass: the counts behind it, so a clean run
// can be told apart from one that read nothing.
func (l ContextLint) Summary() string {
	return fmt.Sprintf("caveman lint passed: %d prose words, %.1f articles per 100 (limit %.1f), %d register block lines left to the renderer",
		l.Report.ProseWords, l.Report.Density(), caveman.DefaultMaxArticleDensity, l.MaskedLines)
}

// LintContext runs the caveman lint over the canonical AGENTS.md at agentsMdPath. It reads no
// manifest: the context surface is fixed to the internal register in every repository, so
// there is no setting to consult and no opt-out. The whole file is linted, the praetor
// harness and whatever the repository wrote below it included; only the rendered register
// block is masked (MaskRegisterBlock). Findings return ErrContextProse, wrapped with the
// first findings and the fix.
func LintContext(ctx context.Context, agentsMdPath string) (ContextLint, error) {
	data, err := contextopt.ReadSnapshot(ctx, agentsMdPath)
	if err != nil {
		return ContextLint{}, fmt.Errorf("caveman lint: read %s: %w", agentsMdPath, err)
	}
	text, masked := MaskRegisterBlock(string(data))
	lint := ContextLint{Report: caveman.Check(text, caveman.Options{Kind: caveman.KindContext}), MaskedLines: masked}
	if !lint.Report.Passed() {
		return lint, fmt.Errorf("%w: %s", ErrContextProse, describeLintFindings(agentsMdPath, lint.Report.Findings))
	}
	return lint, nil
}

// MaskRegisterBlock blanks the text register block, markers included, and returns how many
// lines it blanked. The context gate and `praetorctl caveman check` both lint through it, so
// the command an error message names reproduces the gate's verdict. The block is rendered by
// config.RenderRegisterBlock and spliced by compile-context; nobody edits it by hand, so its
// wording belongs to its renderer, and a repository is never failed for text it did not
// write. Blanking keeps every other line on its number. Malformed markers are left for
// SyncRegisterBlock to report; the text is then linted whole.
func MaskRegisterBlock(content string) (string, int) {
	first, last, err := util.FindMarkedBlock(content, config.RegisterBlockStart, config.RegisterBlockEnd)
	if err != nil || first < 0 {
		return content, 0
	}
	lines := strings.Split(content, "\n")
	for i := first; i <= last; i++ {
		lines[i] = ""
	}
	return strings.Join(lines, "\n"), last - first + 1
}

// describeLintFindings quotes the first findings and names the fix.
func describeLintFindings(path string, findings []caveman.Finding) string {
	quoted := make([]string, 0, maxQuotedLintFindings)
	for i := 0; i < len(findings) && i < maxQuotedLintFindings; i++ {
		quoted = append(quoted, fmt.Sprintf("line %d %s: %s", findings[i].Line, findings[i].Rule, findings[i].Excerpt))
	}
	more := ""
	if extra := len(findings) - len(quoted); extra > 0 {
		more = fmt.Sprintf("; +%d more", extra)
	}
	return fmt.Sprintf("%d finding(s): %s%s. Run 'praetorctl caveman check --kind=context %s' and rewrite the flagged lines in caveman",
		len(findings), strings.Join(quoted, "; "), more, path)
}
