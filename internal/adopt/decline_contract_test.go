package adopt

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// Boundary (#600): every step of the adoption chain is mandatory or carries an audit decline
// contract, never both and never neither, so a step that becomes declinable must say what audit
// does with its artefact. A retained decline names what audit still requires; no other does.
func TestDeclineContracts_Boundary_CoverEveryStep(t *testing.T) {
	steps := adoptStepNames()
	inChain := make(map[string]bool, len(steps))
	for _, step := range steps {
		inChain[step] = true
		_, mandatory := mandatoryArtifacts[step]
		_, contracted := declineContracts[step]
		if mandatory == contracted {
			t.Errorf("step %q: mandatory=%v, decline contract=%v; want exactly one", step, mandatory, contracted)
		}
	}
	for step, contract := range declineContracts {
		if !inChain[step] {
			t.Errorf("decline contract %q names no step of the chain", step)
		}
		if (contract.audit == DeclineAuditRetained) != (contract.retains != "") {
			t.Errorf("step %q: audit %q with retains %q", step, contract.audit, contract.retains)
		}
	}
	listed := DeclineContracts()
	if len(listed) != len(declineContracts) {
		t.Fatalf("DeclineContracts lists %d steps, want %d", len(listed), len(declineContracts))
	}
	for i := 1; i < len(listed); i++ {
		if stepIndex(steps, listed[i-1].Step) > stepIndex(steps, listed[i].Step) {
			t.Fatalf("DeclineContracts is not in chain order at %q", listed[i].Step)
		}
	}
}

// declineGuide is the guide whose table states every decline contract to adopters.
var declineGuide = filepath.Join("..", "..", "docs", "guides", "adoption-verification.md")

// Boundary (#600): the guide's table of declined steps lists every contract in chain order with
// its audit class, so the documented contract cannot drift from the one audit applies.
func TestDeclineContractsTableMatchesTheGuide(t *testing.T) {
	data, err := os.ReadFile(declineGuide)
	if err != nil {
		t.Fatal(err)
	}
	tableRows := testsupport.MarkdownTableRowsUnderHeading(string(data), "### What audit does with a declined step")
	if len(tableRows) == 0 {
		t.Fatalf("%s has no section on declined steps", declineGuide)
	}
	var rows []string
	for _, cells := range tableRows {
		if len(cells) >= 2 {
			step := strings.Trim(cells[0], "`")
			class, _, _ := strings.Cut(cells[1], ":")
			class, _, _ = strings.Cut(class, " ")
			rows = append(rows, step+"="+strings.TrimSuffix(class, ","))
		}
	}
	contracts := DeclineContracts()
	want := make([]string, 0, len(contracts))
	for _, contract := range contracts {
		want = append(want, contract.Step+"="+string(contract.Audit))
	}
	if strings.Join(rows, "\n") != strings.Join(want, "\n") {
		t.Fatalf("%s table rows:\n%s\nwant, in chain order:\n%s", declineGuide, strings.Join(rows, "\n"), strings.Join(want, "\n"))
	}
}

func stepIndex(steps []string, name string) int {
	for i, step := range steps {
		if step == name {
			return i
		}
	}
	return -1
}

func declineManifest(steps ...string) *config.Manifest {
	return &config.Manifest{Version: 1, Adoption: &config.AdoptionPolicy{Decline: steps}}
}

// Positive: a skipped decline reads as declined with nothing retained and renders the pass line;
// a retained one names what audit still requires in its line and in a failure it narrows.
func TestAuditDecline_Positive(t *testing.T) {
	labels, err := AuditDecline(declineManifest("labels"), "labels")
	if err != nil || !labels.Declined || labels.Retains != "" {
		t.Fatalf("labels verdict = %+v, %v", labels, err)
	}
	if got := labels.Line("Label taxonomy .config/labels.yaml"); got != "[PASS] Label taxonomy .config/labels.yaml declined by adoption.decline." {
		t.Fatalf("labels line = %q", got)
	}
	harness, err := AuditDecline(declineManifest("agent-harness"), "agent-harness")
	if err != nil || !harness.Declined || !strings.Contains(harness.Retains, "text register block") {
		t.Fatalf("agent-harness verdict = %+v, %v", harness, err)
	}
	if got := harness.Line("Agent harness"); !strings.Contains(got, "declined by adoption.decline; audit still requires the text register block") {
		t.Fatalf("agent-harness line = %q", got)
	}
	cause := errors.New("AGENTS.md has no text register block")
	narrowed := harness.Narrow(cause)
	if !errors.Is(narrowed, cause) || !strings.Contains(narrowed.Error(), "adoption.decline lists agent-harness, which does not cover this check") {
		t.Fatalf("narrowed = %v", narrowed)
	}
}

// Negative: an unknown or mandatory entry in the list fails every gate closed, and a step no
// decline can name is refused rather than read as undeclined.
func TestAuditDecline_Negative(t *testing.T) {
	for name, manifest := range map[string]*config.Manifest{
		"unknown":   declineManifest("label"),
		"mandatory": declineManifest("documentation-gate"),
	} {
		if _, err := AuditDecline(manifest, "labels"); err == nil {
			t.Errorf("%s decline accepted", name)
		}
	}
	if _, err := AuditDecline(nil, "manifest"); err == nil || !strings.Contains(err.Error(), "cannot be declined") {
		t.Fatalf("mandatory step lookup: %v", err)
	}
	if _, err := AuditDecline(nil, "no-such-step"); err == nil {
		t.Fatal("unknown step lookup accepted")
	}
}

// Boundary: a manifest without adoption.decline declines nothing, and an undeclined or
// skipped verdict leaves a failure and a nil error exactly as they were.
func TestAuditDecline_Boundary(t *testing.T) {
	verdict, err := AuditDecline(nil, "agent-harness")
	if err != nil || verdict.Declined || verdict.Retains != "" {
		t.Fatalf("nil manifest verdict = %+v, %v", verdict, err)
	}
	cause := errors.New("drift")
	unchanged := func(got error) bool { return errors.Is(got, cause) && got.Error() == cause.Error() }
	if got := verdict.Narrow(cause); !unchanged(got) {
		t.Fatalf("undeclined Narrow changed the error: %v", got)
	}
	labels, err := AuditDecline(declineManifest("labels"), "labels")
	if err != nil || !unchanged(labels.Narrow(cause)) || labels.Narrow(nil) != nil {
		t.Fatalf("skipped decline narrowed a failure: %v", err)
	}
	if verdict, err := AuditDecline(declineManifest(" Labels "), "labels"); err != nil || !verdict.Declined {
		t.Fatalf("normalised decline entry = %+v, %v", verdict, err)
	}
	// Adoption's report line for a declined step: unchanged for a skipped decline, extended with
	// what audit still requires for a retained one.
	if got := declinedDetail("labels"); got != "Declined by adoption.decline in .standards.yaml" {
		t.Fatalf("labels detail = %q", got)
	}
	if got := declinedDetail("agent-harness"); !strings.Contains(got, "; audit still requires the text register block in AGENTS.md") {
		t.Fatalf("agent-harness detail = %q", got)
	}
}

// The label taxonomy and hook configuration gates the CLI and MCP audits share: declined, the
// missing file passes with the decline named (Positive); undeclined it fails (Negative); a
// present file passes whatever the decline, and an invalid list fails closed (Boundary).
func TestAuditSharedDeclineGates_3D(t *testing.T) {
	root := t.TempDir()
	if line, err := AuditLabelTaxonomy(declineManifest("labels"), root); err != nil || !strings.Contains(line, "declined by adoption.decline") {
		t.Fatalf("declined labels: %q, %v", line, err)
	}
	if hooks, err := AuditGitHookConfig(t.Context(), declineManifest("git-hooks"), root); err != nil || !hooks.Declined ||
		!strings.Contains(hooks.Line, "Git hooks (lefthook.yml and its activation) declined by adoption.decline") {
		t.Fatalf("declined git-hooks: %+v, %v", hooks, err)
	}
	if _, err := AuditLabelTaxonomy(nil, root); err == nil || !strings.Contains(err.Error(), "labels.yaml is missing") {
		t.Fatalf("missing labels passed: %v", err)
	}
	if _, err := AuditGitHookConfig(t.Context(), declineManifest("labels"), root); err == nil || !strings.Contains(err.Error(), "lefthook.yml configuration is missing") {
		t.Fatalf("missing lefthook.yml passed: %v", err)
	}
	mustWrite(t, filepath.Join(root, ".config", "labels.yaml"), "version: 1\nlabels: []\n")
	mustWrite(t, filepath.Join(root, lefthookFile), "pre-commit:\n  commands: {}\n")
	if line, err := AuditLabelTaxonomy(nil, root); err != nil || !strings.HasPrefix(line, "[PASS] Repository label taxonomy") {
		t.Fatalf("present labels: %q, %v", line, err)
	}
	if hooks, err := AuditGitHookConfig(t.Context(), nil, root); err != nil || hooks.Declined || !strings.HasPrefix(hooks.Line, "[PASS] Git hook configuration") {
		t.Fatalf("present lefthook.yml: %+v, %v", hooks, err)
	}
	if _, err := AuditLabelTaxonomy(declineManifest("lables"), root); err == nil {
		t.Fatal("invalid decline list passed the labels gate")
	}
}
