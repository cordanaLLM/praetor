package p

// Cognitive16 has cognitive complexity 16, over the HISS-04 cap of 15. gocognit is
// configured at min-complexity 16 and reports only above it, so this is unreported
// (BUG-830 in .golangci.yml).
func Cognitive16(total int) int {
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
	return total
}
