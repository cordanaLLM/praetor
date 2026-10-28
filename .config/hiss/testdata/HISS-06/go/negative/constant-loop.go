package p

const maxWorkers = 4

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
