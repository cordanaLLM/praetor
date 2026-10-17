package main

import (
	"fmt"
	"os"

	"github.com/cordanaLLM/praetor/internal/clientschema/typegen"
)

// main regenerates the files typegen.Targets lists, relative to the working directory, which
// go generate sets to internal/clientschema; the repository root is two levels up.
func main() {
	if err := typegen.Write("../.."); err != nil {
		fmt.Fprintln(os.Stderr, "typegen:", err)
		os.Exit(1)
	}
}
