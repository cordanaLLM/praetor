package adopt

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
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
	stdinCtx, err := util.WithCommandStdin(t.Context(), []byte(rendered))
	if err != nil {
		t.Fatal(err)
	}
	out, err := util.RunCommand(stdinCtx, "", codespellPath, "-")
	if err != nil {
		t.Fatalf("codespell reported issues on buildBlockEvasionPY(): %v\noutput:\n%s", err, out)
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
	stdinCtx, err := util.WithCommandStdin(t.Context(), []byte("r\"grap\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := util.RunCommand(stdinCtx, "", codespellPath, "-")
	if err == nil {
		t.Fatalf("codespell expected to flag 'grap', but passed: %s", out)
	}
	if !strings.Contains(err.Error(), "grap") && !strings.Contains(out, "grap") {
		t.Fatalf("codespell output did not name 'grap': %v %s", err, out)
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
