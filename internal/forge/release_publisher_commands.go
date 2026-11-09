// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"fmt"
)

// scriptPublishesRelease reads actual commands with the shared shell parser without execution.
// A script past maxRunScriptFields fields never reaches the loop: ShellFields refuses it with
// ErrShellReadLimit, and the error fails the audit, so the loop bound truncates nothing.
func scriptPublishesRelease(script string) (bool, error) {
	fields, err := scriptFields(script)
	if err != nil {
		return false, fmt.Errorf("read release publisher command: %w", err)
	}
	commands := scriptCommands(fields)
	for i := 0; i < len(commands) && i < maxRunScriptFields; i++ {
		cmd := parseShellCommand(commands[i])
		if cmd.program == "gh" && ghCommandPublishesRelease(cmd.args) {
			return true, nil
		}
	}
	return false, nil
}

// ghCommandPublishesRelease recognizes release creation, asset uploads and draft publication.
func ghCommandPublishesRelease(args []string) bool {
	if len(args) < 2 || args[0] != "release" {
		return false
	}
	switch args[1] {
	case "create", "upload":
		return true
	case "edit":
		return flagValue(args[2:], "--draft") == "false"
	}
	return false
}
