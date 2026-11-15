// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package compiler

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// Positive and boundary: an explicit development version works without tags, the shipped
// manifest has it, and a repository shipping no plugin gains no new requirement.
func TestPluginVersion_PositiveAndBoundary(t *testing.T) {
	root := t.TempDir()
	if err := VerifyPluginVersion(t.Context(), root, ""); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPluginVersion(t.Context(), root, "0.1.0"); err == nil {
		t.Fatal("an explicit release version must require a manifest")
	}
	writeRegisterFixture(t, root, PluginManifestRel, `{"name":"praetor","version":"0.1.0-dev"}`)
	if err := VerifyPluginVersion(t.Context(), root, "0.1.0-dev"); err != nil {
		t.Fatal(err)
	}
	if err := VerifyPluginVersion(t.Context(), filepath.Join("..", ".."), ""); err != nil {
		t.Fatalf("shipped plugin: %v", err)
	}
}

// Negative: the engine refuses absent, malformed and mismatched versions without agy.
func TestPluginVersion_Negative(t *testing.T) {
	cases := []struct{ name, manifest, expected, problem string }{
		{"missing", `{"name":"praetor"}`, "", "version required"},
		{"malformed", `{"version":"0.1"}`, "", "malformed SemVer"},
		{"tag_prefix", `{"version":"v0.1.0"}`, "", "malformed SemVer"},
		{"whitespace", `{"version":" 0.1.0"}`, "", "malformed SemVer"},
		{"duplicate", `{"version":"0.1.0","version":"0.2.0"}`, "", "duplicate"},
		{"mismatch", `{"version":"0.2.0"}`, "0.1.0", "differs from release"},
	}
	for i := 0; i < len(cases) && i < 16; i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeRegisterFixture(t, root, PluginManifestRel, tc.manifest)
			if err := VerifyPluginVersion(t.Context(), root, tc.expected); err == nil || !strings.Contains(err.Error(), tc.problem) {
				t.Fatalf("VerifyPluginVersion: %v; want %s", err, tc.problem)
			}
		})
	}
}

// Positive and boundary: release stamping preserves metadata, verifies exact readback and
// is repeatable on the same release version.
func TestStampPluginReleaseVersion_PositiveAndBoundary(t *testing.T) {
	root := t.TempDir()
	writeRegisterFixture(t, root, PluginManifestRel, `{"name":"praetor","version":"0.1.0-dev","custom":{"keep":true}}`)
	for i := 0; i < 2; i++ {
		if err := StampPluginReleaseVersion(t.Context(), root, "0.1.0"); err != nil {
			t.Fatal(err)
		}
		if err := VerifyPluginVersion(t.Context(), root, "0.1.0"); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(PluginManifestRel)))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct{ Custom struct{ Keep bool } }
	if err := json.Unmarshal(data, &manifest); err != nil || !manifest.Custom.Keep {
		t.Fatalf("metadata lost: %s, %v", data, err)
	}
}

// Negative: compile-context's shared verifier applies the version check, even with no
// canonical personas or skills; the failure remains an agent-surface failure for adoption.
func TestPluginVersion_Negative_ContextVerifier(t *testing.T) {
	root := t.TempDir()
	writeRegisterFixture(t, root, PluginManifestRel, `{"name":"praetor"}`)
	_, err := verifyAgentSurfaces(t.Context(), &syncWriter{w: io.Discard}, root)
	if err == nil || !strings.Contains(err.Error(), "version required") {
		t.Fatalf("context verifier accepted a missing version: %v", err)
	}
}

// Negative: missing or malformed versions and release-line drift leave the input intact.
func TestStampPluginReleaseVersion_Negative(t *testing.T) {
	cases := []struct{ name, manifest, release string }{
		{"missing", `{"name":"praetor"}`, "0.1.0"},
		{"malformed", `{"version":"bad"}`, "0.1.0"},
		{"mismatch", `{"version":"0.2.0-dev"}`, "0.1.0"},
		{"invalid_release", `{"version":"0.1.0-dev"}`, "bad"},
		{"empty_release", `{"version":"0.1.0-dev"}`, ""},
	}
	for i := 0; i < len(cases) && i < 16; i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			writeRegisterFixture(t, root, PluginManifestRel, tc.manifest)
			if err := StampPluginReleaseVersion(t.Context(), root, tc.release); err == nil {
				t.Fatal("invalid stamp accepted")
			}
			if got := readFixtureText(t, filepath.Join(root, filepath.FromSlash(PluginManifestRel))); got != tc.manifest {
				t.Fatalf("refusal changed manifest: %s", got)
			}
		})
	}
}

// The release must consume the same tag-derived .Version as the binary stamp and actually
// package the stamped plugin. Removing either configuration entry invalidates this contract.
func TestPluginVersion_ReleaseConfiguration(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".goreleaser.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Before   struct{ Hooks []string }
		Archives []struct{ Files []string }
	}
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	const hook = "go run ./cmd/standardsctl release plugin-version --version={{.Version}}"
	if !slices.Contains(config.Before.Hooks, hook) {
		t.Fatal("release no longer stamps the plugin from the binaries' version source")
	}
	if len(config.Archives) == 0 || len(config.Archives) > 16 {
		t.Fatalf("unexpected archive count %d", len(config.Archives))
	}
	for i := 0; i < len(config.Archives) && i < 16; i++ {
		if !slices.Contains(config.Archives[i].Files, ".agents/plugins/praetor/**/*") {
			t.Errorf("archive %d omits the stamped plugin", i)
		}
	}
}
