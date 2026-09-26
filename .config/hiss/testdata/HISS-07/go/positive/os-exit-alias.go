package p

import sys "os"

// The alias binds sys to package os, so this is os.Exit under another name.
func Fail() {
	sys.Exit(1)
}
