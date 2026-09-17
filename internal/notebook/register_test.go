package notebook

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// A notebook pack produces documents, so its prompt asks for the docs register, once, as
// the last line, and never for the telegraphic register of agent-to-agent traffic.
func TestGenerationPromptStatesTheDocsRegister(t *testing.T) {
	prompt := generationPrompt(strings.Repeat("a", 64))
	directive := config.RegisterDirective(config.TextRegisterDocs)
	if directive == "" || strings.Count(prompt, directive) != 1 || !strings.HasSuffix(prompt, directive+"\n") {
		t.Fatalf("prompt must end with the docs register directive exactly once:\n%s", prompt)
	}
	if strings.Contains(prompt, config.RegisterDirective(config.TextRegisterInternal)) {
		t.Fatal("a document prompt must not ask for the internal register")
	}
	if empty := generationPrompt(""); !strings.HasSuffix(empty, directive+"\n") {
		t.Fatal("the directive must not depend on the bundle hash")
	}
}
