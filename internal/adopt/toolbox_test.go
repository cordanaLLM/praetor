// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// gitForWindowsFstab is the mount table Git for Windows ships in etc/fstab.
const gitForWindowsFstab = "none / cygdrive binary,posix=0,noacl,user 0 0\nnone /tmp usertemp binary,posix=0,noacl 0 0\n"

// toolboxCache holds the toolboxes copiedToolbox made in this test binary, by name set.
var toolboxCache = struct {
	sync.Mutex
	dirs map[string]string
}{dirs: map[string]string{}}

// copiedToolbox is linkTools for Windows (#166). An MSYS tool from Git for Windows does not
// start from a symbolic link: it loads msys-2.0.dll from the directory it runs from, and takes
// the directory two levels above that DLL as its root, where /bin/sh and /tmp must resolve. So
// the tools are copied, as name.exe and with every msys-*.dll beside them, into
// <toolbox>/usr/bin, and <toolbox>/tmp exists. A shell stub's "#!/bin/sh" then starts the
// toolbox's own sh, and no other tool of the host is reachable through it.
//
// The copy is made once per name set and test binary, under stubCache.dir, and shared: every
// caller only puts it on PATH. The returned directory is <toolbox>/usr/bin.
func copiedToolbox(t *testing.T, names ...string) string {
	t.Helper()
	key := strings.Join(names, " ")
	toolboxCache.Lock()
	defer toolboxCache.Unlock()
	if dir, ok := toolboxCache.dirs[key]; ok {
		return dir
	}
	// Every tool is looked up before anything is copied, so a missing one skips the test and
	// leaves no half-built toolbox behind for the next.
	sources := make([]string, len(names))
	for i, name := range names {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Skipf("%s required", name)
		}
		sources[i] = scoopShimTarget(path)
	}
	root, err := os.MkdirTemp(stubCache.dir, "toolbox-")
	if err != nil {
		t.Fatalf("toolbox: %v", err)
	}
	bin := filepath.Join(root, "usr", "bin")
	for _, dir := range []string{bin, filepath.Join(root, "etc"), filepath.Join(root, "tmp")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("toolbox: %v", err)
		}
	}
	// Git for Windows mounts drives at / (/c/Users/...) and /tmp at the user's temp
	// directory; without its fstab the MSYS defaults (/cygdrive/c/...) would answer instead.
	if err := os.WriteFile(filepath.Join(root, "etc", "fstab"), []byte(gitForWindowsFstab), 0o600); err != nil {
		t.Fatalf("toolbox: %v", err)
	}
	runtimeDirs := map[string]bool{}
	for i, name := range names {
		if err := copyExecutable(sources[i], filepath.Join(bin, name+".exe")); err != nil {
			t.Fatalf("toolbox: copy %s: %v", name, err)
		}
		runtimeDirs[filepath.Dir(sources[i])] = true
	}
	for dir := range runtimeDirs {
		dlls, err := filepath.Glob(filepath.Join(dir, "msys-*.dll"))
		if err != nil {
			t.Fatalf("toolbox: %v", err)
		}
		for _, dll := range dlls {
			target := filepath.Join(bin, filepath.Base(dll))
			if fileExists(target) {
				continue
			}
			if err := copyExecutable(dll, target); err != nil {
				t.Fatalf("toolbox: copy %s: %v", filepath.Base(dll), err)
			}
		}
	}
	toolboxCache.dirs[key] = bin
	return bin
}

// scoopShimTarget returns the program a Scoop shim at path starts, read from the "path = "
// line of the <name>.shim file beside it, or path itself when there is none. A copied shim
// starts nothing without that file.
func scoopShimTarget(path string) string {
	// #nosec G304 -- the .shim file beside a program exec.LookPath found.
	data, err := os.ReadFile(strings.TrimSuffix(path, filepath.Ext(path)) + ".shim")
	if err != nil {
		return path
	}
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "path = "); ok {
			return strings.Trim(value, `"`)
		}
	}
	return path
}
