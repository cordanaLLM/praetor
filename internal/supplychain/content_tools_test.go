package supplychain

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// TestGenerateSLSAProvenance_Positive_DpkgDebPackage attests a package dpkg-deb itself
// builds, so the Debian rule tracks the real tool rather than only the fixtures above.
// dpkg-deb ships on Debian-family Linux, the CI Linux leg; elsewhere the test is skipped.
func TestGenerateSLSAProvenance_Positive_DpkgDebPackage(t *testing.T) {
	if _, err := exec.LookPath("dpkg-deb"); err != nil {
		t.Skipf("dpkg-deb is not installed (it ships on Debian-family Linux): %v", err)
	}
	root := filepath.Join(t.TempDir(), "pkg")
	control := filepath.Join(root, "DEBIAN")
	if err := os.MkdirAll(filepath.Join(root, "usr", "share", "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	// dpkg-deb refuses a control directory outside 0755..0775, whatever the umask made it.
	if err := os.MkdirAll(control, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(control, 0o755); err != nil {
		t.Fatal(err)
	}
	fields := "Package: demo\nVersion: 1.0\nArchitecture: all\nMaintainer: Example <maintainer@example.invalid>\nDescription: demo package\n"
	if err := os.WriteFile(filepath.Join(control, "control"), []byte(fields), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "usr", "share", "demo", "hello"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deb := filepath.Join(t.TempDir(), "demo_1.0_all.deb")
	if _, err := util.RunCommand(t.Context(), "", "dpkg-deb", "--root-owner-group", "--build", root, deb); err != nil {
		t.Fatalf("dpkg-deb --build: %v", err)
	}
	_, verdicts, err := GenerateSLSAProvenance(t.Context(), ProvenanceRequest{ArtifactPath: deb, BuilderID: "b"})
	if err != nil || verdicts[0].Status != ContentVerified {
		t.Fatalf("a package dpkg-deb built is not verified: %+v, %v", verdicts, err)
	}
}

// TestGenerateSLSAProvenance_UkifyImageAndBareKernel builds a Unified Kernel Image with
// ukify from an installed kernel and attests it, then refuses that kernel copied to a UKI
// name, which is the placeholder the content check exists for. It needs ukify and a
// readable kernel image under /usr/lib/modules, so it runs on Linux hosts that have both
// and is skipped elsewhere.
func TestGenerateSLSAProvenance_UkifyImageAndBareKernel(t *testing.T) {
	if _, err := exec.LookPath("ukify"); err != nil {
		t.Skipf("ukify is not installed (it ships with systemd on Linux): %v", err)
	}
	kernel := readableKernel(t)
	dir := t.TempDir()
	uki := filepath.Join(dir, "vmlinuz-test.efi")
	if _, err := util.RunCommand(t.Context(), dir, "ukify", "build", "--linux="+kernel, "--output="+uki); err != nil {
		t.Fatalf("ukify build: %v", err)
	}
	if _, verdicts, err := GenerateSLSAProvenance(t.Context(), ProvenanceRequest{ArtifactPath: uki, BuilderID: "b"}); err != nil || verdicts[0].Status != ContentVerified {
		t.Fatalf("a UKI ukify built is not verified: %+v, %v", verdicts, err)
	}
	bare := filepath.Join(dir, "vmlinuz-bare.efi")
	data, err := os.ReadFile(kernel)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bare, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := GenerateSLSAProvenance(t.Context(), ProvenanceRequest{ArtifactPath: bare, BuilderID: "b"}); err == nil {
		t.Fatal("a bare EFI-stub kernel under a UKI name was attested")
	}
}

// readableKernel returns the first kernel image under /usr/lib/modules the test can read,
// or skips the test.
func readableKernel(t *testing.T) string {
	t.Helper()
	matches, err := filepath.Glob("/usr/lib/modules/*/vmlinuz")
	if err != nil {
		t.Skipf("no kernel image glob: %v", err)
	}
	for _, match := range matches {
		if file, err := os.Open(match); err == nil {
			if closeErr := file.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
			return match
		}
	}
	t.Skip("no readable kernel image under /usr/lib/modules")
	return ""
}
