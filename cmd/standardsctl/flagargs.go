// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"flag"
	"fmt"
	"strings"
)

// maxCLIArgs is the scalar upper bound (HISS-02) on the number of argv tokens a single
// subcommand accepts. It also bounds every parse round in parseInterspersed, because each
// round consumes at least one token.
const maxCLIArgs = 256

// parseInterspersed parses args with fs while allowing flags and positional arguments to
// appear in any order, and returns the positional arguments in the order given. It is the
// one argv parser every subcommand uses; none calls fs.Parse on raw argv.
//
// The standard library's flag.FlagSet stops parsing at the first non-flag argument, so
// documented forms such as `flavor audit ./svc --flavor=go-service` silently drop every
// flag written after the positional. This helper applies the stdlib idiom for interspersed
// arguments: parse, peel off one positional, parse the remainder, repeat. Consequently a
// misspelled flag anywhere in argv is reported by fs.Parse instead of being accepted as a
// positional value, and the space-separated `--flag value` form keeps its value.
//
// A literal "--" in flag position terminates flag parsing wherever it appears: every token
// after it is positional, including tokens that look like flags. A "--" that is the value
// of a preceding non-boolean flag (`--log --`) is that flag's value, as fs.Parse reads it.
//
// On success fs.Args(), fs.NArg() and fs.Arg() report the same positional arguments the
// function returns, so a caller may read either.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	if fs == nil {
		return nil, fmt.Errorf("nil flag set")
	}
	if len(args) > maxCLIArgs {
		return nil, fmt.Errorf("too many arguments: %d exceeds the %d-argument limit", len(args), maxCLIArgs)
	}
	positional, err := collectInterspersed(fs, args)
	if err != nil {
		return nil, err
	}
	// A leading "--" makes fs.Parse bind no flag and leave fs.Args() equal to the
	// positionals, whatever they look like.
	if err := fs.Parse(append([]string{"--"}, positional...)); err != nil {
		return nil, fmt.Errorf("record positional arguments: %w", err)
	}
	return positional, nil
}

// collectInterspersed runs the parse rounds of parseInterspersed. Each round parses one
// run of flags, then either stops at a "--" terminator, stops at the end of args, or peels
// off one positional. len(args) <= maxCLIArgs, so maxCLIArgs+1 rounds always suffice: one
// per positional plus the final round that finds nothing left.
func collectInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	boolFlags := boolFlagNames(fs)
	positional := make([]string, 0, len(args))
	rest := args
	for round := 0; round <= maxCLIArgs; round++ {
		end, terminated := flagRunEnd(rest, boolFlags)
		if err := fs.Parse(rest[:end]); err != nil {
			return nil, err
		}
		if terminated {
			return append(positional, rest[end+1:]...), nil
		}
		if end >= len(rest) {
			return positional, nil
		}
		positional = append(positional, rest[end])
		rest = rest[end+1:]
	}
	return nil, fmt.Errorf("argument parsing exceeded %d rounds", maxCLIArgs+1)
}

// flagRunEnd returns how many leading tokens of args fs.Parse reads as flags and flag
// values, and whether a "--" terminator ends that run (the terminator itself is not
// counted). It follows the flag package's own rules: "-" or a token without a leading
// dash is a positional; "--" terminates flags; a flag written with "=" or naming a boolean
// flag stands alone; any other flag takes the next token as its value, whatever it looks
// like. An unknown flag is counted like a value flag; fs.Parse rejects it either way.
func flagRunEnd(args []string, boolFlags map[string]bool) (int, bool) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return i, true
		}
		if len(arg) < 2 || arg[0] != '-' {
			return i, false
		}
		name := strings.TrimLeft(arg, "-")
		if !strings.Contains(name, "=") && !boolFlags[name] {
			i++
		}
	}
	return len(args), false
}

// boolFlagNames returns the names of every flag in fs whose value needs no separate
// argument, read from the FlagSet itself so a new boolean flag can never be mistaken for
// one that consumes the next token.
func boolFlagNames(fs *flag.FlagSet) map[string]bool {
	names := make(map[string]bool)
	fs.VisitAll(func(f *flag.Flag) {
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			names[f.Name] = true
		}
	})
	return names
}

// positionalAt returns the positional argument at idx, or fallback when it is absent or
// empty. It keeps the "[dir]"-style optional positionals of the CLI in one place.
func positionalAt(positional []string, idx int, fallback string) string {
	if idx < 0 || idx >= len(positional) || positional[idx] == "" {
		return fallback
	}
	return positional[idx]
}
