// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build windows

package adopt

import "os"

func currentProcessGroup() int {
	return os.Getpid()
}
