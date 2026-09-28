//go:build windows

package util

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// prepareBatchFile makes cmd start a batch file through an explicit cmd.exe command line.
//
// CreateProcess runs a .bat or .cmd file through cmd.exe, which parses the command line with
// its own rules, not the CommandLineToArgvW rules os/exec quotes for. When cmd resolves to a
// batch file, cmd runs %SystemRoot%\System32\cmd.exe instead, with the whole command line
// batchCommandLine builds for the batch file and cmd's arguments in SysProcAttr.CmdLine, as
// the os/exec documentation advises for batch files. Every caller of the bounded runner gets
// this, so none escapes arguments for cmd.exe itself (HISS-19). A command that does not
// resolve, or resolves to anything else, is left unchanged, and Start reports what it did.
func prepareBatchFile(cmd *exec.Cmd) error {
	// A name os/exec could not resolve keeps its lookup error in cmd.Err, which Start reports.
	script, ok := "", false
	if cmd.Err == nil {
		script, ok = resolvedBatchFile(cmd.Dir, cmd.Path)
	}
	if !ok {
		return nil
	}
	line, err := batchCommandLine(script, cmd.Args[1:])
	if err != nil {
		return fmt.Errorf("run batch file %s: %w", script, err)
	}
	processor, err := commandProcessor()
	if err != nil {
		return fmt.Errorf("run batch file %s: %w", script, err)
	}
	cmd.Path = processor
	cmd.Args = []string{processor}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CmdLine = line
	return nil
}

// resolvedBatchFile resolves path, relative to dir as os/exec evaluates it, and returns the
// absolute path when it is a batch file. exec.Command has already resolved a bare name through
// PATH; a path with a separator is resolved here with the same PATHEXT search Start applies.
func resolvedBatchFile(dir, path string) (string, bool) {
	candidate := path
	if !filepath.IsAbs(candidate) && dir != "" {
		candidate = filepath.Join(dir, candidate)
	}
	resolved, err := exec.LookPath(candidate)
	if err != nil || !isBatchFile(resolved) {
		return "", false
	}
	absolute, err := filepath.Abs(resolved)
	return absolute, err == nil
}

// commandProcessor returns the cmd.exe in the Windows system directory. It is located from
// SystemRoot rather than through PATH or ComSpec, which a directory or value earlier in the
// environment could redirect to another program.
func commandProcessor() (string, error) {
	root := os.Getenv("SystemRoot")
	if root == "" || !filepath.IsAbs(root) {
		return "", errors.New("SystemRoot is unset or not absolute, so cmd.exe cannot be located")
	}
	return filepath.Join(root, "System32", "cmd.exe"), nil
}
