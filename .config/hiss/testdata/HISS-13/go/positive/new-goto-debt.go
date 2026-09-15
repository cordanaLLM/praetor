package p

// Added debt the scanner counts: HISS-01 goto raises V_total, so the ratchet refuses it.
func Retry(limit int) int {
	i := 0
again:
	i++
	if i < limit {
		goto again
	}
	return i
}
