// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package admission

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Limits are the memory bounds of a step's scope, in systemd's notation (8G, 512M). MemoryMax
// alone does not contain a step: above it the kernel swaps the step out, so a step could still
// fill the host's swap, the pressure under which the out-of-memory daemon kills the editor's scope
// with every session in it. MemorySwapMax bounds that too; "0" keeps the step out of swap.
type Limits struct {
	MemoryHigh    string
	MemoryMax     string
	MemorySwapMax string
}

var (
	memorySize = regexp.MustCompile(`^[1-9][0-9]{0,5}[KMGT]$`)
	unsafeUnit = regexp.MustCompile(`[^A-Za-z0-9_-]`)
)

// Check refuses a bound systemd would read differently from the operator (a missing or unknown
// unit, a fraction, a sign), so a typo never runs a step unbounded.
func (l Limits) Check() error {
	if !memorySize.MatchString(l.MemoryHigh) || !memorySize.MatchString(l.MemoryMax) {
		return fmt.Errorf("memory limits %q and %q must each be a whole number with K, M, G or T", l.MemoryHigh, l.MemoryMax)
	}
	if l.MemorySwapMax != "0" && !memorySize.MatchString(l.MemorySwapMax) {
		return fmt.Errorf("swap limit %q must be 0 or a whole number with K, M, G or T", l.MemorySwapMax)
	}
	return nil
}

// UnitName is the transient scope's name: the label with anything outside [A-Za-z0-9_-]
// replaced, and the start time, so two steps with one label never share a unit.
func UnitName(label string, now time.Time) string {
	safe := unsafeUnit.ReplaceAllString(label, "-")
	if len(safe) > 48 {
		safe = safe[:48]
	}
	return fmt.Sprintf("admit-%s-%s", safe, now.Format("20060102-150405.000000"))
}

func unitName(label string, now time.Time) string {
	return UnitName(label, now)
}

// ScopeArgs is the systemd-run argv that runs argv in its own user scope under l. The scope is
// collected when the step ends, and --expand-environment=no keeps systemd from rewriting ${VAR}
// and $$ in the step's arguments (systemd 262).
func ScopeArgs(unit string, l Limits, argv []string) []string {
	args := []string{"--user", "--scope", "--quiet", "--collect", "--expand-environment=no", "--unit=" + unit,
		"--property=MemoryHigh=" + l.MemoryHigh, "--property=MemoryMax=" + l.MemoryMax, "--property=MemorySwapMax=" + l.MemorySwapMax, "--"}
	return append(args, argv...)
}

// scopeProbeTimeout bounds the systemd-run probe (HISS-02).
const scopeProbeTimeout = 15 * time.Second

// ScopesAvailable reports whether this host can start a user scope: Linux, systemd-run on PATH,
// and a user manager that runs `true` in a scope. The reason names what is missing.
func ScopesAvailable(ctx context.Context) (bool, string) {
	if runtime.GOOS != "linux" {
		return false, "no systemd on " + runtime.GOOS
	}
	path, err := exec.LookPath("systemd-run")
	if err != nil {
		return false, "systemd-run is not on PATH"
	}
	probe, cancel := context.WithTimeout(ctx, scopeProbeTimeout)
	defer cancel()
	if _, err := util.RunCommand(probe, "", path, "--user", "--scope", "--quiet", "--collect", "true"); err != nil {
		return false, "systemd-run --user cannot start a scope: " + err.Error()
	}
	return true, ""
}

// Step is one admitted command, its execution bound, and where its streams go.
type Step struct {
	Label   string
	Argv    []string
	Limits  Limits
	Timeout time.Duration
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
}

// Run runs the step in its own scope where the host offers scopes, and unscoped otherwise with
// a line saying why (no silent fallback). Timeout bounds the step after the scope probe; without
// Timeout, ctx's deadline (or the command runner's short default) bounds it. A step cut by its
// deadline prints and returns a reason naming the label, command and deadline. Any other failure
// of the step is returned unchanged, so errors.As finds its *exec.ExitError.
func Run(ctx context.Context, s Step) error {
	if len(s.Argv) == 0 {
		return fmt.Errorf("admission %s: no command to run", s.Label)
	}
	if err := s.Limits.Check(); err != nil {
		return err
	}
	inherited := contextRemaining(ctx)
	name, args := s.Argv[0], s.Argv[1:]
	if ok, why := ScopesAvailable(ctx); ok {
		name, args = "systemd-run", ScopeArgs(unitName(s.Label, time.Now()), s.Limits, s.Argv)
	} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return reportStepDeadline(s, inherited)
	} else if _, err := fmt.Fprintf(s.Stderr, "admission %s: runs unscoped, %s\n", s.Label, why); err != nil {
		return err
	}
	stepCtx, bound, cancel := withStepDeadline(ctx, s.Timeout)
	defer cancel()
	ignoreBackgroundTTYSignals()
	err := util.RunCommandPassthrough(stepCtx, "", name, s.Stdin, s.Stdout, s.Stderr, args...)
	if errors.Is(stepCtx.Err(), context.DeadlineExceeded) {
		return reportStepDeadline(s, bound)
	}
	return err
}

// contextRemaining is the time left before ctx's deadline, or the command runner's default when ctx has none.
func contextRemaining(ctx context.Context) time.Duration {
	if dl, ok := ctx.Deadline(); ok {
		return max(0, time.Until(dl).Round(time.Millisecond))
	}
	return util.DefaultCommandTimeout
}

// withStepDeadline installs an execution deadline and reports its effective bound after the scope check.
func withStepDeadline(ctx context.Context, timeout time.Duration) (context.Context, time.Duration, context.CancelFunc) {
	if timeout <= 0 {
		if _, ok := ctx.Deadline(); ok {
			return ctx, contextRemaining(ctx), func() {}
		}
		timeout = util.DefaultCommandTimeout
	}
	stepCtx, cancel := context.WithTimeout(ctx, timeout)
	return stepCtx, contextRemaining(stepCtx), cancel
}

// reportStepDeadline prints and returns the named reason for a step cut at its deadline: label, command and bound.
func reportStepDeadline(s Step, bound time.Duration) error {
	msg := fmt.Sprintf("admission %s: %s exceeded %s deadline, killed", s.Label, s.Argv[0], bound)
	if s.Stderr != nil {
		if _, writeErr := fmt.Fprintln(s.Stderr, msg); writeErr != nil {
			return errors.Join(fmt.Errorf("%s: %w", msg, context.DeadlineExceeded), writeErr)
		}
	}
	return fmt.Errorf("%s: %w", msg, context.DeadlineExceeded)
}

// DefaultStep is a step with the process's own output streams and non-interactive input.
// A character-device stdin (a controlling terminal or console) becomes nil (/dev/null), while
// a pipe or file passes through: Run starts the step in its own process group, where reading
// or configuring a controlling terminal triggers SIGTTIN or SIGTTOU instead of reading input.
func DefaultStep(label string, argv []string, l Limits) Step {
	return Step{Label: label, Argv: argv, Limits: l, Stdin: nonInteractiveStdin(os.Stdin), Stdout: os.Stdout, Stderr: os.Stderr}
}

// nonInteractiveStdin passes a pipe or file stdin through and replaces a terminal or console (a character device) with nil.
func nonInteractiveStdin(f *os.File) io.Reader {
	if f == nil {
		return nil
	}
	info, err := f.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice != 0 {
		return nil
	}
	return f
}
