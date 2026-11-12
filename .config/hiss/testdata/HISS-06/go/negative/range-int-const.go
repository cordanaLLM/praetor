package p

const workerCount = 8

// process demonstrates a worker loop bounded by ranging over a declared integer constant.
func process(int) {}

// BoundedByRangeIntConst launches goroutines from a range loop over a declared constant.
func BoundedByRangeIntConst() {
	for i := range workerCount {
		go process(i)
	}
}
