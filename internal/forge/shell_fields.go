package forge

import (
	"fmt"

	"github.com/cordanaLLM/praetor/internal/hiss"
)

// ErrShellReadLimit identifies a script whose bounded reader cannot inspect every word.
var ErrShellReadLimit = hiss.ErrShellReadLimit

// ShellFields reads shell words and control operators without executing or expanding them.
// Quotes and escapes stay in words; quoted whitespace and operators stay literal. Comments
// are removed, backslash-newline continues a command, and an unquoted newline becomes ";".
// Operators ;, &, &&, | and || are separate fields even without surrounding whitespace.
// All readers share SplitHereDocs execution classification and the bounded code tokenizer.
func ShellFields(script string) ([]string, error) {
	script, _, err := hiss.SplitHereDocs(script)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrShellReadLimit, err)
	}
	return hiss.ShellCodeFields(script)
}
