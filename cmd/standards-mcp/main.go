package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

const mcpVersion = "v1.0.0"

func main() {
	transport := flag.String("transport", "stdio", "Transport protocol: stdio, http, or sse")
	host := flag.String("host", "127.0.0.1", "Host address for http/sse transport")
	port := flag.Int("port", 8080, "Port for http/sse transport")
	rootDir := flag.String("root", ".", "Repository root directory")
	versionFlag := flag.Bool("version", false, "Print server version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("standards-mcp %s\n", mcpVersion)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server, err := NewServer(*rootDir, mcpVersion)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize standards-mcp server: %v\n", err)
		os.Exit(1)
	}

	if err := runTransport(ctx, server, *transport, *host, *port); err != nil {
		fmt.Fprintf(os.Stderr, "standards-mcp server error: %v\n", err)
		os.Exit(1)
	}
}

// runTransport delegates execution to the selected transport runner.
func runTransport(ctx context.Context, server *Server, transport, host string, port int) error {
	addr := fmt.Sprintf("%s:%d", host, port)

	switch transport {
	case "stdio":
		return server.RunStdio(ctx)

	case "http":
		fmt.Fprintf(os.Stderr, "standards-mcp %s listening on HTTP at http://%s/mcp\n", mcpVersion, addr)
		return server.RunHTTP(ctx, addr)

	case "sse":
		fmt.Fprintf(os.Stderr, "standards-mcp %s listening on SSE at http://%s/sse\n", mcpVersion, addr)
		return server.RunSSE(ctx, addr)

	default:
		return fmt.Errorf("unsupported transport: %s (supported: stdio, http, sse)", transport)
	}
}
