package gap

// An unmarked simulation loop allocating heap memory is invisible to HISS-03.
func Step() []byte {
	var buf []byte
	for i := 0; i < 100; i++ {
		buf = append(buf, byte(i))
	}
	return buf
}
