// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build linux

package repairrun

import (
	"os"
	"strconv"
)

// verificationEnvironment is the environment systemd-run starts the verification with: the
// user's runtime directory, which only Linux has (#132).
func verificationEnvironment() ([]string, error) {
	return []string{"PATH=/usr/bin:/bin", "XDG_RUNTIME_DIR=/run/user/" + strconv.Itoa(os.Getuid())}, nil
}
