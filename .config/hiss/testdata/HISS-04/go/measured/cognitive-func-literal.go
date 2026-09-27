package p

// CognitiveLiteral has cognitive complexity 21 (six nesting levels) and cyclomatic 7, so
// gocyclo stays quiet and gocognit never visits a function literal at all. The hiss scanner
// measures a package-level literal as a function of its own and reports it without
// enforcing it.
var CognitiveLiteral = func(total int) int {
	if total > 0 {
		if total > 1 {
			if total > 2 {
				if total > 3 {
					if total > 4 {
						if total > 5 {
							total++
						}
					}
				}
			}
		}
	}
	return total
}
