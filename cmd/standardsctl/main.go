package main

import (
	"fmt"
	"os"
)

const version = "v1.0.0"

func printUsage() {
	fmt.Println("standardsctl - Enterprise Fleet Governance & Autonomous Agent CLI (" + version + ")")
	fmt.Println("\nUsage:")
	fmt.Println("  standardsctl <command> [arguments]")
	fmt.Println("\nAvailable Commands:")
	fmt.Println("  compile-context    Transpile canonical AGENTS.md to vendor-native formats (< 300 LOC)")
	fmt.Println("  audit              Audit repository against declared HISS-16 invariants and lockfile")
	fmt.Println("  baseline           Inspect or record technical debt infractions")
	fmt.Println("  version            Print CLI version information")
	fmt.Println("\nRun 'standardsctl <command> -h' for more information on a command.")
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "compile-context":
		err = runCompileContext(args)
	case "audit":
		err = runAudit(args)
	case "version":
		fmt.Printf("standardsctl version %s\n", version)
	case "-h", "--help", "help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
