// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"flag"
	"fmt"
)

// maxCLIArgs is the scalar upper bound (HISS-02) on the number of argv tokens a single
// subcommand accepts. It also bounds every parse round in parseInterspersed, because each
// round consumes at least one token.
const maxCLIArgs = 256

// parseInterspersed parses args with fs while allowing flags and positional arguments to
// appear in any order, and returns the positional arguments in the order given.
//
// The standard library's flag.FlagSet stops parsing at the first non-flag argument, so
// documented forms such as `flavor audit ./svc --flavor=go-service` silently drop every
// flag written after the positional. This helper applies the stdlib idiom for interspersed
// arguments: parse, peel off one positional, parse the remainder, repeat. Consequently a
// misspelled flag anywhere in argv is reported by fs.Parse instead of being accepted as a
// positional value, and the space-separated `--flag value` form keeps its value.
//
// A literal "--" terminates flag parsing: every token after it is positional.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	if fs == nil {
		return nil, fmt.Errorf("nil flag set")
	}
	if len(args) > maxCLIArgs {
		return nil, fmt.Errorf("too many arguments: %d exceeds the %d-argument limit", len(args), maxCLIArgs)
	}

	positional := make([]string, 0, len(args))
	rest := args
	for round := 0; round < maxCLIArgs; round++ {
		if len(rest) == 0 {
			return positional, nil
		}
		if rest[0] == "--" {
			return append(positional, rest[1:]...), nil
		}
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		rest = fs.Args()
		if len(rest) == 0 {
			return positional, nil
		}
		positional = append(positional, rest[0])
		rest = rest[1:]
	}
	return nil, fmt.Errorf("argument parsing exceeded %d rounds", maxCLIArgs)
}

// positionalAt returns the positional argument at idx, or fallback when it is absent or
// empty. It keeps the "[dir]"-style optional positionals of the CLI in one place.
func positionalAt(positional []string, idx int, fallback string) string {
	if idx < 0 || idx >= len(positional) || positional[idx] == "" {
		return fallback
	}
	return positional[idx]
}
