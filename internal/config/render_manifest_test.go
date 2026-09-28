package config

import (
	"reflect"
	"strings"
	"testing"
)

// Positive: the rendering opens with the document start, indents by two spaces, fits the long
// register digest onto its own line and decodes back to the same manifest.
func TestRenderManifest_Positive_LintCleanRoundTrip(t *testing.T) {
	manifest := &Manifest{Version: 1, Repository: RepositoryMetadata{Owner: "acme", Name: "service"},
		Profiles: []string{"framework"}, Facets: []string{"security:high"},
		Register: &RegisterPolicy{Sources: &RegisterSources{Expected: 1,
			SHA256: "sha256:" + strings.Repeat("ab", 32),
			Inputs: []RegisterSourceInput{{Path: ".paperclip/harness.json", Surface: SurfacePrompts, Kind: "message",
				Format: SourceFormatJSON, Selector: "invariants.*"}}}}}
	data, err := RenderManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.HasPrefix(text, "---\nversion: 1\nrepository:\n  owner: acme\n") {
		t.Errorf("unexpected layout:\n%s", text)
	}
	if !strings.Contains(text, "    sha256:\n      sha256:"+strings.Repeat("ab", 32)+"\n") {
		t.Errorf("the register digest was not fitted:\n%s", text)
	}
	decoded, err := DecodeManifest(data)
	if err != nil || decoded.Register.Sources.SHA256 != manifest.Register.Sources.SHA256 ||
		!reflect.DeepEqual(decoded.Register.Sources.Inputs, manifest.Register.Sources.Inputs) {
		t.Fatalf("round trip = %+v, %v", decoded, err)
	}
	again, err := RenderManifest(decoded)
	if err != nil || string(again) != text {
		t.Fatalf("re-rendering the decoded manifest must reproduce the text:\n%s", again)
	}
}

// Negative: there is no text for no manifest.
func TestRenderManifest_Negative_NilManifest(t *testing.T) {
	if data, err := RenderManifest(nil); err == nil || data != nil {
		t.Fatalf("RenderManifest(nil) = %q, %v", data, err)
	}
}

// Boundary: the zero manifest still renders one decodable document.
func TestRenderManifest_Boundary_ZeroManifest(t *testing.T) {
	data, err := RenderManifest(&Manifest{})
	if err != nil || !strings.HasPrefix(string(data), "---\n") {
		t.Fatalf("RenderManifest(zero) = %q, %v", data, err)
	}
	if _, err := DecodeManifest(data); err != nil {
		t.Fatalf("the zero rendering must decode: %v", err)
	}
}
