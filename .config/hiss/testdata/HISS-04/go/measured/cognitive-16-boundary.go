package p

// Cognitive16 has cognitive complexity 16, one over the HISS-04 cap of 15. The hiss
// scanner measures it and reports it without enforcing it. gocognit, configured at
// min-complexity 16 in .golangci.yml, reports only above that and stays quiet.
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
