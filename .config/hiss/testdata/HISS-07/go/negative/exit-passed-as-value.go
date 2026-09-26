package p

import "os"

// The entry point hands os.Exit to a library as a value; nothing here calls it.
func Install(exit func(int)) {}

func Wire() {
	Install(os.Exit)
}
