package devcontainer

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	wantLegacyPostCreate = "DevContainer postCreateCommand runs 'go run ./cmd/standardsctl', which no longer exists; regenerate with 'praetorctl devcontainer generate --force'"
	wantLegacyVersion    = "bootstrap specification v1 predates the praetorctl rename; regenerate with 'praetorctl devcontainer generate --source-root <praetor checkout> --force'"
)

func TestBootstrapV2RendersRenamedCLIWithoutAlias(t *testing.T) {
	bundle := preparedBootstrap(t)
	spec := bundle.Spec()
	if spec.Version != 2 {
		t.Fatalf("bootstrap version = %d, want 2", spec.Version)
	}
	dockerfile := renderBootstrapDockerfile(spec)
	if !strings.HasPrefix(dockerfile, "# Praetor bootstrap v2; selected source digest ") {
		t.Fatalf("unexpected bootstrap header: %q", strings.SplitN(dockerfile, "\n", 2)[0])
	}
	if !strings.Contains(dockerfile, " -o /out/praetorctl ./cmd/praetorctl\n") {
		t.Fatal("bootstrap Dockerfile does not build ./cmd/praetorctl")
	}
	if strings.Contains(dockerfile, "standards") || strings.Count(dockerfile, "/usr/local/bin/") != 1 {
		t.Fatalf("bootstrap Dockerfile installs a legacy alias:\n%s", dockerfile)
	}
	root := t.TempDir()
	target := filepath.Join(root, ".devcontainer", "devcontainer.json")
	if err := WriteBundle(t.Context(), target, bundle, false); err != nil {
		t.Fatal(err)
	}
	if err := Verify(t.Context(), target, mustBaseContainer(t)); err != nil {
		t.Fatalf("v2 regenerate/verify round trip failed: %v", err)
	}
}

func TestBootstrapSourceRequiresRenamedCLI(t *testing.T) {
	root := bootstrapSourceFixture(t)
	writeBootstrapFile(t, root, "cmd/standardsctl/main.go", "package main\nfunc main() {}\n")
	if err := os.Remove(filepath.Join(root, "cmd", "praetorctl", "main.go")); err != nil {
		t.Fatal(err)
	}
	_, err := PrepareBundle(t.Context(), "app", nil, nil, BootstrapOptions{SourceRoot: root})
	if err == nil || !strings.Contains(err.Error(), "bootstrap requires CLI sources") {
		t.Fatalf("source without cmd/praetorctl/main.go accepted: %v", err)
	}
}

func TestBootstrapVersionOneSourceBundleRejectedWithRegenerateMessage(t *testing.T) {
	bundle := preparedBootstrap(t)
	root := t.TempDir()
	target := filepath.Join(root, ".devcontainer", "devcontainer.json")
	if err := WriteBundle(t.Context(), target, bundle, false); err != nil {
		t.Fatal(err)
	}
	rewriteBootstrapVersion(t, target, 1)
	err := Verify(t.Context(), target, mustBaseContainer(t))
	if !errors.Is(err, ErrLegacyBootstrapVersion) || err.Error() != wantLegacyVersion {
		t.Fatalf("v1 source bundle error = %v, want %q", err, wantLegacyVersion)
	}
}

func TestBootstrapSpecVersionBoundaries(t *testing.T) {
	ready := *preparedBootstrap(t).Spec()
	unavailable := BootstrapSpec{Version: 1, State: BootstrapUnavailable, BaseImage: DefaultBaseImage, Reason: "config-only catalog"}
	cases := []struct {
		name string
		spec BootstrapSpec
		want error
		text string
	}{
		{name: "current-ready", spec: ready},
		{name: "v1-unavailable-stays-valid", spec: unavailable},
		{name: "v1-source-bundle", spec: withVersion(ready, 1), want: ErrLegacyBootstrapVersion},
		{name: "v1-unknown-state", spec: withState(withVersion(ready, 1), "other"), want: ErrLegacyBootstrapVersion},
		{name: "v0", spec: withVersion(ready, 0), text: "unsupported or absent bootstrap specification"},
		{name: "v3", spec: withVersion(ready, 3), text: "unsupported or absent bootstrap specification"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateBootstrapSpec(&tc.spec)
			switch {
			case tc.want != nil:
				if !errors.Is(err, tc.want) {
					t.Fatalf("error = %v, want %v", err, tc.want)
				}
			case tc.text != "":
				if err == nil || err.Error() != tc.text {
					t.Fatalf("error = %v, want %q", err, tc.text)
				}
			case err != nil:
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
	if err := validateBootstrapSpec(nil); err == nil {
		t.Fatal("absent bootstrap specification accepted")
	}
}

func TestLegacyPostCreateRejectedBeforeConfigurationComparison(t *testing.T) {
	for name, command := range map[string]string{
		"exact":       "go run ./cmd/standardsctl compile-context && make verify-all",
		"embedded":    "cd /workspace && go run ./cmd/standardsctl",
		"prefix-only": "go run ./cmd/standardsctl-fork audit",
	} {
		t.Run(name, func(t *testing.T) {
			dc := mustBaseContainer(t)
			dc.PostCreateCommand = command
			path := filepath.Join(t.TempDir(), ".devcontainer", "devcontainer.json")
			if err := WriteDevContainer(t.Context(), path, dc); err != nil {
				t.Fatal(err)
			}
			err := Verify(t.Context(), path, mustBaseContainer(t))
			if !errors.Is(err, ErrLegacyPostCreate) || err.Error() != wantLegacyPostCreate {
				t.Fatalf("legacy postCreateCommand error = %v, want %q", err, wantLegacyPostCreate)
			}
		})
	}
}

func TestCurrentPostCreateIsNotReportedAsLegacy(t *testing.T) {
	dc := mustBaseContainer(t)
	if dc.PostCreateCommand != "go run ./cmd/praetorctl compile-context && make verify-all" {
		t.Fatalf("framework postCreateCommand = %q", dc.PostCreateCommand)
	}
	if err := rejectLegacyPostCreate(dc); err != nil {
		t.Fatalf("current postCreateCommand rejected: %v", err)
	}
	if err := rejectLegacyPostCreate(nil); err != nil {
		t.Fatalf("absent DevContainer rejected as legacy: %v", err)
	}
	path := filepath.Join(t.TempDir(), ".devcontainer", "devcontainer.json")
	if err := WriteDevContainer(t.Context(), path, dc); err != nil {
		t.Fatal(err)
	}
	if err := Verify(t.Context(), path, dc); errors.Is(err, ErrLegacyPostCreate) {
		t.Fatalf("current self-host configuration reported as legacy: %v", err)
	}
}

func withVersion(spec BootstrapSpec, version int) BootstrapSpec {
	spec.Version = version
	return spec
}

func withState(spec BootstrapSpec, state string) BootstrapSpec {
	spec.State = state
	return spec
}

func rewriteBootstrapVersion(t *testing.T, path string, version int) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var dc DevContainer
	if err := json.Unmarshal(data, &dc); err != nil {
		t.Fatal(err)
	}
	dc.Customizations.Praetor.Bootstrap.Version = version
	rendered, err := Render(&dc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, rendered, 0600); err != nil {
		t.Fatal(err)
	}
}
