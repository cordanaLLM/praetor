// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"errors"
	"fmt"
	"os"
)

var userConfigEnvKeys = []string{"HOME", "XDG_CONFIG_HOME", "AppData", "APPDATA"}

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
	saved := make([]savedEnv, 0, len(userConfigEnvKeys))
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
// function that removes the directory and restores previous environment variable values, and any error
// encountered.
func IsolateUserConfig() (string, func(), error) {
	tmp, err := mkdirTempHook("", "praetor-test-config-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp config: %w", err)
	}

	saved, err := applyEnv(tmp)
	if err != nil {
		if rmErr := os.RemoveAll(tmp); rmErr != nil {
			fmt.Fprintf(os.Stderr, "cleanup temp config: %v\n", rmErr)
		}
		if resErr := restoreEnv(saved[:len(saved)-1]); resErr != nil {
			fmt.Fprintf(os.Stderr, "restore config env: %v\n", resErr)
		}
		return "", nil, err
	}

	cleanup := func() {
		if rmErr := os.RemoveAll(tmp); rmErr != nil {
			fmt.Fprintf(os.Stderr, "cleanup temp config: %v\n", rmErr)
		}
		if resErr := restoreEnv(saved); resErr != nil {
			fmt.Fprintf(os.Stderr, "restore config env: %v\n", resErr)
		}
	}

	return tmp, cleanup, nil
}
