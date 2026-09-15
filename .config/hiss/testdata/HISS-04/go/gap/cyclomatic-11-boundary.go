package p

// Cyclomatic11 has cyclomatic complexity 11, over the HISS-04 cap of 10. gocyclo is
// configured at min-complexity 11 and reports only above it, so this is unreported
// (BUG-830 in .golangci.yml).
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
