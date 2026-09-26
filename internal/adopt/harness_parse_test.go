package adopt

import (
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"testing"
)

func TestHarnessDirectives_ParseGatedInvariants(t *testing.T) {
	content := buildAgentHarnessDirectives()
	gated, err := hisscatalog.ParseGatedInvariants(content)
	if err != nil {
		t.Fatalf("ParseGatedInvariants failed on harness output: %v", err)
	}
	if len(gated) == 0 {
		t.Fatalf("ParseGatedInvariants found no invariants in harness output")
	}
	foundHISS01 := false
	for _, inv := range gated {
		if inv.ID == "HISS-01" {
			foundHISS01 = true
			break
		}
	}
	if !foundHISS01 {
		t.Errorf("Did not find HISS-01 in parsed invariants")
	}
}
