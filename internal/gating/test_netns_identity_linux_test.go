// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"os"
	"strconv"
	"syscall"
	"testing"
)

// setGateIdentity hands the fixture tests of the real planted case the uid that owns / and the
// user namespace, as this process sees them. syscall.Stat_t's owner fields exist on Unix only, so
// this half lives in a Linux-only file; test_netns_identity_other_test.go skips elsewhere.
func setGateIdentity(t *testing.T) {
	t.Helper()
	info, err := os.Stat("/")
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	userns, nsErr := os.Readlink("/proc/self/ns/user")
	if !ok || nsErr != nil {
		t.Fatalf("read gate identity: stat_t=%v namespace error=%v", ok, nsErr)
	}
	t.Setenv("PRAETOR_FIXTURE_ROOT_UID", strconv.FormatUint(uint64(stat.Uid), 10))
	t.Setenv("PRAETOR_FIXTURE_USERNS", userns)
}
