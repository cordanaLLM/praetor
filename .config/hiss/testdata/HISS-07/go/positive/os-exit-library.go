package p

import "os"

// A library function ending the process; only main.main may decide that.
func Fail() {
	os.Exit(1)
}
