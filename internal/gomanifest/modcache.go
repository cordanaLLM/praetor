// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package gomanifest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrUnescapable marks a module path or version the module cache cannot spell.
var ErrUnescapable = errors.New("module cache escape")

// EscapeModuleCachePath returns a module path or version as the Go module cache
// spells it on disk: every upper-case ASCII letter becomes '!' followed by its
// lower-case form, so a case-insensitive file system cannot conflate two
// modules. github.com/BurntSushi/toml is stored as github.com/!burnt!sushi/toml.
//
// It is the encoding of golang.org/x/mod/module.EscapePath and EscapeVersion,
// written here so a cache lookup does not need that module. An input that
// already contains '!' is refused rather than escaped: '!' is not valid in a
// module path or version, so such an input could only produce a cache path
// that belongs to a different module.
func EscapeModuleCachePath(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("%w: empty module path or version", ErrUnescapable)
	}
	if strings.ContainsRune(value, '!') {
		return "", fmt.Errorf("%w: %q contains '!'", ErrUnescapable, value)
	}
	var escaped strings.Builder
	escaped.Grow(len(value) + 8)
	for _, r := range value {
		if r >= 'A' && r <= 'Z' {
			escaped.WriteByte('!')
			escaped.WriteRune(r + ('a' - 'A'))
			continue
		}
		escaped.WriteRune(r)
	}
	return escaped.String(), nil
}

// ModuleCacheRoot returns the directory the go command uses as its module
// cache, resolved as the go command resolves it: GOMODCACHE when set, otherwise
// pkg/mod under the first GOPATH entry, otherwise pkg/mod under the default
// GOPATH of $HOME/go. It reads the environment only; a value written with
// `go env -w` and absent from the environment is not seen. ok is false when no
// root can be resolved at all.
func ModuleCacheRoot() (root string, ok bool) {
	if modCache := os.Getenv("GOMODCACHE"); modCache != "" {
		return modCache, true
	}
	for _, entry := range filepath.SplitList(os.Getenv("GOPATH")) {
		if entry != "" {
			return filepath.Join(entry, "pkg", "mod"), true
		}
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	return filepath.Join(home, "go", "pkg", "mod"), true
}

// ModuleCacheDir returns the path, relative to a module cache root, of the
// extracted module at version: the escaped path, '@', the escaped version.
func ModuleCacheDir(modulePath, version string) (string, error) {
	escapedPath, err := EscapeModuleCachePath(modulePath)
	if err != nil {
		return "", err
	}
	escapedVersion, err := EscapeModuleCachePath(version)
	if err != nil {
		return "", err
	}
	return filepath.FromSlash(escapedPath) + "@" + escapedVersion, nil
}
