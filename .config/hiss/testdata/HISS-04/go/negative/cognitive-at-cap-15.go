package p

// Cognitive15 sits exactly on the HISS-04 cognitive cap of 15.
func Cognitive15(total int) int {
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
	return total
}
