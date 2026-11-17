// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build unix

package admission

import (
	"os/signal"
	"sync"
	"syscall"
)

var ignoreTTYSignals sync.Once

// ignoreBackgroundTTYSignals keeps a step started in its own process group from stopping on
// SIGTTIN or SIGTTOU when a controlling terminal is attached: SIG_IGN carries across fork and
// execve, so terminal attribute setup (libedit readline in pytest) proceeds instead of stopping
// the child, and any direct read from /dev/tty fails at once with EIO.
func ignoreBackgroundTTYSignals() {
	ignoreTTYSignals.Do(func() {
		signal.Ignore(syscall.SIGTTIN, syscall.SIGTTOU)
	})
}
