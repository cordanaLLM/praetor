package compiler

import (
	"errors"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

// preRegisterAdoption returns an AGENTS.md as adoption wrote it before the text register
// section existed: a 75-line harness (agentcontext.EngineBlockReserve) without register
// markers, followed by adopter lines of repository text.
func preRegisterAdoption(adopter int) string {
	lines := []string{"<!-- markdownlint-disable MD013 -->", "# acme/widget " + agentcontext.HarnessTitleSuffix}
	for i := 0; i < agentcontext.EngineBlockReserve-3; i++ {
		lines = append(lines, "- engine rule.")
	}
	lines = append(lines, HarnessEndMarker)
	for i := 0; i < adopter; i++ {
		lines = append(lines, "- adopter rule.")
	}
	return strings.Join(lines, "\n") + "\n"
}

// #1232: compile-context appends the text register section to an AGENTS.md adopted before it
// existed. Those lines are engine text, so the splice no longer pushes an unchanged adopter
// file over the budget ("304 lines, budget 300" on lusoris/sveltesentio).
func TestSpliceRegisterBlockChargesAdopterTextOnly(t *testing.T) {
	block, err := config.RenderRegisterBlock(config.DefaultRegisterPolicy(), false)
	if err != nil {
		t.Fatalf("render defaults: %v", err)
	}
	cases := []struct {
		name    string
		adopter int
		wantErr bool
	}{
		{"positive: unchanged adopter file from before the register section", 212, false},
		{"boundary: adopter text at its allowance", agentcontext.MaxLineBudget - agentcontext.EngineBlockReserve, false},
		{"boundary: one adopter line over", agentcontext.MaxLineBudget - agentcontext.EngineBlockReserve + 1, true},
		{"negative: adopter text far over", 260, true},
	}
	for _, tc := range cases {
		out, changed, err := SpliceRegisterBlock(preRegisterAdoption(tc.adopter), block)
		if tc.wantErr {
			if !errors.Is(err, util.ErrMarkedBlockBudget) || !strings.Contains(err.Error(), "adopter allowance 225") {
				t.Errorf("%s: error = %v, want %v naming the adopter allowance", tc.name, err, util.ErrMarkedBlockBudget)
			}
			continue
		}
		if err != nil || !changed || !strings.Contains(out, config.RegisterBlockStart) {
			t.Errorf("%s: changed=%v err=%v", tc.name, changed, err)
		}
	}
}
