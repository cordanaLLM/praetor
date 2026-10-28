package p

func worker(int, <-chan string) {}

// BoundedWorkerPool uses an explicit int bound documented by a //hiss:bound directive.
func BoundedWorkerPool(workers int, jobs <-chan string) {
	//hiss:bound workers<=16 clamped by configuration validator before starting pool
	for i := 0; i < workers; i++ {
		go worker(i, jobs)
	}
}
