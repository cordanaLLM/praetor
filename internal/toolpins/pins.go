// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package toolpins

// The fallback Security Scan builds these versions in a throwaway module. tools/go/go.mod is
// the version source; TestGoSecurityVersionsMatchToolsModule names the required update whenever
// Renovate moves one side without the other.
const (
	GovulnVersion   = "v1.9.0"
	GosecVersion    = "v2.30.0"
	GitleaksVersion = "v8.30.1"
	XToolsVersion   = "v0.52.0"
	// XToolsFloor is the oldest golang.org/x/tools whose export-data reader decodes what
	// go1.27.2 writes (pkgbits V5, uirVersion in cmd/compile/internal/noder/unified.go). gosec
	// v2.29.0 requires only v0.49.0, so a gosec built without this floor fails to load every
	// package, reports 0 issues and exits 1 (plurano/pertica main, 2026-10-16).
	XToolsFloor = "v0.50.0"
)
