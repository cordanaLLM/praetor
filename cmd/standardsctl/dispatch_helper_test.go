package main

import "context"

// dispatchCommand runs cmd the way main.main does, from a fresh process root, so the tests
// exercise the dispatch path of the binary itself.
func dispatchCommand(cmd string, args []string) error {
	return dispatchCommandFrom(context.Background(), cmd, args)
}
