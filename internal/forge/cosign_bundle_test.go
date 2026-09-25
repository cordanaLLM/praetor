package forge

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// cosignV2OnlyFlags are sign-blob flags that cosign v2 accepted and cosign v3 removed.
// Upstream reference: doc/cosign_sign-blob.md at v2.6.5 lists them, at v3.1.3 none of them
// survives; a release job that still passes one dies at the tag push, not in CI.
var cosignV2OnlyFlags = [...]string{"--output-signature", "--output-certificate", "--b64"}

// maxSigningDefects bounds the defect scan, one report per removed flag plus the bundle
// count (HISS-02).
const maxSigningDefects = len(cosignV2OnlyFlags) + 1

// maxSigningLines bounds the comment sweep of one signing surface (HISS-02).
const maxSigningLines = 4096

// maxVersionDigits bounds the major-version digit scan of one action pin (HISS-02).
const maxVersionDigits = 8

// cosignInstallerPinRegex captures the full version tag pinned for sigstore/cosign-installer,
// so a Renovate bump beyond v4 (say to v5) is parsed rather than matched against one literal.
var cosignInstallerPinRegex = regexp.MustCompile(`sigstore/cosign-installer@(\S+)`)

// cosignInstallerMajor extracts the major version pinned for sigstore/cosign-installer in
// text. It reports ok=false when the action is not pinned at all, or the pin does not start
// with a parseable "vN" major, so a bad edit fails closed instead of silently passing.
func cosignInstallerMajor(text string) (int, bool) {
	m := cosignInstallerPinRegex.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	tag := m[1]
	if len(tag) < 2 || tag[0] != 'v' {
		return 0, false
	}
	rest := tag[1:]
	end := 0
	for end < len(rest) && end < maxVersionDigits && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	major, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0, false
	}
	return major, true
}

// cosignInstallerCapable reports whether text pins sigstore/cosign-installer at v4 or newer,
// whose default cosign-release is a v3 capable of --bundle-only sign-blob invocations. A
// missing or unparseable pin is not capable.
func cosignInstallerCapable(text string) bool {
	major, ok := cosignInstallerMajor(text)
	return ok && major >= 4
}

// withoutComments drops whole-line YAML and shell comments, so prose that names a removed
// flag in order to explain the migration is not mistaken for an invocation that uses it.
func withoutComments(text string) string {
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	for i := 0; i < len(lines) && i < maxSigningLines; i++ {
		if !strings.HasPrefix(strings.TrimSpace(lines[i]), "#") {
			kept = append(kept, lines[i])
		}
	}
	return strings.Join(kept, "\n")
}

// cosignBundleDefects reports every way one signing surface would fail under cosign v3:
// a flag v3 no longer accepts, or a sign-blob invocation that writes no Sigstore bundle.
// cosign v3 defaults --use-signing-config to true, and that mode requires --bundle.
func cosignBundleDefects(name, raw string) []string {
	text := withoutComments(raw)
	defects := make([]string, 0, maxSigningDefects)
	for i := 0; i < len(cosignV2OnlyFlags) && i < maxSigningDefects; i++ {
		if strings.Contains(text, cosignV2OnlyFlags[i]) {
			defects = append(defects, fmt.Sprintf("%s: %s was removed from cosign v3 sign-blob", name, cosignV2OnlyFlags[i]))
		}
	}
	signings := strings.Count(text, "sign-blob")
	bundles := strings.Count(text, "--bundle")
	if signings > bundles {
		defects = append(defects, fmt.Sprintf("%s: %d sign-blob invocations but %d --bundle flags", name, signings, bundles))
	}
	return defects
}

// signingSurfaces returns the real files that invoke cosign sign-blob, keyed by path.
func signingSurfaces(t *testing.T) map[string]string {
	t.Helper()
	workflows, _ := engineWorkflows(t)
	surfaces := map[string]string{
		".github/workflows/release-binaries.yml": string(workflows["release-binaries.yml"]),
		".github/workflows/sbom.yml":             string(workflows["sbom.yml"]),
	}
	data, err := os.ReadFile(filepath.Join(engineRoot, ".goreleaser.yaml"))
	if err != nil {
		t.Fatalf("read .goreleaser.yaml: %v", err)
	}
	surfaces[".goreleaser.yaml"] = string(data)
	return surfaces
}

// Positive: every shipped signing surface is already on the bundle flags, and each one
// really does invoke sign-blob, so the rule is not passing on empty files.
func TestSigningSurfacesUseSigstoreBundles(t *testing.T) {
	surfaces := signingSurfaces(t)
	if len(surfaces) != 3 {
		t.Fatalf("expected the three signing surfaces, read %d", len(surfaces))
	}
	for name, text := range surfaces {
		if strings.Count(withoutComments(text), "sign-blob") == 0 {
			t.Errorf("%s: no sign-blob invocation; the signing step moved and this rule now checks nothing", name)
		}
		if defects := cosignBundleDefects(name, text); len(defects) != 0 {
			t.Errorf("%s: %v", name, defects)
		}
	}
}

// Positive: cosign v3 only reaches the runner when the installer is v4 or newer, whose
// default cosign-release is a v3. An installer held at v3.x would install cosign v2 and
// reject --bundle-only invocations.
func TestWorkflowsInstallCosignV3CapableInstaller(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	for _, name := range []string{"release-binaries.yml", "sbom.yml"} {
		text := string(workflows[name])
		if !cosignInstallerCapable(text) {
			t.Errorf("%s: cosign-installer is not pinned at v4 or newer, so the runner would get cosign v2", name)
		}
		if strings.Contains(text, "cosign-release:") {
			t.Errorf("%s: pins cosign-release; the installer default is the tracked version", name)
		}
	}
}

// Negative: the pre-migration text is rejected, one defect per removed flag plus the
// missing bundle, so the rule is tied to the flags rather than merely present.
func TestCosignBundleDefectsRejectsRemovedV2Flags(t *testing.T) {
	const v2 = "cosign sign-blob --yes --output-certificate a.cert --output-signature a.sig a.json"
	defects := cosignBundleDefects("legacy.yml", v2)
	if len(defects) != 3 {
		t.Fatalf("pre-migration invocation yielded %d defects, want 3: %v", len(defects), defects)
	}
	for _, want := range []string{"--output-certificate", "--output-signature", "0 --bundle"} {
		if !strings.Contains(strings.Join(defects, "\n"), want) {
			t.Errorf("defects %v do not mention %q", defects, want)
		}
	}
}

// Boundary: no sign-blob at all is clean, one signing to one bundle is clean, and two
// signings sharing a single bundle flag is not.
func TestCosignBundleDefectsBoundaries(t *testing.T) {
	cases := []struct {
		name, text string
		want       int
	}{
		{"no signing surface", "", 0},
		{"comment naming a removed flag", "# cosign v3 dropped --output-signature from sign-blob", 0},
		{"unrelated cosign command", "cosign verify-blob --bundle a.sigstore.json a.json", 0},
		{"one signing one bundle", "cosign sign-blob --yes --bundle a.sigstore.json a.json", 0},
		{"two signings one bundle", "cosign sign-blob --bundle a.sigstore.json a\ncosign sign-blob b", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cosignBundleDefects(tc.name, tc.text); len(got) != tc.want {
				t.Errorf("defects %v, want %d", got, tc.want)
			}
		})
	}
}

// Boundary: v4 and any newer major such as a Renovate bump to v5 are capable, the
// pre-migration v3 pin is not, and a pin the parser cannot read fails closed rather than
// passing by accident.
func TestCosignInstallerCapableBoundaries(t *testing.T) {
	cases := []struct {
		name, text string
		want       bool
	}{
		{"pinned at v4", "uses: sigstore/cosign-installer@v4.1.2", true},
		{"renovate bump to v5", "uses: sigstore/cosign-installer@v5.0.0", true},
		{"pre-migration v3", "uses: sigstore/cosign-installer@v3.8.1", false},
		{"unparseable pin", "uses: sigstore/cosign-installer@latest", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := cosignInstallerCapable(tc.text); got != tc.want {
				t.Errorf("cosignInstallerCapable(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}
