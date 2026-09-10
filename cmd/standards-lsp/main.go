package main

import (
	"fmt"
	"os"
)

const lspVersion = "v1.0.0"

func main() {
	// Language Server Protocol daemon placeholder for Phase 3 implementation
	fmt.Fprintf(os.Stderr, "standards-lsp %s initialized on stdio transport.\n", lspVersion)
}
