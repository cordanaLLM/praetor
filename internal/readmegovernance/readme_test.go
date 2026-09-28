package readmegovernance

import (
	"errors"
	"math"
	"regexp"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// documentationItem is the documentation gate's paragraph in the rendered block.
const documentationItem = "**Documentation**: `make docs-lint` enforces locked Markdown style and the\n" +
	"private scratch-link policy.\n"

// linkReferenceDefinition matches a line MD013 exempts even in strict mode.
var linkReferenceDefinition = regexp.MustCompile(`^\[[^\]]+\]: \S+$`)

// strictOverlongLines returns every line of the managed block in md longer than MD013's
// default 80 columns, link reference definitions aside: what MD013 reports with strict: true.
func strictOverlongLines(md string) []string {
	start, end := strings.Index(md, Start), strings.Index(md, End)
	if start < 0 || end < start {
		return []string{"no managed block"}
	}
	var long []string
	for _, line := range strings.Split(md[start:end+len(End)], "\n") {
		if len(line) > testsupport.MarkdownLineLimit && !linkReferenceDefinition.MatchString(line) {
			long = append(long, line)
		}
	}
	return long
}

// longestIdentityState is the widest rendering GitHub's limits allow: a 39-character owner,
// a 100-character repository name, the largest debt count, and the documentation gate on.
func longestIdentityState() State {
	return State{
		BaselineKnown: true, LegacyDebtCount: math.MaxInt, DocumentationEnabled: true,
		RepositoryOwner: strings.Repeat("o", 39), RepositoryName: strings.Repeat("r", 100),
	}
}

// Positive: every rendering passes markdownlint's default rules as MarkdownFindings replays
// them, and MD013's strict mode as well: only link reference definitions, which MD013 always
// exempts, pass 80 columns.
func TestRenderedBlockPassesMD013Positive(t *testing.T) {
	for name, state := range map[string]State{
		"pending baseline":  {},
		"one infraction":    {BaselineKnown: true, LegacyDebtCount: 1},
		"documentation":     {BaselineKnown: true, DocumentationEnabled: true, RepositoryOwner: "acme", RepositoryName: "widgets"},
		"longest identity":  longestIdentityState(),
		"custom HISS badge": {BaselineKnown: true},
	} {
		input := "# Widgets\n\nHuman text.\n"
		if name == "custom HISS badge" {
			input = "# Widgets\n\n[![HISS policy](https://img.shields.io/badge/Custom-HISS-blue)](policy.md)\n"
		}
		out, _, err := Reconcile(input, state)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if findings := testsupport.MarkdownFindings(out); len(findings) != 0 {
			t.Fatalf("%s: rendered README fails markdownlint defaults: %v\n%s", name, findings, out)
		}
		if long := strictOverlongLines(out); len(long) != 0 {
			t.Fatalf("%s: lines past 80 columns under strict MD013: %q", name, long)
		}
		// MD004 takes the list marker style from a document's first list; a list in the block
		// would set it for every list the README keeps below.
		block := out[strings.Index(out, Start):strings.Index(out, End)]
		if item := listItem.FindString(block); item != "" {
			t.Fatalf("%s: the block opens a list with %q", name, item)
		}
	}
}

// listItem matches the first line of a Markdown list item.
var listItem = regexp.MustCompile(`(?m)^\s*(?:[-*+]|[0-9]+[.)]) .*$`)

// Negative: the block this renderer replaced, a table beside inline badges, fails MD013 at
// its defaults (the 142-column prose line and the table rows) and under strict mode (the
// inline badge), so the check above is not vacuous.
func TestRenderedBlockPassesMD013Negative(t *testing.T) {
	previous := "# Widgets\n\n" + Start + "\n" +
		"[![Documentation Governance](https://github.com/acme/widgets/actions/workflows/praetor-docs.yml/badge.svg)](https://github.com/acme/widgets/actions/workflows/praetor-docs.yml)\n\n" +
		"Praetor manages this repository's declared governance policy. This managed block records adoption state; it is not a verification certificate.\n\n" +
		"| Gate | Command | Contract |\n| :--- | :--- | :--- |\n" +
		"| **Documentation** | `make docs-lint` | Enforces locked Markdown style and private scratch-link policy |\n" + End + "\n"
	if findings := strings.Join(testsupport.MarkdownFindings(previous), "\n"); strings.Count(findings, "MD013") != 2 {
		t.Fatalf("the previous block's prose line and table row must fail MD013:\n%s", findings)
	}
	if long := strictOverlongLines(previous); len(long) != 3 {
		t.Fatalf("strict MD013 must report the badge, prose and row lines: %q", long)
	}
}

// Boundary: the longest identity renders its URLs only into reference definitions, and one
// character past GitHub's owner or repository limit is refused before rendering.
func TestRenderedBlockPassesMD013Boundary(t *testing.T) {
	state := longestIdentityState()
	out, _, err := Reconcile("# Widgets\n", state)
	if err != nil {
		t.Fatal(err)
	}
	url := "https://github.com/" + state.RepositoryOwner + "/" + state.RepositoryName + "/actions/workflows/praetor-docs.yml"
	for _, want := range []string{"[praetor-docs-runs]: " + url + "\n", "\n9223372036854775807 recorded infractions; audit forbids growth.\n"} {
		if !strings.Contains(out, want) {
			t.Fatalf("longest identity rendering lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, url) != 2 {
		t.Fatalf("the workflow URL appears outside its two reference definitions:\n%s", out)
	}
	for _, over := range []State{
		{DocumentationEnabled: true, RepositoryOwner: strings.Repeat("o", 40), RepositoryName: "r"},
		{DocumentationEnabled: true, RepositoryOwner: "o", RepositoryName: strings.Repeat("r", 101)},
	} {
		if _, _, err := Reconcile("# Widgets\n", over); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("identity past GitHub's limits rendered: %v", err)
		}
	}
}

func TestReconcilePositiveRendersTruthfulRecordedDebt(t *testing.T) {
	out, changed, err := Reconcile("# Demo\n\nHuman text.\n", State{BaselineKnown: true, LegacyDebtCount: 2})
	if err != nil || !changed {
		t.Fatalf("reconcile: changed=%v err=%v", changed, err)
	}
	for _, want := range []string{Start, End, "2%20baselined", "2 recorded infractions", "Human text."} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered README missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Compliant") || strings.Contains(out, "conforms") {
		t.Fatalf("adoption state was rendered as certification:\n%s", out)
	}
	again, changed, err := Reconcile(out, State{BaselineKnown: true, LegacyDebtCount: 2})
	if err != nil || changed || again != out {
		t.Fatalf("reconcile is not idempotent: changed=%v err=%v\n%s", changed, err, again)
	}
}

func TestReconcilePositiveRendersDocumentationGateContract(t *testing.T) {
	state := State{
		BaselineKnown:        true,
		DocumentationEnabled: true,
		RepositoryOwner:      "acme",
		RepositoryName:       "widgets",
	}
	out, changed, err := Reconcile("# Demo\n\nHuman text.\n", state)
	if err != nil || !changed {
		t.Fatalf("reconcile documentation contract: changed=%v err=%v", changed, err)
	}
	for _, want := range []string{
		"[![Documentation Governance][praetor-docs-badge]][praetor-docs-runs]\n",
		"[praetor-docs-badge]: https://github.com/acme/widgets/actions/workflows/praetor-docs.yml/badge.svg\n",
		"[praetor-docs-runs]: https://github.com/acme/widgets/actions/workflows/praetor-docs.yml\n",
		documentationItem,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("rendered README missing %q:\n%s", want, out)
		}
	}
	again, changed, err := Reconcile(out, state)
	if err != nil || changed || again != out {
		t.Fatalf("documentation reconciliation is not idempotent: changed=%v err=%v\n%s", changed, err, again)
	}
}

func TestReconcileNegativeRejectsInvalidAndMalformedInputs(t *testing.T) {
	if _, _, err := Reconcile("# Demo\n", State{LegacyDebtCount: -1}); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("negative count: %v", err)
	}
	for _, identity := range [][2]string{
		{"", "widgets"}, {"acme", ""}, {"acme/evil", "widgets"}, {"acme", `widgets\evil`},
		{"acme", "widgets\rmalformed"}, {"acme", "widgets\nmalformed"}, {"acme", "widgets]evil"},
		{"acme", "widgets(evil"},
	} {
		state := State{
			DocumentationEnabled: true, RepositoryOwner: identity[0], RepositoryName: identity[1],
		}
		if _, _, err := Reconcile("# Demo\n", state); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("invalid documentation identity %q/%q: %v", identity[0], identity[1], err)
		}
	}
	malformed := "# Demo\n\n" + Start + "\nunterminated\n"
	if _, _, err := Reconcile(malformed, State{}); err == nil || !strings.Contains(err.Error(), "README governance markers") {
		t.Fatalf("malformed markers: %v", err)
	}
}

func TestReconcileBoundaryPreservesCustomBadgeAndMigratesLegacyOutput(t *testing.T) {
	custom := "[![HISS policy](https://img.shields.io/badge/Custom-HISS-blue)](policy.md)"
	legacy := "[![HISS-16 Compliant](https://img.shields.io/badge/Standards-HISS--16%20Compliant-brightgreen)](AGENTS.md)"
	input := custom + "\n" + legacy + legacyGovernanceHISS16 + "\nHuman tail.\n"
	state := State{
		BaselineKnown:        true,
		DocumentationEnabled: true,
		RepositoryOwner:      "acme",
		RepositoryName:       "widgets",
	}
	out, _, err := Reconcile(input, state)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(out, custom) != 1 || strings.Contains(out, legacy) || strings.Contains(out, "standardsctl audit") {
		t.Fatalf("legacy migration changed custom content or retained generated output:\n%s", out)
	}
	if strings.Contains(out, "Standards-HISS%20Adopted") {
		t.Fatalf("custom HISS badge was duplicated by a managed badge:\n%s", out)
	}
	if strings.Count(out, "[![Documentation Governance]") != 1 {
		t.Fatalf("documentation badge was absent or duplicated with a custom HISS badge:\n%s", out)
	}
	if err := Verify(out, state); err != nil {
		t.Fatalf("verified reconciled output: %v", err)
	}
}

func TestReconcileBoundaryMigratesEveryShippedFalseComplianceBadge(t *testing.T) {
	cases := map[string]string{
		"hiss-16": "[![HISS-16 Compliant](https://img.shields.io/badge/Standards-HISS--16%20Compliant-brightgreen)](AGENTS.md)" + legacyGovernanceHISS16,
		"current": "[![HISS Compliant](https://img.shields.io/badge/Standards-HISS%20Compliant-brightgreen)](AGENTS.md)" + legacyGovernanceCurrent,
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			out, _, err := Reconcile("# Demo\n\n"+input+"\nHuman tail.\n", State{BaselineKnown: true})
			if err != nil {
				t.Fatal(err)
			}
			for _, stale := range []string{"Compliant", "This repository conforms", "standardsctl audit"} {
				if strings.Contains(out, stale) {
					t.Fatalf("legacy %s output retained %q:\n%s", name, stale, out)
				}
			}
			if !strings.Contains(out, "HISS%20Adopted-blue") || !strings.Contains(out, "Human tail.") {
				t.Fatalf("legacy %s output was not migrated safely:\n%s", name, out)
			}
		})
	}
}

func TestVerifyBoundaryDistinguishesMissingAndStale(t *testing.T) {
	if err := Verify("# Demo\n", State{}); !errors.Is(err, ErrMissing) {
		t.Fatalf("missing block: %v", err)
	}
	out, _, err := Reconcile("# Demo\n", State{BaselineKnown: true})
	if err != nil {
		t.Fatal(err)
	}
	stale := strings.Replace(out, "0 recorded infractions", "9 recorded infractions", 1)
	if err := Verify(stale, State{BaselineKnown: true}); !errors.Is(err, ErrStale) {
		t.Fatalf("stale block: %v", err)
	}
}

func TestVerifyNegativeRejectsMissingOrStaleDocumentationContract(t *testing.T) {
	state := State{
		BaselineKnown:        true,
		DocumentationEnabled: true,
		RepositoryOwner:      "acme",
		RepositoryName:       "widgets",
	}
	out, _, err := Reconcile("# Demo\n", state)
	if err != nil {
		t.Fatal(err)
	}
	for name, stale := range map[string]string{
		"missing badge": strings.Replace(out,
			"[![Documentation Governance][praetor-docs-badge]][praetor-docs-runs]\n", "", 1),
		"stale badge": strings.Replace(out, "github.com/acme/widgets/", "github.com/acme/old-widgets/", 1),
		"missing row": strings.Replace(out, documentationItem, "", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := Verify(stale, state); !errors.Is(err, ErrStale) {
				t.Fatalf("verify stale documentation contract: error=%v, want %v", err, ErrStale)
			}
		})
	}
}

func TestReconcilePositiveAcceptsSynchronizedCRLF(t *testing.T) {
	state := State{
		BaselineKnown:        true,
		LegacyDebtCount:      2,
		DocumentationEnabled: true,
		RepositoryOwner:      "acme",
		RepositoryName:       "widgets",
	}
	lf, _, err := Reconcile("# Demo\n\nHuman text.\n", state)
	if err != nil {
		t.Fatal(err)
	}
	crlf := strings.ReplaceAll(lf, "\n", "\r\n")
	if err := Verify(crlf, state); err != nil {
		t.Fatalf("verify synchronized CRLF README: %v", err)
	}
	again, changed, err := Reconcile(crlf, state)
	if err != nil || changed || again != crlf {
		t.Fatalf("reconcile synchronized CRLF README: changed=%v err=%v\n%q", changed, err, again)
	}
}

func TestReconcileNegativeRepairsStaleCRLFWithoutMixingEndings(t *testing.T) {
	state := State{
		BaselineKnown:        true,
		DocumentationEnabled: true,
		RepositoryOwner:      "acme",
		RepositoryName:       "widgets",
	}
	lf, _, err := Reconcile("# Demo\n\nHuman text.\n", state)
	if err != nil {
		t.Fatal(err)
	}
	crlf := strings.ReplaceAll(lf, "\n", "\r\n")
	stale := strings.Replace(crlf, "0 recorded infractions", "9 recorded infractions", 1)
	if err := Verify(stale, state); !errors.Is(err, ErrStale) {
		t.Fatalf("verify stale CRLF README: error=%v, want %v", err, ErrStale)
	}
	repaired, changed, err := Reconcile(stale, state)
	if err != nil || !changed {
		t.Fatalf("repair stale CRLF README: changed=%v err=%v", changed, err)
	}
	assertOnlyCRLF(t, repaired)
	if !strings.Contains(repaired, "Human text.\r\n") {
		t.Fatalf("repair did not preserve human content: %q", repaired)
	}
}

func TestReconcileBoundaryInsertsBlockIntoCRLFWithoutTerminalNewline(t *testing.T) {
	input := "# Demo\r\n\r\nHuman text."
	state := State{DocumentationEnabled: true, RepositoryOwner: "acme", RepositoryName: "widgets"}
	out, changed, err := Reconcile(input, state)
	if err != nil || !changed {
		t.Fatalf("insert into CRLF README: changed=%v err=%v", changed, err)
	}
	assertOnlyCRLF(t, out)
	if !strings.Contains(out, "Human text.\r\n") {
		t.Fatalf("insert did not preserve human content: %q", out)
	}
	if err := Verify(out, state); err != nil {
		t.Fatalf("verify reconciled CRLF README: %v", err)
	}
}

func TestReconcileRejectsInconsistentLineEndings(t *testing.T) {
	state := State{DocumentationEnabled: true, RepositoryOwner: "acme", RepositoryName: "widgets"}
	for name, input := range map[string]string{
		"mixed":   "# Demo\r\n\nHuman text.\n",
		"lone CR": "# Demo\rHuman text.",
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := Reconcile(input, state); err == nil {
				t.Fatal("inconsistent README line endings accepted")
			}
		})
	}
}

func assertOnlyCRLF(t *testing.T, content string) {
	t.Helper()
	if strings.Count(content, "\n") != strings.Count(content, "\r\n") {
		t.Fatalf("README contains mixed line endings: %q", content)
	}
}
