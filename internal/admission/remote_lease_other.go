// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build !unix

package admission

import "fmt"

// AdmitExecutorLease refuses on non-Unix platforms because remote executor leases require Unix advisory file locks.
func AdmitExecutorLease(name string) (int, func() error, error) {
	return 0, nil, fmt.Errorf("executor %s: remote executor leases require Unix advisory file locks", name)
}
