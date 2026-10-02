// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// emptyFileProxy returns a module proxy that is an empty directory on this machine. A go
// command that consults it finds nothing and opens no socket, so the hostile environment
// below never reaches the network either.
func emptyFileProxy(t *testing.T) string {
	t.Helper()
	proxy := url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(t.TempDir()), "/")}
	return proxy.String()
}

// hostileToolchain is a toolchain no machine has installed, so selecting it means fetching it.
const hostileToolchain = "go1.999.0"

func requireGo(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go is not on PATH: %v", err)
	}
}

// hostileGoEnvironment makes the process environment carry what a developer's shell can: a
// toolchain the go command would have to download, a proxy to download it from, a build flag
// that changes module resolution, a workspace file that does not exist and a stray variable
// that only shares the prefix.
func hostileGoEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("GOTOOLCHAIN", hostileToolchain)
	t.Setenv("GOPROXY", emptyFileProxy(t))
	t.Setenv("GOFLAGS", "-mod=vendor")
	t.Setenv("GOWORK", filepath.Join(t.TempDir(), "no-such-go.work"))
	t.Setenv("GONOSUMDB", "example.invalid")
}

// dependencyFreeModule writes a go.mod that names no go version and requires nothing, so no
// toolchain is selected and nothing is left to download.
func dependencyFreeModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.invalid/fixture\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	return dir
}

// TestOfflineGoEnv_Positive_RunsUnderAHostileEnvironment is the property every caller relies
// on: the fixture's go commands run on the toolchain already installed, whatever the process
// environment asks for.
func TestOfflineGoEnv_Positive_RunsUnderAHostileEnvironment(t *testing.T) {
	requireGo(t)
	hostileGoEnvironment(t)
	module := dependencyFreeModule(t)
	env := OfflineGoEnv(t)
	for _, args := range [][]string{{"mod", "verify"}, {"mod", "download"}} {
		if out, err := runTool(t, "go", module, env, args...); err != nil {
			t.Fatalf("go %v under the offline environment: %v: %s", args, err, out)
		}
	}
	// Each setting is read back from the go command itself, which is what decides.
	want := map[string]string{"GOPROXY": "off", "GOSUMDB": "off", "GOTOOLCHAIN": "local", "GOWORK": "off", "GOFLAGS": ""}
	for key, value := range want {
		if got, err := runTool(t, "go", module, env, "env", key); err != nil || got != value {
			t.Errorf("go env %s = %q (err %v), want %q", key, got, err, value)
		}
	}
}

// TestOfflineGoEnv_Negative_NothingIsFetched proves the environment refuses the network rather
// than happening not to need it: a module lookup and a toolchain switch both fail, each with
// the go command's own offline reason, and the inherited environment fails differently, which
// is what makes the positive case evidence.
func TestOfflineGoEnv_Negative_NothingIsFetched(t *testing.T) {
	requireGo(t)
	hostileGoEnvironment(t)
	module := dependencyFreeModule(t)
	env := OfflineGoEnv(t)

	out, err := runTool(t, "go", module, env, "mod", "download", "example.invalid/absent@v1.0.0")
	if err == nil || !strings.Contains(out, "GOPROXY=off") {
		t.Errorf("a module lookup must be refused by GOPROXY=off, got err %v: %s", err, out)
	}

	newer := t.TempDir()
	if err := os.WriteFile(filepath.Join(newer, "go.mod"), []byte("module example.invalid/newer\n\ngo 1.999\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	out, err = runTool(t, "go", newer, env, "mod", "verify")
	if err == nil || !strings.Contains(out, "GOTOOLCHAIN=local") {
		t.Errorf("a newer go directive must be refused by GOTOOLCHAIN=local, got err %v: %s", err, out)
	}

	out, err = runTool(t, "go", module, os.Environ(), "mod", "verify")
	if err == nil || !strings.Contains(out, hostileToolchain) || strings.Contains(out, "GOTOOLCHAIN=local") {
		t.Errorf("the inherited environment did not ask for %s; the positive case proves nothing: err %v: %s", hostileToolchain, err, out)
	}
}

// TestOfflineGoEnv_Boundary_EnvironmentContents pins the environment itself: no inherited GO*
// variable survives, every setting appears exactly once, ordinary variables are kept, and each
// call gets its own empty module cache.
func TestOfflineGoEnv_Boundary_EnvironmentContents(t *testing.T) {
	hostileGoEnvironment(t)
	t.Setenv("PRAETOR_TESTSUPPORT_KEEP", "kept")
	set := map[string]string{}
	for _, entry := range OfflineGoEnv(t) {
		key, value, _ := strings.Cut(entry, "=")
		if _, dup := set[key]; dup {
			t.Errorf("%s is set twice", key)
		}
		set[key] = value
	}
	if value, ok := set["GONOSUMDB"]; ok {
		t.Errorf("inherited GONOSUMDB=%q survived", value)
	}
	required := map[string]string{
		"GOENV": "off", "GOFLAGS": "", "GOPROXY": "off", "GOSUMDB": "off", "GOTOOLCHAIN": "local",
		"GOWORK": "off", "PRAETOR_TESTSUPPORT_KEEP": "kept",
	}
	for key, want := range required {
		if got, ok := set[key]; !ok || got != want {
			t.Errorf("%s = %q (present %v), want %q", key, got, ok, want)
		}
	}
	entries, err := os.ReadDir(set["GOMODCACHE"])
	if err != nil || len(entries) != 0 {
		t.Errorf("GOMODCACHE %q must be an existing empty directory: %v (%d entries)", set["GOMODCACHE"], err, len(entries))
	}
	for _, entry := range OfflineGoEnv(t) {
		if entry == "GOMODCACHE="+set["GOMODCACHE"] {
			t.Errorf("two calls share one module cache %q", set["GOMODCACHE"])
		}
	}
}
