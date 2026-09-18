// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package clientsetup

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// hostRoot is the root of the host's own filesystem: "/" on POSIX, "C:\" on Windows.
//
// The fixtures here spelled executables as "/opt/praetor/bin/praetor-mcp". validateServer
// requires filepath.IsAbs, which is correct -- a server command is a real executable on the
// host -- and that literal is absolute on POSIX and not on Windows, where an absolute path
// needs a volume. Every registry was refused before the case under test ran.
//
// Built from the host's volume name, these helpers yield strings byte-identical to the old
// literals on POSIX, where the volume name is empty. The Linux expectations therefore cannot
// move; only the Windows values change, into paths that are actually absolute there.
var hostRoot = filepath.VolumeName(os.TempDir()) + string(filepath.Separator)

// hostAbsolute joins parts under hostRoot and cleans the result.
func hostAbsolute(parts ...string) string {
	return filepath.Join(append([]string{hostRoot}, parts...)...)
}

// hostAbsoluteUnclean joins parts under hostRoot without cleaning, for the negative cases
// that exist precisely to present an absolute path that is not clean. filepath.Join would
// clean ".." away and turn the case into a valid path.
func hostAbsoluteUnclean(parts ...string) string {
	return hostRoot + strings.Join(parts, string(filepath.Separator))
}

// quoted renders a path for embedding in a JSON or TOML fixture. strconv.Quote is what
// tomlString uses in production, and for the characters a path contains it is also valid
// JSON: a backslash becomes "\\" and nothing else is escaped.
func quoted(value string) string { return strconv.Quote(value) }

var (
	praetorMCP  = hostAbsolute("opt", "praetor", "bin", "praetor-mcp")
	optServer   = hostAbsolute("opt", "server")
	agentToken  = hostAbsolute("private", "agent-token")
	workProject = hostAbsolute("work", "project")
)

// containsSerialized reports whether value appears in serialized output, in either its raw
// form or the escaped form a JSON or TOML string gives it.
//
// The adapter output is TOML, JSON or YAML, and the commands are JSON-encoded. The first two
// escape a backslash as "\\", so a Windows path is present in the output without being
// present verbatim, and a plain substring search reported it lost. YAML plain scalars do not
// escape it, which is why both forms are accepted. On POSIX a path contains no backslash, the
// two forms are the same string, and the check is exactly the strings.Contains it replaces.
func containsSerialized(haystack, value string) bool {
	return strings.Contains(haystack, value) ||
		strings.Contains(haystack, strings.ReplaceAll(value, `\`, `\\`))
}
