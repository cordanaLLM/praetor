// Package main is the docsref fixture CLI: a dispatcher, a nested subcommand, flags
// registered through two styles and one compared by hand, a git argument literal that is not
// a flag, and a leaf whose operands live in another package.
package main

import (
	"errors"
	"flag"

	"example.com/fixture/internal/events"
)

func main() {}

func runState(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: state <sync|task>")
	}
	switch args[0] {
	case "sync":
		fs := flag.NewFlagSet("state sync", flag.ContinueOnError)
		fs.Bool("verify", false, "Verify without writing")
		fs.String("log", "", "Message recorded with the sync")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return gitStatus([]string{"status", "--porcelain"})
	case "task":
		return runStateTask(args[1:])
	}
	return errors.New("unknown state subcommand")
}

func runStateTask(args []string) error {
	if len(args) > 0 && (args[0] == "add" || args[0] == "list") {
		return nil
	}
	return errors.New("usage: state task add|list")
}

// gitStatus stands in for a git call: "--porcelain" is git's flag, not the CLI's.
func gitStatus(argv []string) error {
	if len(argv) == 0 {
		return errors.New("git needs arguments")
	}
	return nil
}

func runHook(args []string) error {
	var registry events.Registry
	if len(args) == 2 && events.Known(args[0], args[1]) && registry.Lookup(args[1]) {
		return nil
	}
	return errors.New("usage: hook <client> <event>")
}

func runAudit(args []string) error {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	var strict bool
	fs.BoolVar(&strict, "strict", false, "Fail on any finding")
	fs.String("config", ".standards.yaml", "Manifest path")
	for _, arg := range args {
		if arg == "--json" {
			return nil
		}
	}
	return fs.Parse(args)
}

func runHelp(_ []string) error { return nil }
