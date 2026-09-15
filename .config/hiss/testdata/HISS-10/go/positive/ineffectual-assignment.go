package p

// The first value assigned to total is overwritten before it is ever read.
func Ineffectual(in int) int {
	total := in * 2
	total = in * 3
	return total
}
