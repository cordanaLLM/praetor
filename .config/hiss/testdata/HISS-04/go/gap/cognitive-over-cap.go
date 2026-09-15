package p

// Cognitive17 has cognitive complexity 17 (five nesting levels plus two flat branches),
// over the HISS-04 cap of 15, while its cyclomatic complexity stays at 8.
func Cognitive17(total int) int {
	if total > 0 {
		if total > 1 {
			if total > 2 {
				if total > 3 {
					if total > 4 {
						total++
					}
				}
			}
		}
	}
	if total > 0 {
		total++
	}
	if total > 1 {
		total++
	}
	return total
}
