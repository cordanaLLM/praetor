// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

var userConfigEnvKeys = []string{"HOME", "XDG_CONFIG_HOME", "AppData", "APPDATA", IsolatedRootEnv}

// IsolatedRootEnv names the isolated root for the processes a test starts, so a helper re-run of
// the test binary reuses it (IsolateUserConfig).
const IsolatedRootEnv = "PRAETOR_TEST_CONFIG_ROOT"

// maxCleanupEntries bounds the walk that makes the temporary tree writable before removal (HISS-02).
const maxCleanupEntries = 1 << 20

// goCaches keeps the Go toolchain's caches where they were while HOME points at the temporary
// directory. Without it a `go` command a test runs put a fresh module cache under
// <tmp>/go/pkg/mod (about 540 MB, read-only by design), which os.RemoveAll cannot remove: 937
// praetor-test-config-* directories filled /tmp on 2026-10-19.
func goCaches() map[string]string {
	caches := map[string]string{}
	home, homeErr := os.UserHomeDir()
	gopath := os.Getenv("GOPATH")
	if gopath == "" && homeErr == nil {
		gopath = filepath.Join(home, "go")
	}
	if gopath != "" {
		caches["GOPATH"] = gopath
		caches["GOMODCACHE"] = filepath.Join(filepath.SplitList(gopath)[0], "pkg", "mod")
	}
	if mod := os.Getenv("GOMODCACHE"); mod != "" {
		caches["GOMODCACHE"] = mod
	}
	if cache := os.Getenv("GOCACHE"); cache != "" {
		caches["GOCACHE"] = cache
	} else if dir, err := os.UserCacheDir(); err == nil {
		caches["GOCACHE"] = filepath.Join(dir, "go-build")
	}
	return caches
}

// removeTree makes every directory under root writable, then removes root: a read-only directory
// (a Go module cache, a packed Git object store) left behind by a test never blocks the removal.
// The walk runs inside an os.Root, so a symlink below root never leads the chmod outside it.
func removeTree(root string) error {
	scoped, err := os.OpenRoot(root)
	if err != nil {
		return errors.Join(fmt.Errorf("open %s: %w", root, err), os.RemoveAll(root))
	}
	seen := 0
	walkErr := fs.WalkDir(scoped.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk %s: %w", path, err)
		}
		seen++
		if seen > maxCleanupEntries {
			return fmt.Errorf("more than %d entries under %s", maxCleanupEntries, root)
		}
		if !d.IsDir() {
			return nil
		}
		// #nosec G302 -- a directory needs its owner's execute bit to be listed and emptied; 0700 grants nothing to group or other.
		if chmodErr := scoped.Chmod(path, 0o700); chmodErr != nil {
			return fmt.Errorf("make %s writable: %w", path, chmodErr)
		}
		return nil
	})
	return errors.Join(walkErr, scoped.Close(), os.RemoveAll(root))
}

// mkdirTempHook allows negative tests to simulate a directory creation failure.
var mkdirTempHook = os.MkdirTemp

type savedEnv struct {
	key   string
	value string
	set   bool
}

func restoreEnv(saved []savedEnv) error {
	var errs []error
	for _, s := range saved {
		var err error
		if s.set {
			err = os.Setenv(s.key, s.value)
		} else {
			err = os.Unsetenv(s.key)
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func applyEnv(tmp string) ([]savedEnv, error) {
	caches := goCaches()
	saved := make([]savedEnv, 0, len(userConfigEnvKeys)+len(caches))
	for _, k := range []string{"GOPATH", "GOMODCACHE", "GOCACHE"} {
		dir, ok := caches[k]
		if !ok {
			continue
		}
		val, set := os.LookupEnv(k)
		saved = append(saved, savedEnv{key: k, value: val, set: set})
		if err := os.Setenv(k, dir); err != nil {
			return saved, fmt.Errorf("setenv %s: %w", k, err)
		}
	}
	for _, k := range userConfigEnvKeys {
		val, ok := os.LookupEnv(k)
		saved = append(saved, savedEnv{key: k, value: val, set: ok})
		if err := os.Setenv(k, tmp); err != nil {
			return saved, fmt.Errorf("setenv %s: %w", k, err)
		}
	}
	return saved, nil
}

// IsolateUserConfig points the user configuration directory at a fresh temporary directory across
// Linux (XDG_CONFIG_HOME/HOME), macOS (HOME), and Windows (AppData/APPDATA) so tests never touch
// the developer's real configuration or claim keys. It returns the temporary directory root, a cleanup
// function that removes the directory (read-only parts included) and restores previous environment
// variable values, and any error encountered. The Go caches (GOPATH, GOMODCACHE, GOCACHE) stay where
// they were, so a `go` command the test runs never fills the temporary directory with a module cache.
func IsolateUserConfig() (string, func(), error) {
	// A test binary re-run as a helper process (the signal tests) inherits the isolated root:
	// it reuses it instead of creating a nested one that its killed process would leave behind
	// (64 empty praetor-test-config-* directories per run until 2026-10-19).
	if inherited := os.Getenv(IsolatedRootEnv); inherited != "" {
		// #nosec G703 -- the inherited root is a directory this package created in the parent test process; it is only stat'ed and reused, never written outside the test.
		if info, statErr := os.Stat(inherited); statErr == nil && info.IsDir() {
			return inherited, func() {}, nil
		}
	}
	tmp, err := mkdirTempHook("", "praetor-test-config-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp config: %w", err)
	}

	saved, err := applyEnv(tmp)
	if err != nil {
		if rmErr := removeTree(tmp); rmErr != nil {
			fmt.Fprintf(os.Stderr, "cleanup temp config: %v\n", rmErr)
		}
		if resErr := restoreEnv(saved); resErr != nil {
			fmt.Fprintf(os.Stderr, "restore config env: %v\n", resErr)
		}
		return "", nil, err
	}

	cleanup := func() {
		if rmErr := removeTree(tmp); rmErr != nil {
			fmt.Fprintf(os.Stderr, "cleanup temp config: %v\n", rmErr)
		}
		if resErr := restoreEnv(saved); resErr != nil {
			fmt.Fprintf(os.Stderr, "restore config env: %v\n", resErr)
		}
	}

	return tmp, cleanup, nil
}
