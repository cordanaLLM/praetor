// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build windows

package container

import (
	"errors"
)

// errSignalUnsupported names what Windows cannot do, so a caller can skip with a reason
// rather than fail or -- worse -- appear to have tested something it never ran.
var errSignalUnsupported = errors.New(
	"raising SIGTERM at the current process is not supported on Windows; " +
		"the drain path is covered on the POSIX legs of the platform matrix")

// raiseTermination reports that this platform cannot deliver SIGTERM to itself.
//
// Windows has no kill(2). A process can receive a CTRL_BREAK style console event, but a
// Go test binary cannot raise one at itself without a console group it does not own, so
// there is no honest substitute here. Returning the reason is the honest answer.
func raiseTermination() error {
	return errSignalUnsupported
}
