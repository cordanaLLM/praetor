package p

// Discarding a channel receive cannot discard an error.
func H(ch chan int) {
	_ = <-ch
}
