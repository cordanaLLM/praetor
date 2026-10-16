// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor

// priorGoSecurityDigests records every earlier text of go/security-go.yml.tmpl. A plain flavor
// apply or adoption refreshes an unedited copy and keeps an edited workflow for the adopter.
var priorGoSecurityDigests = map[string]string{
	"7e09c97321a7a0c19f66d7df2a16e34d12317aa9d69e35d07acac5d243feb077": "the Go Security Scan fallback rendering before its concurrency group",
	"4dec1ae4286c4d46422710fc713d1c5934e502baaee6c111af262e6feae8844c": "the Go Security Scan tools-module rendering before its concurrency group",
	"847689783c9db2920c9c4fb2454be1f39b2ff81948c9a287994ea5067e6736a1": "the Go Security Scan that ran on push and schedule and installed both scanners at latest (#1371)",
}
