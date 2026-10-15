// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build !linux

package repairrun

import (
	"strings"
	"testing"
)

// Off Linux, os.Getuid is -1 and there is no /run/user, so the verification environment says
// the feature is unsupported instead of naming /run/user/-1 (#132).
func TestVerificationEnvironment_Negative_UnsupportedOffLinux(t *testing.T) {
	env, err := verificationEnvironment()
	if err == nil || !strings.Contains(err.Error(), "requires Linux") {
		t.Fatalf("verification environment off Linux = %q, %v; want an unsupported error", env, err)
	}
}
