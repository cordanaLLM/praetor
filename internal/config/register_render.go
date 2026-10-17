package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
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
	// EvidenceDirDefault is where the rendered evidence rule sends agent evidence unless
	// register.evidence.dir names another directory (RegisterPolicy.EvidenceDir), relative to
	// the directory of the AGENTS.md that carries the block. compile-context makes Git ignore
	// it, and compile-context --verify and audit fail while Git does not
	// (compiler.EvidenceIgnored).
	EvidenceDirDefault = ".workingdir/evidence/"
	// MaxEvidenceDirBytes bounds register.evidence.dir; it renders inside one line of the block.
	MaxEvidenceDirBytes = 255
)

// registerOrder fixes the row order of every rendered list.
var registerOrder = []TextRegister{TextRegisterSocial, TextRegisterDocs, TextRegisterInternal}

// registerForms is the single statement of what each register demands in every repository.
// The compiled block and the prompt directives both read it, so the two cannot drift apart. It
// holds only what the engine can assert for any repository. A convention of one repository,
// such as a pull-request template, a receipt fence or a changelog fragment lane, comes from that
// repository's register.conventions or from what compile-context detects there
// (RegisterPolicy.form, WithDetectedConventions), never from here (#328).
var registerForms = map[TextRegister]string{
	TextRegisterSocial:   "BLUF, full sentences, scannable, enough and no more; conventional commit subject unchanged",
	TextRegisterDocs:     "complete without bloat: newcomer path first, expert reference after; every claim points at a file, command or test; no restated code",
	TextRegisterInternal: "fragments, no filler, verbatim code/paths/errors; facts, paths, commands, verdict",
}

// registerSkills names the skill that states a register's form at length, for the registers
// that have one. The block names a skill only where the repository carries its
// .agents/skills/<name>/SKILL.md (RegisterPolicy.WithAbsentSkills): a name with no skill behind
// it points every agent at instructions the repository does not hold (#235).
var registerSkills = map[TextRegister]string{
	TextRegisterSocial:   "social-text",
	TextRegisterInternal: "caveman",
}

// RegisterSkills returns the skills the rendered block can name, in register order.
func RegisterSkills() []string {
	names := make([]string, 0, len(registerSkills))
	for _, register := range registerOrder {
		if name := registerSkills[register]; name != "" {
			names = append(names, name)
		}
	}
	return names
}

// registerSkillInheritance names the skills a register skill inherits from by reference:
// social-text takes three principles from adhd-format, so the social register is incomplete
// without it (#235).
var registerSkillInheritance = []string{"adhd-format"}

// RegisterSkillBundle returns the skills Praetor ships for the text register: the register skills
// (RegisterSkills), then the skills they inherit from. Adoption installs them, and
// compile-context copies each one a repository carries into the skill directory of every agent
// client that does not read .agents/skills.
func RegisterSkillBundle() []string {
	return append(RegisterSkills(), registerSkillInheritance...)
}

// namedForm returns the form of register r, led by the skill that states it at length when r
// has one and named is set.
func namedForm(r TextRegister, named bool) string {
	if skill := registerSkills[r]; skill != "" && named {
		return "`" + skill + "` skill: " + registerForms[r]
	}
	return registerForms[r]
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
// register: its engine-universal form, led by the register's skill, without a repository
// convention. An unknown or empty register yields "", so a caller that appends the result leaves
// its prompt byte-identical. Repair jobs, prompts for a provider kept as a private review
// artifact (dogfood.SaveRepairPlan) and never as repository content, use it; a directive written
// into a repository, which may lack the skill, uses RegisterDirectiveWithout.
func RegisterDirective(r TextRegister) string {
	return RegisterDirectiveWithout(r, nil)
}

// RegisterDirectiveWithout is RegisterDirective for a repository that lacks the register skills
// in absent (compiler.AbsentRegisterSkills): when r's skill is among them, the sentence states
// the form without naming it, as the block does (RegisterPolicy.WithAbsentSkills). The Paperclip
// harness, which adoption writes into the repository, uses it (#235).
func RegisterDirectiveWithout(r TextRegister, absent []string) string {
	if _, ok := registerForms[r]; !ok {
		return ""
	}
	return fmt.Sprintf("Text register %s: %s.", r, namedForm(r, !slices.Contains(absent, registerSkills[r])))
}

// RenderRegisterBlock renders the marker-delimited block that compile-context splices
// under RegisterBlockHeading. The table, the task-row line and the evidence numbers come
// from the policy; the rest is fixed text. Task budgets are deliberately not printed: they
// are dispatch parameters, not writing guidance. Blank lines surround the table so that it
// renders as a table on the forge and passes an adopter's Markdown lint. dispatchGated
// reports whether the repository registers the pre-dispatch hook
// (agenthook.DispatchGateRegistered); only then does the block say a hook denies a subagent
// brief without `task:` (#504).
func RenderRegisterBlock(p RegisterPolicy, dispatchGated bool) (string, error) {
	lines := []string{
		RegisterBlockStart,
		"Register follows the audience, then the task label of your brief (`register:` in `.standards.yaml`; labels are the router's `target_tasks`).",
		"",
	}
	evidence, err := renderEvidenceRule(p)
	if err != nil {
		return "", err
	}
	lines = append(lines, renderRegisterTable(p)...)
	lines = append(lines, "",
		"- "+renderRegisterTaskRows(p, dispatchGated),
		"- "+evidence,
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
		rows = append(rows, fmt.Sprintf("| %s | %s | %s |", register, strings.Join(where, "; "), p.form(register)))
	}
	return rows
}

// form returns what register r demands in this repository: the engine-universal form
// (registerForms), led by its skill unless the repository lacks that skill (WithAbsentSkills),
// then the repository's own convention for r when it states one (RegisterPolicy.Conventions).
func (p RegisterPolicy) form(r TextRegister) string {
	form := namedForm(r, !p.absentSkills[registerSkills[r]])
	if convention := strings.TrimSpace(p.Conventions[r]); convention != "" {
		form += "; " + convention
	}
	return form
}

// renderRegisterTaskRows names every row that departs from the agent surface; a row that
// repeats it is the fallback already and is not printed. It closes with the subagent brief
// rule, and with its enforcement only where the dispatch hook is registered.
func renderRegisterTaskRows(p RegisterPolicy, dispatchGated bool) string {
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
	parts = append(parts, fmt.Sprintf("every other label and any unlabeled text = %s.", fallback))
	brief := subagentBriefRule
	if p.absentSkills[registerSkills[TextRegisterInternal]] {
		brief = subagentBriefPlain
	}
	rule := brief + "."
	if dispatchGated {
		rule = brief + subagentBriefGate
	}
	return "Task rows: " + strings.Join(parts, "; ") + " " + rule
}

// subagentBriefRule states what a subagent launch brief needs, and subagentBriefPlain states it
// without the `caveman` skill, for a repository that lacks it. subagentBriefGate adds what the
// native dispatch hook (`praetorctl hook <client> pre-dispatch`, internal/agenthook) does where
// the repository registers it: it resolves the brief's register from its `task:` label and
// denies a brief without one, so the fallback above never applies to a launch brief. Without the
// registration nothing denies such a brief, and the block does not say anything does.
const (
	subagentBriefRule  = "Subagent launch brief: `caveman` brief shape with `task:` = routing label"
	subagentBriefPlain = "Subagent launch brief: internal register with `task:` = routing label"
	subagentBriefGate  = "; registered dispatch hook denies brief missing `task:`."
)

// renderEvidenceRule renders the evidence line from the policy's bounds and directory. A
// directory CheckEvidenceDir rejects fails the render instead of falling back to the default.
func renderEvidenceRule(p RegisterPolicy) (string, error) {
	dir, err := CheckEvidenceDir(p.EvidenceDir())
	if err != nil {
		return "", err
	}
	bounds := DefaultRegisterPolicy().Evidence
	tightenPositive(&bounds.InlineMaxLines, p.Evidence.InlineMaxLines)
	tightenPositive(&bounds.InlineMaxTokens, p.Evidence.InlineMaxTokens)
	return fmt.Sprintf("Evidence above %d lines or %d tokens leaves the message as a file under `%s`; "+
		"return `evidence: <path> sha256:<12 hex> lines:<n>` and fetch it only when a decision needs it.",
		bounds.InlineMaxLines, bounds.InlineMaxTokens, dir), nil
}

// CheckEvidenceDir returns dir, a register.evidence.dir value, in canonical form: a
// slash-separated path relative to the AGENTS.md that carries the block, ending in one slash.
// It refuses an empty or oversized value, an absolute path, a path that leaves the repository
// through "..", an empty or "." segment, a path inside .git, and any character that would break
// the rendered line or is not portable across platforms (control characters, backslash, ':',
// '|', '`'). Whether Git ignores the directory is a property of the repository, not of the value:
// compiler.CheckEvidenceIgnored decides it.
func CheckEvidenceDir(dir string) (string, error) {
	if err := checkEvidenceDirText(dir); err != nil {
		return "", err
	}
	trimmed := strings.TrimSuffix(dir, "/")
	for i, segment := range strings.Split(trimmed, "/") {
		if err := checkEvidenceDirSegment(dir, segment, i == 0); err != nil {
			return "", err
		}
	}
	return trimmed + "/", nil
}

// checkEvidenceDirText refuses a value whose length, characters or leading slash CheckEvidenceDir
// does not accept, before it is split into segments.
func checkEvidenceDirText(dir string) error {
	if dir == "" || len(dir) > MaxEvidenceDirBytes {
		return fmt.Errorf("register evidence dir must be 1..%d bytes", MaxEvidenceDirBytes)
	}
	if !utf8.ValidString(dir) || strings.ContainsFunc(dir, unicode.IsControl) || strings.ContainsAny(dir, "\\:|`") {
		return fmt.Errorf("register evidence dir %q must be one line of UTF-8 without control characters, '\\', ':', '|' or '`'", dir)
	}
	if strings.HasPrefix(dir, "/") {
		return fmt.Errorf("register evidence dir %q must be relative to the repository, not absolute", dir)
	}
	return nil
}

// checkEvidenceDirSegment refuses one slash-separated segment of dir: "..", an empty or "."
// segment, and .git as the first one.
func checkEvidenceDirSegment(dir, segment string, first bool) error {
	switch {
	case segment == "..":
		return fmt.Errorf("register evidence dir %q must not leave the repository through '..'", dir)
	case segment == "" || segment == ".":
		return fmt.Errorf("register evidence dir %q must not hold an empty or '.' segment", dir)
	case first && strings.EqualFold(segment, ".git"):
		return fmt.Errorf("register evidence dir %q must not lie inside .git", dir)
	}
	return nil
}

// EvidenceRoot returns the top-level directory of evidenceDir, a value CheckEvidenceDir
// accepted: the directory an ignore rule names to keep the evidence directory and everything
// beside it private, such as .workingdir for .workingdir/evidence/.
func EvidenceRoot(evidenceDir string) string {
	root, _, _ := strings.Cut(evidenceDir, "/")
	return root
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
