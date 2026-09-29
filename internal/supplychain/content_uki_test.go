package supplychain

import (
	"errors"
	"strings"
	"testing"
)

// Positive: a bare EFI-stub kernel under a kernel name only has to be an EFI image, and a
// UKI glob declares a kernel-named subject a Unified Kernel Image, alone or in a manifest.
func TestGenerateSLSAProvenance_Positive_KernelNamesAndDeclaredUKI(t *testing.T) {
	bare, _ := writeArtifact(t, "vmlinuz-7.efi", bareKernelFixture())
	_, verdicts, err := GenerateSLSAProvenance(t.Context(), ProvenanceRequest{ArtifactPath: bare, BuilderID: "b"})
	if err != nil || verdicts[0].Status != ContentVerified || verdicts[0].Format != efiFormat.name {
		t.Errorf("bare EFI-stub kernel vmlinuz-7.efi: %+v, %v; want verified as an EFI image", verdicts, err)
	}
	uki, _ := writeArtifact(t, "vmlinuz-7.2.4.efi", ukiFixture())
	_, verdicts, err = GenerateSLSAProvenance(t.Context(), ProvenanceRequest{ArtifactPath: uki, BuilderID: "b", UKIGlobs: []string{"vmlinuz-*.efi"}})
	if err != nil || verdicts[0].Status != ContentVerified || verdicts[0].Format != ukiFormat.name {
		t.Errorf("declared UKI vmlinuz-7.2.4.efi: %+v, %v; want verified as a UKI", verdicts, err)
	}
	contents := map[string][]byte{"EFI/BOOT/BOOTX64.EFI": bareKernelFixture(), "boot/vmlinuz-7.2.4.efi": ukiFixture()}
	manifest, _ := releaseManifestOf(t, []string{"a.tar.gz", "EFI/BOOT/BOOTX64.EFI", "boot/vmlinuz-7.2.4.efi"}, func(name string) []byte {
		if content, ok := contents[name]; ok {
			return content
		}
		return []byte("archive")
	})
	_, verdicts, err = GenerateSLSAProvenanceFromChecksums(t.Context(), ChecksumsRequest{ManifestPath: manifest, BuilderID: "b", UKIGlobs: []string{"**/vmlinuz-*.efi"}})
	if err != nil || len(verdicts) != 3 {
		t.Fatalf("manifest with a declared UKI: %+v, %v", verdicts, err)
	}
	want := []ContentVerdict{
		{Name: "a.tar.gz", Status: ContentUnchecked},
		{Name: "EFI/BOOT/BOOTX64.EFI", Format: efiFormat.name, Status: ContentVerified},
		{Name: "boot/vmlinuz-7.2.4.efi", Format: ukiFormat.name, Status: ContentVerified},
	}
	for i := range want {
		if verdicts[i] != want[i] {
			t.Errorf("verdict %d = %+v, want %+v", i, verdicts[i], want[i])
		}
	}
}

// Negative: a text placeholder under a kernel name fails the EFI check, and a PE image
// without .linux fails once a UKI glob declares it or it sits directly in EFI/Linux/; each
// refusal names what chose the rule when the path does not show it.
func TestGenerateSLSAProvenance_Negative_UKIRuleRefusals(t *testing.T) {
	placeholder, _ := writeArtifact(t, "vmlinuz-7.2.4.efi", []byte(strings.Repeat("placeholder kernel image\n", 4)))
	bare, _ := writeArtifact(t, "vmlinuz-7.efi", bareKernelFixture())
	cases := map[string]struct {
		req  ProvenanceRequest
		want []string
	}{
		"text placeholder": {ProvenanceRequest{ArtifactPath: placeholder}, []string{efiFormat.name, "MZ"}},
		"declared by glob": {ProvenanceRequest{ArtifactPath: bare, UKIGlobs: []string{"vmlinuz-*.efi"}},
			[]string{ukiFormat.name, "no .linux section", `declared a Unified Kernel Image by UKI glob "vmlinuz-*.efi"`}},
		"directly in EFI/Linux": {ProvenanceRequest{ArtifactPath: bare, ArtifactName: "EFI/Linux/x.efi"},
			[]string{ukiFormat.name, "no .linux section", "(subject EFI/Linux/x.efi)"}},
	}
	for label, tc := range cases {
		tc.req.BuilderID = "b"
		stmt, _, err := GenerateSLSAProvenance(t.Context(), tc.req)
		if stmt != nil || !errors.Is(err, ErrContentMismatch) {
			t.Errorf("%s: statement %v, err %v; want a content mismatch", label, stmt, err)
			continue
		}
		for _, want := range tc.want {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: %v; want it to contain %q", label, err, want)
			}
		}
	}
	_, verdicts, err := GenerateSLSAProvenance(t.Context(), ProvenanceRequest{ArtifactPath: bare, BuilderID: "b", UKIGlobs: []string{"*"}, ContentCheck: ContentCheckReport})
	if err != nil || verdicts[0].Status != ContentUnverified || verdicts[0].Format != ukiFormat.name || !strings.Contains(verdicts[0].Reason, ".linux") {
		t.Errorf("report mode, declared UKI without .linux: %+v, %v", verdicts, err)
	}
	manifest, _ := releaseManifestOf(t, []string{"EFI/Linux/x.efi"}, func(string) []byte { return bareKernelFixture() })
	if _, _, err := GenerateSLSAProvenanceFromChecksums(t.Context(), ChecksumsRequest{ManifestPath: manifest, BuilderID: "b"}); !errors.Is(err, ErrContentMismatch) ||
		!strings.Contains(err.Error(), "checksum line 1") || !strings.Contains(err.Error(), "no .linux section") {
		t.Errorf("manifest line EFI/Linux/x.efi without .linux: %v", err)
	}
}

// Boundary: the glob count and segment limits hold at their edge, a malformed or blank glob
// is refused before any file is read, a glob matches names in full and case included, and a
// glob that matches no subject refuses the statement.
func TestUKIGlobs_Boundary(t *testing.T) {
	atLimit := make([]string, maxUKIGlobs)
	for i := range atLimit {
		atLimit[i] = "*.efi"
	}
	deepest := strings.TrimSuffix(strings.Repeat("*/", maxUKIGlobSegments), "/")
	for label, globs := range map[string][]string{"no globs": nil, "at the count limit": atLimit, "at the segment limit": {deepest}} {
		if err := validateUKIGlobs(globs); err != nil {
			t.Errorf("%s refused: %v", label, err)
		}
	}
	refused := map[string][]string{
		"over the count limit":   append(append([]string{}, atLimit...), "*.efi"),
		"over the segment limit": {deepest + "/*"},
		"blank":                  {" "},
		"malformed class":        {"vmlinuz-[.efi"},
		"trailing escape":        {`uki\`},
	}
	for label, globs := range refused {
		if err := validateUKIGlobs(globs); err == nil {
			t.Errorf("%s accepted", label)
		}
	}
	if _, _, err := GenerateSLSAProvenanceFromChecksums(t.Context(), ChecksumsRequest{ManifestPath: "missing/checksums.txt", UKIGlobs: []string{"["}}); err == nil || !strings.Contains(err.Error(), `UKI glob "["`) {
		t.Errorf("a malformed glob was not refused before the manifest was read: %v", err)
	}
	matches := map[string]string{
		"vmlinuz.efi":                  "*.efi",
		"EFI/Linux/entry.efi":          "**/*.efi",
		`EFI\Linux\entry.efi`:          "EFI/Linux/*.efi",
		"boot/efi/EFI/Linux/entry.efi": "**/Linux/*.efi",
	}
	for name, glob := range matches {
		if got := matchingUKIGlob([]string{"none", glob}, name); got != glob {
			t.Errorf("matchingUKIGlob(%q, %q) = %q", glob, name, got)
		}
	}
	for name, glob := range map[string]string{"EFI/Linux/entry.efi": "*.efi", "VMLINUZ-7.EFI": "vmlinuz-*.efi", "vmlinuz-7.efi.sig": "vmlinuz-*.efi"} {
		if got := matchingUKIGlob([]string{glob}, name); got != "" {
			t.Errorf("matchingUKIGlob(%q, %q) = %q, want no match", glob, name, got)
		}
	}
	other, _ := writeArtifact(t, "tool.tar.gz", []byte("archive"))
	if stmt, _, err := GenerateSLSAProvenance(t.Context(), ProvenanceRequest{ArtifactPath: other, BuilderID: "b", UKIGlobs: []string{"vmlinuz-*.efi"}}); stmt != nil || err == nil || !strings.Contains(err.Error(), "matches no subject name") {
		t.Errorf("a glob matching no subject: statement %v, err %v", stmt, err)
	}
	manifest, _ := releaseManifest(t, []string{"a.tar.gz", "b.tar.gz"})
	if _, _, err := GenerateSLSAProvenanceFromChecksums(t.Context(), ChecksumsRequest{
		ManifestPath: manifest, BuilderID: "b", UKIGlobs: []string{"a.*", "*.efi"}, ContentCheck: ContentCheckReport,
	}); err == nil || !strings.Contains(err.Error(), `UKI glob "*.efi" matches no subject name`) {
		t.Errorf("a manifest glob matching no listed name: %v", err)
	}
}
