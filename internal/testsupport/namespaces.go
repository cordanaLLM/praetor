// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import "strings"

// NamespaceProbeUnavailable recognizes only bubblewrap namespace failures with exit status 1.
// It mirrors internal/sandbox/network_integration_test.go's realNetworkBinaries permission
// checks, restricted to namespace-specific messages so unrelated setup failures stay fatal.
func NamespaceProbeUnavailable(exitCode int, stderr []byte) bool {
	if exitCode != 1 {
		return false
	}
	message := strings.TrimSpace(string(stderr))
	const denied = "bwrap: No permissions to create a new namespace, likely because the kernel does not allow non-privileged user namespaces"
	if message == denied || strings.HasPrefix(message, denied+".") {
		return true
	}
	switch message {
	case "bwrap: Creating new namespace failed: Operation not permitted",
		"bwrap: Creating new namespace failed: Permission denied",
		"bwrap: Creating new namespace failed, likely because the kernel does not support user namespaces.":
		return true
	}
	return false
}
