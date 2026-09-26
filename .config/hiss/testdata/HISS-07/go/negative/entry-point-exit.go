package main

import "os"

// main.main is the binary entry point, where the abort policy allows ending the process.
func main() {
	if len(os.Args) > 3 {
		panic("usage")
	}
	os.Exit(run())
}

func run() int { return 0 }
