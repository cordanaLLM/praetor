package p

const maxWorkers = 4

// process demonstrates a constant-bounded worker loop, which should be accepted.
func process(int) {}

// BoundedByConstantAndArray launches goroutines from a constant-bounded counter loop and a
// range over a fixed-length array.
func BoundedByConstantAndArray(slots [4]int) {
	for i := 0; i < maxWorkers; i++ {
		go process(i)
	}
	for _, v := range slots {
		go process(v)
	}
}
