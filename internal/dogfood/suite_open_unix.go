//go:build unix

package dogfood

import (
	"os"
	"syscall"
)

func openSuiteConfigFile(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
}
