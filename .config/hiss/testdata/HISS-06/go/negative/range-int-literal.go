package p

// process demonstrates a worker loop bounded by ranging over an integer literal.
func process(int) {}

// BoundedByRangeIntLiteral launches goroutines from a range loop over an integer literal.
func BoundedByRangeIntLiteral() {
	for i := range 8 {
		go process(i)
	}
}
