package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

// mcpVersion is set to dev-<source SHA256> by scripts/dev_mcp.py at build time.
var mcpVersion = "v1.0.0"

// authTokenEnv names the environment variable consulted when -auth-token is empty.
const authTokenEnv = "STANDARDS_MCP_TOKEN"

func main() {
	transport := flag.String("transport", "stdio", "Transport protocol: stdio, http, or sse")
	host := flag.String("host", "127.0.0.1", "Host address for http/sse transport")
	port := flag.Int("port", 8080, "Port for http/sse transport")
	rootDir := flag.String("root", ".", "Repository root directory; every path argument is confined to it")
	allowOutside := flag.Bool("allow-outside-root", false, "Permit path arguments that resolve outside -root (cross-repository adoption, workstation harvests)")
	allowRemote := flag.Bool("allow-remote-benchmarks", false, "Permit standards_dogfood to clone the curated public benchmark repositories")
	authToken := flag.String("auth-token", "", "Bearer token required on every http/sse request (default: $"+authTokenEnv+"); mandatory when -host is not loopback")
	origins := flag.String("allowed-origins", "", "Comma-separated browser origins accepted on http/sse in addition to loopback (e.g. https://ide.example)")
	versionFlag := flag.Bool("version", false, "Print server version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Printf("standards-mcp %s\n", mcpVersion)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	token := *authToken
	if token == "" {
		token = os.Getenv(authTokenEnv)
	}

	server, err := NewServerWithOptions(ServerOptions{
		RootDir:               *rootDir,
		Version:               mcpVersion,
		AllowOutsideRoot:      *allowOutside,
		AllowRemoteBenchmarks: *allowRemote,
		AuthToken:             token,
		AllowedOrigins:        splitOrigins(*origins),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize standards-mcp server: %v\n", err)
		os.Exit(1)
	}

	if err := runTransport(ctx, server, *transport, *host, *port); err != nil {
		fmt.Fprintf(os.Stderr, "standards-mcp server error: %v\n", err)
		os.Exit(1)
	}
}

// splitOrigins parses the comma-separated -allowed-origins value, dropping blanks.
func splitOrigins(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// runTransport delegates execution to the selected transport runner.
func runTransport(ctx context.Context, server *Server, transport, host string, port int) error {
	addr := fmt.Sprintf("%s:%d", host, port)

	switch transport {
	case "stdio":
		return server.RunStdio(ctx)

	case "http":
		fmt.Fprintf(os.Stderr, "standards-mcp %s listening on HTTP at http://%s/\n", mcpVersion, addr)
		return server.RunHTTP(ctx, addr)

	case "sse":
		fmt.Fprintf(os.Stderr, "standards-mcp %s listening on SSE at http://%s/sse\n", mcpVersion, addr)
		return server.RunSSE(ctx, addr)

	default:
		return fmt.Errorf("unsupported transport: %s (supported: stdio, http, sse)", transport)
	}
}
