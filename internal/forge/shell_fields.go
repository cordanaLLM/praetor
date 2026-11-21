package forge

import (
	"fmt"

	"github.com/cordanaLLM/praetor/internal/hiss"
)

// ErrShellReadLimit identifies a script whose bounded reader cannot inspect every word.
var ErrShellReadLimit = hiss.ErrShellReadLimit

// ShellFields reads shell words for enforcing consumers, refusing named unproven bodies.
// Quotes and escapes stay in words; comments and escaped newlines are removed.
// Operators ;, &, &&, | and || are separate fields even without surrounding whitespace.
func ShellFields(script string) ([]string, error) {
	return shellFields(script, true)
}

// ProvenShellFields reads execution evidence, withholding credit from unproven bodies.
// Workflow proof readers use this direction; enforcement and claim readers use ShellFields.
func ProvenShellFields(script string) ([]string, error) {
	return shellFields(script, false)
}

// shellFields applies the consumer's named safe default to the shared ownership decision.
func shellFields(script string, refuseUnproven bool) ([]string, error) {
	script, docs, err := hiss.SplitHereDocs(script)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrShellReadLimit, err)
	}
	if refuseUnproven {
		for i := 0; i < len(docs) && i < hiss.MaxShellFields; i++ {
			if docs[i].Reason != "" {
				return nil, fmt.Errorf("%w: here-document: %s", ErrShellReadLimit, docs[i].Reason)
			}
		}
	}
	return hiss.ShellCodeFields(script)
}
