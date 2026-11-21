// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package admission

import "sync"

// executorLeases count admitted steps currently running on each named executor.
type executorLeases struct {
	mu     sync.Mutex
	active map[string]int
}

var remoteLeases = executorLeases{active: make(map[string]int)}

// AdmitExecutorLease records one more concurrent step on name and returns the inclusive count.
func AdmitExecutorLease(name string) int {
	remoteLeases.mu.Lock()
	defer remoteLeases.mu.Unlock()
	remoteLeases.active[name]++
	return remoteLeases.active[name]
}

// ReleaseExecutorLease drops one step from name.
func ReleaseExecutorLease(name string) {
	remoteLeases.mu.Lock()
	defer remoteLeases.mu.Unlock()
	if remoteLeases.active[name] <= 1 {
		delete(remoteLeases.active, name)
		return
	}
	remoteLeases.active[name]--
}
