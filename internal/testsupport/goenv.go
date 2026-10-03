// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"os"
	"strings"
	"testing"
)

// OfflineGoEnv returns an environment for fixture go commands that cannot reach the network
// and that nothing on the host configures, with a fresh empty module cache for each call.
//
// Every inherited GO* variable is dropped, so a developer's GOFLAGS, GOPROXY, GOTOOLCHAIN or
// GOWORK does not decide a fixture's outcome. What remains is set here: GOPROXY=off and
// GOSUMDB=off refuse every module and checksum lookup, GOTOOLCHAIN=local refuses to fetch the
// toolchain a go.mod names (the command fails instead, naming the version it wanted),
// GOWORK=off ignores a workspace file above the fixture, GOFLAGS is empty, and GOENV=off keeps
// the settings `go env -w` stored on this machine out. That last one is what makes the empty
// GOFLAGS hold: the go command treats an empty variable as unset and falls back to that file.
func OfflineGoEnv(t testing.TB) []string {
	t.Helper()
	moduleCache := t.TempDir()
	inherited := os.Environ()
	env := make([]string, 0, len(inherited)+7)
	for _, entry := range inherited {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(key), "GO") {
			env = append(env, entry)
		}
	}
	return append(env,
		"GOENV=off",
		"GOFLAGS=",
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOTOOLCHAIN=local",
		"GOWORK=off",
		"GOMODCACHE="+moduleCache,
	)
}
