// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build unix

package adopt

import "syscall"

func currentProcessGroup() int {
	return syscall.Getpgrp()
}
