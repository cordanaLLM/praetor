// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"regexp"
	"strings"
)

// shellQuoted is a single- or double-quoted string inside a shell command, whose pipes are text.
var shellQuoted = regexp.MustCompile(`'[^']*'|"(?:[^"\\]|\\.)*"`)

// ShellPipes reports whether command, one shell command line, holds a pipe: a | outside quoted
// strings that is not half of the || operator. A pipeline's status is its last command's, so a
// caller uses this to require pipefail before such a line (HISS-07's Ansible rule, Hadolint
// DL4006 for a Dockerfile RUN).
func ShellPipes(command string) bool {
	unquoted := strings.ReplaceAll(shellQuoted.ReplaceAllString(command, ""), "||", "")
	return strings.Contains(unquoted, "|")
}
