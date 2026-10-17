// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build !linux

package repairrun

import "errors"

// verificationEnvironment has no runtime directory to name off Linux: os.Getuid is -1 here and
// there is no /run/user (#132).
func verificationEnvironment() ([]string, error) {
	return nil, errors.New("repair verification requires Linux user runtime directories")
}
