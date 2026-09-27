package p

// Cyclomatic11 has cyclomatic complexity 11, one over the HISS-04 cap of 10. The hiss
// scanner measures it and reports it without enforcing it. gocyclo, configured at
// min-complexity 11 in .golangci.yml, reports only above that and stays quiet.
func Cyclomatic11(total int) int {
	if total > 0 {
		total++
	}
	if total > 1 {
		total++
	}
	if total > 2 {
		total++
	}
	if total > 3 {
		total++
	}
	if total > 4 {
		total++
	}
	if total > 5 {
		total++
	}
	if total > 6 {
		total++
	}
	if total > 7 {
		total++
	}
	if total > 8 {
		total++
	}
	if total > 9 {
		total++
	}
	return total
}
