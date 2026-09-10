package main

import (
	"flag"
	"fmt"
	"os"
)

const mcpVersion = "v1.0.0"

func main() {
	transport := flag.String("transport", "stdio", "Transport protocol: stdio, http, or sse")
	port := flag.Int("port", 8080, "Port for http/sse transport")
	flag.Parse()

	switch *transport {
	case "stdio":
		runStdio()
	case "http", "sse":
		fmt.Printf("standards-mcp %s listening on %s port %d\n", mcpVersion, *transport, *port)
	default:
		fmt.Fprintf(os.Stderr, "Unsupported transport: %s\n", *transport)
		os.Exit(1)
	}
}

func runStdio() {
	// Stdio JSON-RPC 2.0 loop placeholder for Phase 2 implementation
	fmt.Fprintf(os.Stderr, "standards-mcp %s initialized on stdio transport.\n", mcpVersion)
}
