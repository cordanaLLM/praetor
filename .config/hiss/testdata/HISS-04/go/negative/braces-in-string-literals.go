package p

// BraceStrings holds braces inside string literals; they must not desynchronise the
// function-length accounting.
func BraceStrings() string {
	open := "{"
	closed := "}"
	both := "{ nested { deeper } }"
	return open + closed + both
}
