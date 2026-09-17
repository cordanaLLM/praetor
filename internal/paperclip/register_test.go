package paperclip

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// A Paperclip run reports to an orchestrating agent, so the contract names the internal
// register as its last line, and the rendered rules and a reload keep it.
func TestHarnessContractStatesTheInternalRegister(t *testing.T) {
	repo := t.TempDir()
	h, err := SynthesizeHarness(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	directive := config.RegisterDirective(config.TextRegisterInternal)
	if len(h.OperatingContract) != 6 || h.OperatingContract[5] != directive {
		t.Fatalf("contract = %q, want the internal register directive as the sixth line", h.OperatingContract)
	}
	for _, other := range []config.TextRegister{config.TextRegisterSocial, config.TextRegisterDocs} {
		if strings.Contains(strings.Join(h.OperatingContract, "\n"), config.RegisterDirective(other)) {
			t.Fatalf("contract must not name the %s register", other)
		}
	}
	if err := validateHarnessValues(h.OperatingContract); err != nil {
		t.Fatalf("the extended contract must stay within the harness bounds: %v", err)
	}
	if rules := renderRules(h); strings.Count(rules, "- "+directive+"\n") != 1 {
		t.Fatalf("rendered rules must list the directive once:\n%s", rules)
	}
}
