package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/cordanaLLM/praetor/internal/buildid"
)

// version is written with -X main.version by the release config (.goreleaser.yaml). It must
// stay an empty var: the linker cannot write a const, so a const here discarded every injected
// release and the daemon reported v1.0.0 from every build (#666). Without it the daemon
// reports what the build can prove through internal/buildid, the identity standardsctl and
// standards-mcp of the same build report too.
var version = ""

// maxArgs bounds the argument scan (HISS-02).
const maxArgs = 64

// serverVersion is the identity the daemon reports in initialize and to -version.
func serverVersion() string {
	return buildid.Running(version).String()
}

// versionRequested reports whether args ask for the version, as standards-mcp -version does.
// Every other argument is ignored, as it always was: editors start the daemon with none.
func versionRequested(args []string) bool {
	for i := 0; i < len(args) && i < maxArgs; i++ {
		if args[i] == "-version" || args[i] == "--version" {
			return true
		}
	}
	return false
}

// main wires the daemon to SIGINT/SIGTERM. The entry point owns the process lifetime, so
// the daemon's root context is built here (HISS-02): the signal cancels it, which
// Server.Run observes even while it idles on stdin, so a signalled daemon exits promptly
// with status 0. os.Exit runs only after run returned and the signal handler was released.
func main() {
	if versionRequested(os.Args[1:]) {
		fmt.Printf("standards-lsp %s\n", serverVersion())
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx)
	stop()
	os.Exit(code)
}

// run serves the language server protocol on stdio until ctx is cancelled or stdin ends.
func run(ctx context.Context) int {
	srv := NewServer(os.Stdin, os.Stdout, serverVersion())
	err := srv.Run(ctx)
	if err == nil || errors.Is(err, context.Canceled) {
		return 0
	}
	fmt.Fprintf(os.Stderr, "standards-lsp daemon error: %v\n", err)
	return 1
}
