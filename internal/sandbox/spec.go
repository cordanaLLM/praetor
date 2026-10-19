// Package sandbox builds and runs the fail-closed Linux agent execution profile.
package sandbox

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
)

// NetworkMode selects namespace isolation. Only NetworkNone is supported.
type NetworkMode string

// NetworkNone isolates networking, including host loopback, via --unshare-all.
const NetworkNone NetworkMode = "none"

// Bind exposes a host path at an absolute sandbox path, read-only.
type Bind struct{ Source, Target string }

// Variable supplies an explicit environment value after --clearenv.
type Variable struct{ Name, Value string }

// Limits bounds the cgroup, process and temporary filesystem. Zero selects defaults.
type Limits struct {
	MemoryBytes, AddressSpaceBytes, FileBytes, TmpBytes uint64
	Tasks, CPUPercent, CPUSeconds, OpenFiles            uint64
}

// DefaultLimits preserves the repair verifier's resource profile, with swap disabled.
func DefaultLimits() Limits {
	return Limits{MemoryBytes: 4 << 30, AddressSpaceBytes: 4 << 30, FileBytes: 256 << 20,
		TmpBytes: 1 << 30, Tasks: 128, CPUPercent: 200, CPUSeconds: 120, OpenFiles: 256}
}

// Spec mounts Workspace at /workspace. ReadOnly defaults to false. Environment is
// explicit; PassEnvironment copies only named variables from the builder's input.
// Commands run at /workspace with no inherited environment or network access.
type Spec struct {
	Workspace       string
	ReadOnly        bool
	ReadOnlyBinds   []Bind
	Environment     []Variable
	PassEnvironment []string
	Network         NetworkMode
	Limits          Limits
	Command         []string
}

// Binaries names resolved absolute executables outside the namespace.
type Binaries struct{ SystemdRun, Prlimit, Bwrap string }

// Invocation is an argv ready for the audited command runner.
type Invocation struct {
	Path        string
	Args        []string
	Environment []string
}

// Build is pure: no filesystem access, environment reads or execution. A zero
// Limits selects DefaultLimits; a partial or zero-valued custom limit is refused.
func Build(spec Spec, binaries Binaries, environ []string) (Invocation, error) {
	if err := validateSpec(spec, binaries); err != nil {
		return Invocation{}, err
	}
	limits := spec.Limits
	if limits == (Limits{}) {
		limits = DefaultLimits()
	}
	args, err := scopeArguments(limits, binaries)
	if err != nil {
		return Invocation{}, err
	}
	args = append(args, filesystemArguments(spec, limits)...)
	variables, err := environmentVariables(spec, environ)
	if err != nil {
		return Invocation{}, err
	}
	for i := 0; i < len(variables); i++ {
		args = append(args, "--setenv", variables[i].Name, variables[i].Value)
	}
	args = append(args, "--")
	return Invocation{Path: binaries.SystemdRun, Args: append(args, spec.Command...)}, nil
}

func scopeArguments(l Limits, b Binaries) ([]string, error) {
	values := []uint64{l.MemoryBytes, l.AddressSpaceBytes, l.FileBytes, l.TmpBytes, l.Tasks, l.CPUPercent, l.CPUSeconds, l.OpenFiles}
	for i := 0; i < len(values); i++ {
		if values[i] == 0 {
			return nil, errors.New("sandbox resource limits must all be positive")
		}
	}
	return []string{"--user", "--scope", "--quiet", "--collect", "--expand-environment=no",
		"--property=MemoryMax=" + memorySize(l.MemoryBytes), "--property=MemorySwapMax=0",
		"--property=TasksMax=" + decimal(l.Tasks), "--property=CPUQuota=" + decimal(l.CPUPercent) + "%",
		b.Prlimit, "--as=" + decimal(l.AddressSpaceBytes), "--cpu=" + decimal(l.CPUSeconds),
		"--fsize=" + decimal(l.FileBytes), "--nofile=" + decimal(l.OpenFiles), "--", b.Bwrap}, nil
}

func decimal(value uint64) string { return strconv.FormatUint(value, 10) }

func memorySize(value uint64) string {
	if value%(1<<30) == 0 {
		return decimal(value/(1<<30)) + "G"
	}
	return decimal(value)
}

func filesystemArguments(spec Spec, limits Limits) []string {
	mode := "--bind"
	if spec.ReadOnly {
		mode = "--ro-bind"
	}
	args := []string{"--unshare-all", "--die-with-parent", "--new-session", "--clearenv",
		"--ro-bind", "/usr", "/usr", "--symlink", "usr/bin", "/bin", "--symlink", "usr/lib", "/lib",
		"--symlink", "usr/lib", "/lib64", "--proc", "/proc", "--dev", "/dev",
		"--size", decimal(limits.TmpBytes), "--tmpfs", "/tmp", "--dir", "/home", mode, spec.Workspace, "/workspace"}
	for i := 0; i < len(spec.ReadOnlyBinds); i++ {
		bind := spec.ReadOnlyBinds[i]
		args = append(args, "--ro-bind", bind.Source, bind.Target)
	}
	return append(args, "--chdir", "/workspace")
}

func validateSpec(s Spec, b Binaries) error {
	if len(s.Command) == 0 || s.Command[0] == "" || len(s.Command) > 256 {
		return errors.New("sandbox requires a command of 1..256 arguments")
	}
	if s.Network != "" && s.Network != NetworkNone {
		return fmt.Errorf("unsupported sandbox network mode %q; only none is available", s.Network)
	}
	for _, value := range []string{s.Workspace, b.SystemdRun, b.Prlimit, b.Bwrap} {
		if !absolutePath(value) {
			return fmt.Errorf("sandbox requires a clean absolute path: %q", value)
		}
	}
	return validateBinds(s.ReadOnlyBinds)
}

func absolutePath(value string) bool {
	return path.IsAbs(value) && path.Clean(value) == value && !strings.ContainsRune(value, 0)
}

func validateBinds(binds []Bind) error {
	if len(binds) > 64 {
		return errors.New("sandbox allows at most 64 read-only binds")
	}
	for i := 0; i < len(binds); i++ {
		if !absolutePath(binds[i].Source) || !absolutePath(binds[i].Target) {
			return errors.New("sandbox bind paths must be clean and absolute")
		}
		if err := validateBindTarget(binds[i].Target); err != nil {
			return err
		}
	}
	return nil
}

func validateBindTarget(target string) error {
	if slices.Contains([]string{"/", "/usr", "/bin", "/lib", "/lib64", "/proc", "/dev", "/tmp", "/home", "/workspace"}, target) {
		return fmt.Errorf("sandbox bind target overlaps reserved mount: %s", target)
	}
	// Toolchains may live below /usr, /home or /tmp. Read-only submounts there
	// preserve the repair profile without replacing the namespace's special mounts.
	for _, reserved := range []string{"/bin", "/lib", "/lib64", "/proc", "/dev", "/workspace"} {
		if strings.HasPrefix(target, reserved+"/") {
			return fmt.Errorf("sandbox bind target overlaps reserved mount: %s", target)
		}
	}
	return nil
}
