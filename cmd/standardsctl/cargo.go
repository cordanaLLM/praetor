// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/util"
)

// cargoHookArgs are the cargo arguments of each `praetorctl cargo` target: the Rust pre-commit
// jobs of the lefthook.yml adoption writes. clippy is the gate's own clippy run, so a commit and
// a push lint alike.
var cargoHookArgs = map[string]func() []string{
	"fmt":    func() []string { return []string{"fmt", "--all", "--check"} },
	"clippy": gating.CargoClippyArgs,
}

// runCargo is `praetorctl cargo fmt|clippy`.
func runCargo(args []string) error {
	if len(args) != 1 || cargoHookArgs[args[0]] == nil {
		return errors.New("usage: praetorctl cargo fmt|clippy")
	}
	return runCargoHook(rootContext(), ".", args[0], util.RunCommand, os.Stdout)
}

// runCargoHook runs the cargo target tool once in every Cargo workspace or standalone crate git
// tracks below repoDir (gating.TrackedCargoRoots), as `praetorctl go vet` runs once per Go module
// (#1102). Discovery runs when the hook does, so a crate added later needs no re-adoption. A
// repository with none says so and passes; a failure in one workspace fails the command and names
// its manifest after every workspace ran.
func runCargoHook(ctx context.Context, repoDir, tool string, run func(context.Context, string, string, ...string) (string, error), out io.Writer) error {
	roots, err := gating.TrackedCargoRoots(ctx, repoDir)
	if err != nil {
		return fmt.Errorf("cargo %s: discover tracked Cargo workspaces: %w", tool, err)
	}
	if len(roots) == 0 {
		_, err := fmt.Fprintf(out, "cargo %s: no tracked Cargo workspaces, skipping\n", tool)
		return err
	}
	args := cargoHookArgs[tool]()
	failures := make([]string, 0, len(roots))
	for index := 0; index < len(roots) && index < gating.MaxCargoRoots; index++ {
		manifest := path.Join(roots[index].Dir, "Cargo.toml")
		start := time.Now()
		cargoCtx, cancel := context.WithTimeout(ctx, gating.TestStageTimeout)
		output, runErr := run(cargoCtx, filepath.Join(repoDir, filepath.FromSlash(roots[index].Dir)), "cargo", args...)
		cancel()
		elapsed := time.Since(start).Round(time.Millisecond)
		if runErr != nil {
			failures = append(failures, fmt.Sprintf("%s: %v (%v): %s", manifest, runErr, elapsed, strings.TrimSpace(output)))
			continue
		}
		if _, err := fmt.Fprintf(out, "cargo %s %s: passed (%v)\n", tool, manifest, elapsed); err != nil {
			return err
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("cargo %s failed:\n%s", tool, strings.Join(failures, "\n"))
	}
	return nil
}
