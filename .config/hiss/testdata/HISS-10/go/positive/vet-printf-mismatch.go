package p

import "fmt"

// The %d verb is applied to a string argument.
func PrintfMismatch(name string) string {
	return fmt.Sprintf("%d", name)
}
