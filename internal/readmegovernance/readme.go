// Package readmegovernance owns the bounded, marker-delimited governance block that
// Praetor writes into an adopter's README. Adoption and audit share this package so the
// renderer cannot drift from the verifier.
package readmegovernance

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
	markdownassets "github.com/cordanaLLM/praetor/tools/markdownlint"
)

const (
	// Start and End delimit the only README region Praetor owns.
	Start = "<!-- praetor:readme-governance:start -->"
	End   = "<!-- praetor:readme-governance:end -->"
	// File is the repository-relative README that carries the managed block.
	File = "README.md"

	// The documentation paragraph's first line and the debt baseline lines are shared by
	// renderBlock and blockState, so the reader cannot drift from the renderer it reads back.
	documentationLine  = "**Documentation**: `make docs-lint` enforces locked Markdown style and the"
	debtBaselineLead   = "**Debt Baseline**: `.standards-baseline.json` "
	debtPendingLine    = debtBaselineLead + "is not recorded;"
	debtRecordedLine   = debtBaselineLead + "anchors the debt ratchet at"
	debtRecordedSuffix = "; audit forbids growth."
)

var (
	// ErrMissing is returned when audit expects a managed block but none exists.
	ErrMissing = errors.New("managed README governance block is missing")
	// ErrStale is returned when the managed block or a legacy generated surface differs
	// from the current deterministic rendering.
	ErrStale = errors.New("managed README governance block is stale")
	// ErrInvalidState is returned for an impossible renderer input.
	ErrInvalidState = errors.New("README governance state is invalid")

	legacyBadge = regexp.MustCompile(`(?mi)^\[!\[HISS(?:-16)?[[:space:]]+(?:Compliant|Adopted)\]\(https://img\.shields\.io/badge/Standards-HISS(?:--16)?%20(?:Compliant|Adopted)(?:%20\([0-9]+%20baselined\))?-(?:brightgreen|yellow)\)\]\([^\r\n]+\)[[:space:]]*\r?\n?`)
	customBadge = regexp.MustCompile(`(?mi)^.*\[!\[[^\]]*HISS[^\]]*\]\(https://img\.shields\.io/badge/[^\r\n]*HISS[^\r\n]*\).*$`)
)

const legacyGovernanceCurrent = `

## Standards & Governance

This repository conforms to the High-Integrity Systems Standard (HISS)
and modernized NASA JPL Power-of-10 rules.

| Gate | Command | Description |
| :--- | :--- | :--- |
| **Verification** | ` + "`make verify-all`" + ` | Runs full audit, test suite, and context integrity check |
| **HISS Audit** | ` + "`praetorctl audit`" + ` | Enforces zero technical debt regression against baseline |
| **Context Sync** | ` + "`praetorctl compile-context`" + ` | Transpiles canonical ` + "`AGENTS.md`" + ` to all AI targets |
`

const legacyGovernanceHISS16 = `

## Standards & Governance

This repository conforms to High-Integrity Systems Standards (HISS-16)
and modernized NASA JPL Power-of-10 rules.

| Gate | Command | Description |
| :--- | :--- | :--- |
| **Verification** | ` + "`make verify-all`" + ` | Runs full audit, test suite, and context integrity check |
| **HISS Audit** | ` + "`standardsctl audit`" + ` | Enforces zero technical debt regression against baseline |
| **Context Sync** | ` + "`standardsctl compile-context`" + ` | Transpiles canonical ` + "`AGENTS.md`" + ` to all AI targets |
`

// State is the durable evidence adoption can truthfully render. A baseline is a debt
// anchor, not proof that the repository's full verification gate passed.
//
// The block never links a repository-relative path, because a documentation portal that
// includes the README resolves such a link against its own pages and a strict MkDocs build
// aborts on it (#506). It links into the repository only with the documentation contract:
// the contract's GitHub Actions workflow is what establishes that the repository lives on
// GitHub, and it requires RepositoryOwner and RepositoryName, the repository the manifest
// declares (a fork's manifest names the fork). The HISS badge then links AGENTS.md on the
// default branch and the documentation badge links the workflow runs, both by absolute URL.
// Without the contract the block names no repository and the HISS badge renders unlinked: a
// manifest identity alone does not say which forge hosts it.
type State struct {
	BaselineKnown        bool
	LegacyDebtCount      int
	DocumentationEnabled bool
	RepositoryOwner      string
	RepositoryName       string
}

// Reconcile returns content with one current managed block. Human-authored content and
// custom HISS badges remain outside the block and are preserved.
func Reconcile(content string, state State) (string, bool, error) {
	if err := validateState(state); err != nil {
		return "", false, err
	}
	original := content
	content, crlf, err := util.NormalizeLineEndingsStrict(content)
	if err != nil {
		return "", false, fmt.Errorf("README line endings are inconsistent: %w", err)
	}
	first, last, err := util.FindMarkedBlock(content, Start, End)
	if err != nil {
		return "", false, fmt.Errorf("README governance markers: %w", err)
	}
	var cleaned string
	if first >= 0 {
		cleaned = cleanOutsideManagedBlock(content, first, last)
	} else {
		cleaned = stripLegacyGenerated(content)
	}
	block := renderBlock(state, hasCustomBadgeOutsideBlock(cleaned))
	if first >= 0 {
		out, _, replaceErr := util.ReplaceMarkedBlock(cleaned, Start, End, block, util.MaxMarkedBlockLines)
		if replaceErr != nil {
			return "", false, fmt.Errorf("replace README governance block: %w", replaceErr)
		}
		out = util.RestoreLineEndings(out, crlf)
		return out, out != original, nil
	}
	out := insertManagedBlock(cleaned, block)
	out = util.RestoreLineEndings(out, crlf)
	return out, out != original, nil
}

// Verify accepts only the exact output Reconcile would produce for state.
func Verify(content string, state State) error {
	normalized, _, err := util.NormalizeLineEndingsStrict(content)
	if err != nil {
		return fmt.Errorf("README line endings are inconsistent: %w", err)
	}
	first, _, err := util.FindMarkedBlock(normalized, Start, End)
	if err != nil {
		return fmt.Errorf("README governance markers: %w", err)
	}
	if first < 0 {
		return ErrMissing
	}
	out, changed, err := Reconcile(content, state)
	if err != nil {
		return err
	}
	if changed || out != content {
		return ErrStale
	}
	return nil
}

func cleanOutsideManagedBlock(content string, first, last int) string {
	lines := strings.Split(content, "\n")
	prefix := stripLegacyGenerated(strings.Join(lines[:first], "\n"))
	suffix := stripLegacyGenerated(strings.Join(lines[last+1:], "\n"))
	parts := []string{prefix, Start, strings.Join(lines[first+1:last], "\n"), End, suffix}
	return strings.Join(parts, "\n")
}

func stripLegacyGenerated(content string) string {
	content = legacyBadge.ReplaceAllString(content, "")
	for _, legacy := range []string{legacyGovernanceCurrent, legacyGovernanceHISS16} {
		content = strings.ReplaceAll(content, strings.Trim(legacy, "\n"), "")
	}
	return content
}

func hasCustomBadgeOutsideBlock(content string) bool {
	first, last, err := util.FindMarkedBlock(content, Start, End)
	if err != nil {
		return false
	}
	if first >= 0 {
		lines := strings.Split(content, "\n")
		content = strings.Join(append(append([]string{}, lines[:first]...), lines[last+1:]...), "\n")
	}
	return customBadge.MatchString(content)
}

// Reference labels of the block's badge links. Markdown reference labels are document-wide,
// so the prefix keeps them apart from the adopter's own.
const (
	hissBadgeRef    = "praetor-hiss-badge"
	hissAgentsRef   = "praetor-hiss-agents"
	docsBadgeRef    = "praetor-docs-badge"
	docsWorkflowRef = "praetor-docs-runs"
	// hissBadgeImage is the HISS badge's image. renderHISSBadge links it as hissBadgeLink
	// under the documentation contract, which names the repository.
	hissBadgeImage = "![HISS Adopted][" + hissBadgeRef + "]"
	hissBadgeLink  = "[" + hissBadgeImage + "][" + hissAgentsRef + "]"
)

// renderBlock renders the managed block so every line passes markdownlint's MD013 at its
// default 80 columns, tables and strict mode included, for any repository identity: the
// badges are reference-style images whose URLs sit in link reference definitions, which MD013
// always exempts, and the prose is wrapped by hand. Each gate is a paragraph of its own, not
// a list item: MD004 takes a document's list marker style from its first list, and a block
// near the top would otherwise impose its marker on every list the README keeps.
func renderBlock(state State, customHISSBadge bool) string {
	var lines, definitions []string
	lines = append(lines, Start)
	if !customHISSBadge {
		badge, links := renderHISSBadge(state)
		lines = append(lines, badge)
		definitions = append(definitions, links...)
	}
	if state.DocumentationEnabled {
		badge, links := renderDocumentationBadge(state)
		lines = append(lines, badge)
		definitions = append(definitions, links...)
	}
	if len(lines) > 1 {
		lines = append(lines, "")
	}
	lines = append(lines,
		"Praetor manages this repository's declared governance policy. This managed",
		"block records adoption state; it is not a verification certificate.",
		"",
		"**Verification**: `make verify-all` runs the repository's configured",
		"verification cascade.",
		"",
		"**HISS Audit**: `praetorctl audit` enforces policy, generated-surface",
		"integrity, and the debt ratchet.",
		"",
		"**Context Sync**: `praetorctl compile-context --verify` verifies every",
		"generated agent context against `AGENTS.md`.",
		"",
	)
	if state.DocumentationEnabled {
		lines = append(lines, documentationLine, "private scratch-link policy.", "")
	}
	lines = append(lines, baselineParagraph(state)...)
	if len(definitions) > 0 {
		lines = append(append(lines, ""), definitions...)
	}
	return strings.Join(append(lines, End), "\n")
}

func validateState(state State) error {
	if state.LegacyDebtCount < 0 {
		return fmt.Errorf("%w: negative legacy debt count", ErrInvalidState)
	}
	if !state.DocumentationEnabled {
		if state.RepositoryOwner != "" || state.RepositoryName != "" {
			return fmt.Errorf("%w: repository identity without the documentation contract, "+
				"the only evidence that the repository lives on GitHub", ErrInvalidState)
		}
		return nil
	}
	if err := util.ValidateGitHubRepositoryIdentity(state.RepositoryOwner, state.RepositoryName); err != nil {
		return fmt.Errorf("%w: documentation badge identity: %w", ErrInvalidState, err)
	}
	return nil
}

// renderHISSBadge returns the HISS badge line and its reference definitions: the image,
// linked to AGENTS.md on the repository's default branch under the documentation contract
// (State). GitHub resolves blob/HEAD to the default branch, so the link needs no branch name
// and follows a renamed default branch.
func renderHISSBadge(state State) (string, []string) {
	image := "[" + hissBadgeRef + "]: " + hissBadgeURL(state)
	if !state.DocumentationEnabled {
		return hissBadgeImage, []string{image}
	}
	agentsURL := fmt.Sprintf("https://github.com/%s/%s/blob/HEAD/AGENTS.md", state.RepositoryOwner, state.RepositoryName)
	return hissBadgeLink, []string{image, "[" + hissAgentsRef + "]: " + agentsURL}
}

// renderDocumentationBadge returns the documentation gate's badge line and the reference
// definitions of its image and of the workflow's runs page.
func renderDocumentationBadge(state State) (string, []string) {
	workflow := path.Base(markdownassets.WorkflowFile)
	workflowURL := fmt.Sprintf("https://github.com/%s/%s/actions/workflows/%s",
		state.RepositoryOwner, state.RepositoryName, workflow)
	badge := fmt.Sprintf("[![%s][%s]][%s]", markdownassets.StatusContext, docsBadgeRef, docsWorkflowRef)
	return badge, []string{
		"[" + docsBadgeRef + "]: " + workflowURL + "/badge.svg",
		"[" + docsWorkflowRef + "]: " + workflowURL,
	}
}

func hissBadgeURL(state State) string {
	switch {
	case !state.BaselineKnown:
		return "https://img.shields.io/badge/Standards-HISS%20Adopted%20(baseline%20pending)-yellow"
	case state.LegacyDebtCount > 0:
		return fmt.Sprintf("https://img.shields.io/badge/Standards-HISS%%20Adopted%%20(%d%%20baselined)-yellow", state.LegacyDebtCount)
	default:
		return "https://img.shields.io/badge/Standards-HISS%20Adopted-blue"
	}
}

// baselineParagraph is the debt baseline's paragraph, its count on a line of its own so any
// count fits in 80 columns.
func baselineParagraph(state State) []string {
	if !state.BaselineKnown {
		return []string{debtPendingLine, "verification is pending."}
	}
	count := fmt.Sprintf("%d recorded infractions", state.LegacyDebtCount)
	if state.LegacyDebtCount == 1 {
		count = "1 recorded infraction"
	}
	return []string{debtRecordedLine, count + debtRecordedSuffix}
}

func insertManagedBlock(content, block string) string {
	trimmed := strings.TrimRight(content, "\r\n")
	if trimmed == "" {
		return block + "\n"
	}
	lineEnd := strings.Index(trimmed, "\n")
	if strings.HasPrefix(strings.TrimSpace(trimmed), "# ") && lineEnd >= 0 {
		return trimmed[:lineEnd+1] + "\n" + block + "\n" + trimmed[lineEnd+1:] + "\n"
	}
	return trimmed + "\n\n" + block + "\n"
}
