package p

// Cyclomatic10 sits exactly on the HISS-04 cyclomatic cap of 10.
func Cyclomatic10(total int) int {
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
	return total
}
