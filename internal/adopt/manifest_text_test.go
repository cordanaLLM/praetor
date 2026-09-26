package adopt

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"gopkg.in/yaml.v3"
)

func textPatchSources() *config.RegisterSources {
	return &config.RegisterSources{Expected: 13, SHA256: "sha256:" + strings.Repeat("a", 64),
		Inputs: managedHarnessInputs()}
}

// requireSources decodes manifest text and returns its register.sources, failing on a
// manifest the loader would reject.
func requireSources(t *testing.T, text string) *config.RegisterSources {
	t.Helper()
	var manifest config.Manifest
	if err := yaml.Unmarshal([]byte(text), &manifest); err != nil {
		t.Fatalf("patched manifest does not decode: %v\n%s", err, text)
	}
	if manifest.Register == nil || manifest.Register.Sources == nil {
		t.Fatalf("patched manifest has no register.sources:\n%s", text)
	}
	return manifest.Register.Sources
}

func writeSources(t *testing.T, manifest string, replace bool) string {
	t.Helper()
	out, changed, err := setManifestSources(t.Context(), []byte(manifest), textPatchSources(), replace)
	if err != nil || !changed {
		t.Fatalf("set sources: changed=%v err=%v", changed, err)
	}
	return string(out)
}

// TestSetManifestSourcesKeepsOperatorText: adding register.sources leaves every line the
// operator wrote as written, comments, blank lines and 4-space indentation included.
func TestSetManifestSourcesKeepsOperatorText(t *testing.T) {
	original := "# operator comment\nversion: 1\n\nrepository:\n    owner: acme   # trailing\n    name: legacy\n" +
		"profiles: [framework]\n"
	got := writeSources(t, original, false)
	if !strings.HasPrefix(got, original) || !strings.Contains(got, "\nregister:\n    sources:\n        expected: 13\n") {
		t.Fatalf("operator text rewritten or block not in its indentation:\n%s", got)
	}
	if sources := requireSources(t, got); !equalRegisterSources(sources, textPatchSources()) {
		t.Fatalf("patched sources = %+v", sources)
	}
}

// TestSetManifestSourcesInsertsIntoAndReplacesInsideRegister: under an existing register
// mapping the block lands inside it, and a replaced value touches only its own lines.
func TestSetManifestSourcesInsertsIntoAndReplacesInsideRegister(t *testing.T) {
	head := "version: 1\nregister:\n  surfaces:\n    hooks: internal\n"
	tail := "\n# overrides follow\noverrides: {}\n"
	inserted := writeSources(t, head+tail, false)
	if !strings.HasPrefix(inserted, head+"  sources:\n") || !strings.HasSuffix(inserted, tail) {
		t.Fatalf("sources not inserted at the end of register:\n%s", inserted)
	}
	stale := head + "  sources:\n    expected: 1\n    sha256: stale\n  evidence:\n    inline_max_lines: 58\n" + tail
	replaced := writeSources(t, stale, true)
	if !strings.HasPrefix(replaced, head+"  sources:\n    expected: 13\n") ||
		!strings.HasSuffix(replaced, "  evidence:\n    inline_max_lines: 58\n"+tail) || strings.Contains(replaced, "stale") {
		t.Fatalf("replacement touched lines outside register.sources:\n%s", replaced)
	}
	requireSources(t, replaced)
}

// TestSetManifestSourcesTextBoundaries: a missing final newline and CRLF endings are kept,
// and layouts the patch cannot address fall back to the full re-encode.
func TestSetManifestSourcesTextBoundaries(t *testing.T) {
	if got := writeSources(t, "version: 1\nprofiles: [framework]", false); !strings.HasPrefix(got,
		"version: 1\nprofiles: [framework]\nregister:\n") {
		t.Fatalf("missing final newline not completed:\n%s", got)
	}
	crlf := writeSources(t, "version: 1\r\nprofiles: [framework]\r\n", false)
	if !strings.HasPrefix(crlf, "version: 1\r\nprofiles: [framework]\r\nregister:\r\n") ||
		strings.Count(crlf, "\n") != strings.Count(crlf, "\r\n") {
		t.Fatalf("CRLF manifest got mixed line endings:\n%q", crlf)
	}
	requireSources(t, crlf)
	for name, manifest := range map[string]string{
		"flow root":     "{version: 1, profiles: [framework]}\n",
		"flow register": "version: 1\nregister: {surfaces: {hooks: internal}}\n",
	} {
		got := writeSources(t, manifest, false)
		if sources := requireSources(t, got); !equalRegisterSources(sources, textPatchSources()) {
			t.Fatalf("%s: re-encode fallback lost sources:\n%s", name, got)
		}
	}
	if _, ok, err := patchManifestSources([]byte("{version: 1}\n"), &yaml.Node{Kind: yaml.MappingNode,
		Style: yaml.FlowStyle}, textPatchSources()); ok || err != nil {
		t.Fatalf("flow root patched as text: ok=%v err=%v", ok, err)
	}
}
