package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// resolveLockSourceRoot validates explicit, the value of flagName. Unset (empty) returns ("", nil);
// a blank value, a missing path and one that is not a directory are refused with flagName, the
// quoted path and the cause. A directory is returned exactly as given, never rewritten.
func resolveLockSourceRoot(explicit, flagName string) (string, error) {
	if explicit == "" {
		return "", nil
	}
	if strings.TrimSpace(explicit) == "" {
		return "", fmt.Errorf("%s %q: blank; name the source directory or omit the flag", flagName, explicit)
	}
	info, err := os.Stat(explicit)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("%s %q: missing: %w", flagName, explicit, err)
	}
	if err != nil {
		return "", fmt.Errorf("%s %q: %w", flagName, explicit, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s %q: not a directory", flagName, explicit)
	}
	return explicit, nil
}
