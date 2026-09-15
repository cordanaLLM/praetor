package p

// A var declaration shadows the function name just as a short declaration does.
func Reduce(n int) int {
	var Reduce = func(k int) int { return k }
	return Reduce(n)
}
