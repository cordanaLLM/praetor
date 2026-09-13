//go:build !unix

package dogfood

import "os"

func openSuiteConfigFile(root *os.Root, name string) (*os.File, error) {
	return root.Open(name)
}
