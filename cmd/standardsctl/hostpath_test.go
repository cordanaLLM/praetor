// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"os"
	"path/filepath"
	"strconv"
)

// hostRoot is the root of the host's filesystem: "/" on POSIX, "C:\" on Windows.
//
// The client fixtures spelled executables and files as "/usr/bin/true" or
// "/private/not-read". The registry and connection validators require filepath.IsAbs,
// which is correct -- these name real files on the host -- and those literals are not
// absolute on Windows, which needs a volume. Built from the host's volume name, the
// values below are byte-identical to the old literals on POSIX and absolute on Windows.
// Mirrors internal/clientsetup/hostpath_test.go; test files do not export across packages.
var hostRoot = filepath.VolumeName(os.TempDir()) + string(filepath.Separator)

func hostAbsolute(parts ...string) string {
	return filepath.Join(append([]string{hostRoot}, parts...)...)
}

// quoted renders a path for embedding in a JSON fixture: a backslash becomes "\\".
func quoted(value string) string { return strconv.Quote(value) }

var (
	trueCommand   = hostAbsolute("usr", "bin", "true")
	agentBridge   = hostAbsolute("opt", "agent", "bridge")
	notReadToken  = hostAbsolute("private", "not-read")
	memoryProject = hostAbsolute("work", "project")
)
