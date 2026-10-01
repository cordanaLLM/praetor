package util

// EnclosedParens returns the text inside the parenthesis opening at index open of text, up to its
// matching close, or the rest of text after open when it never closes. Nested pairs count, so
// "(a (b) c)" yields "a (b) c". The scan reads bytes and stops at len(text) (HISS-02). The HISS-01
// Rust scanner reads a parameter list with it and the required-context discovery reads a job
// condition's group with it (internal/forge/workflow_guard.go), so the two share one matching
// rule (HISS-19). An open outside text, or one that is not at a "(", yields "".
func EnclosedParens(text string, open int) string {
	if open < 0 || open >= len(text) || text[open] != '(' {
		return ""
	}
	depth := 0
	for i := open; i < len(text); i++ {
		switch text[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return text[open+1 : i]
			}
		}
	}
	return text[open+1:]
}
