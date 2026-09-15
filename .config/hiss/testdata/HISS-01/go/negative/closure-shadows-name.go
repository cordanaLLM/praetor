package p

// Tally is shadowed by a local closure of the same name, so the call reaches the local
// rather than recursing.
func Tally(n int) int {
	Tally := func(k int) int { return k }
	return Tally(n)
}
