// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package gomanifest

import (
	"errors"
	"path/filepath"
	"testing"
)

// Positive and boundary: upper-case letters are escaped the way the go command
// lays the module cache out; a path with none is returned unchanged.
func TestEscapeModuleCachePathMatchesTheCacheLayout(t *testing.T) {
	for in, want := range map[string]string{
		"github.com/BurntSushi/toml": "github.com/!burnt!sushi/toml",
		"github.com/Azure/azure-sdk": "github.com/!azure/azure-sdk",
		"v1.0.0-RC1":                 "v1.0.0-!r!c1",
		"gopkg.in/yaml.v3":           "gopkg.in/yaml.v3",
		"Z":                          "!z",
	} {
		got, err := EscapeModuleCachePath(in)
		if err != nil || got != want {
			t.Errorf("EscapeModuleCachePath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

// Negative: an empty value and a value already carrying '!' are refused; the
// latter would name another module's cache directory.
func TestEscapeModuleCachePathRefusesUnescapableInput(t *testing.T) {
	for _, in := range []string{"", "github.com/!burnt!sushi/toml"} {
		if got, err := EscapeModuleCachePath(in); !errors.Is(err, ErrUnescapable) {
			t.Errorf("EscapeModuleCachePath(%q) = %q, %v; want ErrUnescapable", in, got, err)
		}
	}
}

// Positive and negative: the cache directory joins the escaped path and the
// escaped version, and an unescapable half fails the whole.
func TestModuleCacheDirJoinsEscapedPathAndVersion(t *testing.T) {
	got, err := ModuleCacheDir("github.com/BurntSushi/toml", "v1.4.0")
	want := filepath.FromSlash("github.com/!burnt!sushi/toml") + "@v1.4.0"
	if err != nil || got != want {
		t.Fatalf("got %q, %v; want %q", got, err, want)
	}
	if got, err := ModuleCacheDir("github.com/BurntSushi/toml", ""); !errors.Is(err, ErrUnescapable) {
		t.Fatalf("empty version accepted: %q, %v", got, err)
	}
}

// Positive: GOMODCACHE wins when set.
func TestModuleCacheRootPrefersGOMODCACHE(t *testing.T) {
	modCache := t.TempDir()
	t.Setenv("GOMODCACHE", modCache)
	t.Setenv("GOPATH", t.TempDir())
	if got, ok := ModuleCacheRoot(); !ok || got != modCache {
		t.Fatalf("got %q, %v; want %q", got, ok, modCache)
	}
}

// Boundary: without GOMODCACHE the first GOPATH entry's pkg/mod is used, and
// without either the default GOPATH under the home directory.
func TestModuleCacheRootFallsBackThroughGOPATHToHome(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	t.Setenv("GOMODCACHE", "")
	t.Setenv("GOPATH", first+string(filepath.ListSeparator)+second)
	if got, ok := ModuleCacheRoot(); !ok || got != filepath.Join(first, "pkg", "mod") {
		t.Fatalf("GOPATH fallback: got %q, %v", got, ok)
	}

	home := t.TempDir()
	t.Setenv("GOPATH", "")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("home", home)
	if got, ok := ModuleCacheRoot(); !ok || got != filepath.Join(home, "go", "pkg", "mod") {
		t.Fatalf("home fallback: got %q, %v", got, ok)
	}
}
