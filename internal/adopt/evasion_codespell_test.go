package adopt

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
)

// Positive: the emitted block_evasion.py contains no codespell-flagged word fragments
// like "grap", and passes codespell cleanly when codespell is installed on PATH.
func TestBlockEvasionPY_3D_Positive_CodespellClean(t *testing.T) {
	rendered := buildBlockEvasionPY()
	if strings.Contains(rendered, `r"hql\b`) || strings.Contains(rendered, `grap"`) {
		t.Fatalf("buildBlockEvasionPY contains word fragment 'grap'/'hql' split across literals")
	}
	codespellPath, err := exec.LookPath("codespell")
	if err != nil {
		t.Skip("codespell not installed")
	}
	cmd := exec.Command(codespellPath, "-")
	cmd.Stdin = strings.NewReader(rendered)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("codespell reported issues on buildBlockEvasionPY(): %v\noutput:\n%s", err, string(out))
	}
}

// Negative: verify that a split fragment 'grap' is flagged by codespell or the fragment check.
func TestBlockEvasionPY_3D_Negative_DetectsWordSplitAcrossLiterals(t *testing.T) {
	sample := `r"grap"\n        r"hql\b"`
	if !strings.Contains(sample, `grap"`) || !strings.Contains(sample, `r"hql\b`) {
		t.Fatalf("sample must contain split literals")
	}
	codespellPath, err := exec.LookPath("codespell")
	if err != nil {
		t.Skip("codespell not installed")
	}
	cmd := exec.Command(codespellPath, "-")
	cmd.Stdin = bytes.NewReader([]byte("r\"grap\"\n"))
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("codespell expected to flag 'grap', but passed: %s", string(out))
	}
	if !strings.Contains(string(out), "grap") {
		t.Fatalf("codespell output did not name 'grap': %s", string(out))
	}
}

// Boundary: layout token literals must preserve the whole word without splitting ASCII words.
func TestBlockEvasionPY_3D_Boundary_LayoutKeepsWordBoundary(t *testing.T) {
	token := pyToken{
		text: `(?m)^(?:(?:[^\Wg]\w*|g(?:[^\Wh]\w*|h\w+)?)?[^\w\n])*(gh)\b[^\n]*\bapi\s+graphql\b[^\n]*\bmergePullRequest\b`,
		kind: pyRaw,
	}
	literals := token.literals(79)
	for i, lit := range literals {
		if strings.Contains(lit, `grap"`) || strings.HasPrefix(lit, `r"hql`) {
			t.Fatalf("literal %d (%s) splits 'graphql'", i, lit)
		}
	}
}
