package fixture

func mark(seen map[int]bool, x int) { seen[x] = true }

func dedupe(xs []int) {
	seen := map[int]bool{}
	for _, x := range xs {
		if seen[x] {
			continue
		}
		mark(seen, x)
	}
}
