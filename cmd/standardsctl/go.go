// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/govuln"
	"github.com/cordanaLLM/praetor/internal/sandbox"
	"github.com/cordanaLLM/praetor/internal/util"
	apiassets "github.com/cordanaLLM/praetor/tools/apicompat"
)

// goTestAggregateTimeout bounds the local Go process, including builds, downloads
// and all test binaries. Twice the gate's stage ceiling leaves aggregate headroom
// even when PRAETOR_TEST_STAGE_TIMEOUT raises the stage to its maximum.
const goTestAggregateTimeout = 2 * gating.MaxTestStageTimeout

// runGo dispatches the standardsctl go subcommands for vet, test, or isolated test binary execution.
func runGo(args []string) error {
	if len(args) > 0 && args[0] == "test" {
		return runGoTest(args[1:])
	}
	if len(args) > 0 && args[0] == "test-exec" {
		ctx, cancel := commandContext(gating.MaxTestStageTimeout)
		defer cancel()
		return agentSandboxResult((sandbox.Runner{}).RunLoopback(ctx, args[1:], os.Stdin, os.Stdout, os.Stderr))
	}
	if len(args) != 1 || args[0] != "vet" {
		return errors.New("usage: praetorctl go vet | test [go test arguments] | test-exec BINARY [arguments]")
	}
	return runGoVet()
}

// runGoTest is the local Makefile seam: the Go tool builds and downloads outside
// the namespace and invokes this same CLI for each compiled test binary.
func runGoTest(args []string) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("go test loopback namespace wrapper: %w", err)
	}
	execFlag, note, err := gating.GoTestExec(runtime.GOOS, executable)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(os.Stderr, note); err != nil {
		return err
	}
	argv, err := goTestArgs(args, execFlag)
	if err != nil {
		return err
	}
	ctx, cancel := commandContext(goTestAggregateTimeout)
	defer cancel()
	return childExitStatus(util.RunCommandPassthrough(ctx, "", "go", os.Stdin, os.Stdout, os.Stderr, argv...))
}

// goTestArgs constructs the command-line argument slice for go test, inserting the gate -exec wrapper.
func goTestArgs(args []string, execFlag string) ([]string, error) {
	for i := 0; i < len(args); i++ {
		if goExecFlag(args[i]) {
			return nil, errors.New("go test loopback isolation: -exec is owned by the gate wrapper")
		}
	}
	directoryArgs, args := goTestDirectoryArgs(args)
	argv := append([]string{}, directoryArgs...)
	argv = append(argv, "test")
	if execFlag != "" {
		argv = append(argv, "-exec", execFlag)
	}
	argv = append(argv, args...)
	return argv, nil
}

// goTestDirectoryArgs splits a complete leading -C flag from the remaining test arguments.
func goTestDirectoryArgs(args []string) (directoryArgs, testArgs []string) {
	if len(args) == 0 {
		return nil, args
	}
	if len(args) >= 2 && (args[0] == "-C" || args[0] == "--C") {
		return args[:2], args[2:]
	}
	if strings.HasPrefix(args[0], "-C=") || strings.HasPrefix(args[0], "--C=") {
		return args[:1], args[1:]
	}
	return nil, args
}

// goExecFlag reports whether an argument specifies the go test -exec flag.
func goExecFlag(argument string) bool {
	return argument == "-exec" || argument == "--exec" || strings.HasPrefix(argument, "-exec=") || strings.HasPrefix(argument, "--exec=")
}

func runGoVet() error {
	modules, err := apiassets.TrackedModuleFiles(rootContext(), ".")
	if err != nil {
		return fmt.Errorf("go vet: discover tracked Go modules: %w", err)
	}
	if len(modules) == 0 {
		fmt.Println("go vet: no tracked Go modules, skipping")
		return nil
	}
	if len(modules) > apiassets.MaxTrackedModules {
		return fmt.Errorf("go vet: %w: found %d, maximum %d", apiassets.ErrTooManyModules,
			len(modules), apiassets.MaxTrackedModules)
	}
	failures := make([]string, 0, len(modules))
	for index := 0; index < len(modules) && index < apiassets.MaxTrackedModules; index++ {
		if failure := vetOneModule(modules[index]); failure != "" {
			failures = append(failures, failure)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("go vet failed:\n%s", strings.Join(failures, "\n"))
	}
	return nil
}

// vetOneModule runs go vet on a single tracked module and returns any failure description.
func vetOneModule(module string) string {
	start := time.Now()
	ctx, cancel := commandContext(gating.TestStageTimeout)
	defer cancel()
	dir := filepath.Join(".", filepath.Dir(filepath.FromSlash(module)))
	run := moduleCacheRetry(govuln.Runner(util.RunCommand), os.Stdout, "go vet", module)
	count, listErr := gating.ModulePackageCount(ctx, run, dir)
	if listErr == nil && count == 0 {
		fmt.Printf("go vet %s: %s, skipped\n", module, gating.NoPackagesNote)
		return ""
	}
	if listErr != nil {
		return fmt.Sprintf("%s: %v", module, listErr)
	}
	out, runErr := run(ctx, dir, "go", "vet", "./...")
	if runErr != nil {
		return fmt.Sprintf("%s: %s (%v): %s", module, runErr,
			time.Since(start).Round(time.Millisecond), strings.TrimSpace(out))
	}
	fmt.Printf("go vet %s: passed (%v)\n", module, time.Since(start).Round(time.Millisecond))
	return ""
}
