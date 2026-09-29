package supplychain

import (
	"debug/pe"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
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
// ukify from an installed kernel and attests it as a declared UKI. It then attests that
// kernel copied to vmlinuz-bare.efi, which a bare EFI-stub kernel may be called, and refuses
// the same file once a UKI glob declares it, which is the placeholder the check exists for.
// It needs ukify, its linux stub and a readable kernel image under /usr/lib/modules, so it
// runs on Linux hosts that have all three and is skipped elsewhere with the reason.
func TestGenerateSLSAProvenance_UkifyImageAndBareKernel(t *testing.T) {
	if _, err := exec.LookPath("ukify"); err != nil {
		t.Skipf("ukify is not installed (it ships with systemd on Linux): %v", err)
	}
	systemdStub(t, "linux")
	kernel := readableKernel(t)
	dir := t.TempDir()
	uki := filepath.Join(dir, "vmlinuz-test.efi")
	if _, err := util.RunCommand(t.Context(), dir, "ukify", "build", "--linux="+kernel, "--output="+uki); err != nil {
		t.Fatalf("ukify build: %v", err)
	}
	declared := ProvenanceRequest{ArtifactPath: uki, BuilderID: "b", UKIGlobs: []string{"vmlinuz-*.efi"}}
	if _, verdicts, err := GenerateSLSAProvenance(t.Context(), declared); err != nil || verdicts[0].Status != ContentVerified || verdicts[0].Format != ukiFormat.name {
		t.Fatalf("a UKI ukify built is not verified as a UKI: %+v, %v", verdicts, err)
	}
	bare := copyEFIStubKernel(t, kernel, filepath.Join(dir, "vmlinuz-bare.efi"))
	if _, verdicts, err := GenerateSLSAProvenance(t.Context(), ProvenanceRequest{ArtifactPath: bare, BuilderID: "b"}); err != nil || verdicts[0].Format != efiFormat.name {
		t.Fatalf("a bare EFI-stub kernel named vmlinuz-bare.efi is not verified as an EFI image: %+v, %v", verdicts, err)
	}
	declared.ArtifactPath = bare
	if _, _, err := GenerateSLSAProvenance(t.Context(), declared); !errors.Is(err, ErrContentMismatch) || !strings.Contains(err.Error(), "no .linux section") {
		t.Fatalf("a bare EFI-stub kernel declared a UKI: got %v, want a content mismatch naming the missing .linux section", err)
	}
}

// copyEFIStubKernel copies kernel to target and returns target, or skips the test when
// debug/pe, a parser independent of the content probe, does not read the kernel as a PE
// image with an EFI subsystem: some distributions install a compressed kernel that is no
// EFI-stub kernel at all.
func copyEFIStubKernel(t *testing.T, kernel, target string) string {
	t.Helper()
	image, err := pe.Open(kernel)
	if err != nil {
		t.Skipf("installed kernel %s is not a PE image: %v", kernel, err)
	}
	var subsystem uint16
	switch header := image.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		subsystem = header.Subsystem
	case *pe.OptionalHeader32:
		subsystem = header.Subsystem
	}
	if closeErr := image.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if subsystem < peEFIApplication || subsystem > peEFIROM {
		t.Skipf("installed kernel %s has PE subsystem %d, not an EFI one", kernel, subsystem)
	}
	data, err := os.ReadFile(kernel)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return target
}

// TestGenerateSLSAProvenance_Positive_UkifyAddon builds a systemd-stub addon with ukify and
// attests it under the names systemd-stub(7) loads addons from: foo.efi.extra.d/ inside
// EFI/Linux/, and loader/addons/ with a kernel word in the name. An addon has no .linux
// section, so it must get the plain EFI rule, not the UKI rule. It needs ukify and the
// addon stub for this architecture, so it runs on Linux hosts that have both and is skipped
// elsewhere.
func TestGenerateSLSAProvenance_Positive_UkifyAddon(t *testing.T) {
	if _, err := exec.LookPath("ukify"); err != nil {
		t.Skipf("ukify is not installed (it ships with systemd on Linux): %v", err)
	}
	stub := systemdStub(t, "addon")
	addon := filepath.Join(t.TempDir(), "quiet.addon.efi")
	if _, err := util.RunCommand(t.Context(), "", "ukify", "build", "--stub="+stub, "--cmdline=quiet", "--output="+addon); err != nil {
		t.Fatalf("ukify build addon: %v", err)
	}
	for _, name := range []string{"EFI/Linux/test.efi.extra.d/quiet.addon.efi", "loader/addons/kernel-cmdline.addon.efi"} {
		_, verdicts, err := GenerateSLSAProvenance(t.Context(), ProvenanceRequest{ArtifactPath: addon, ArtifactName: name, BuilderID: "b"})
		if err != nil || verdicts[0].Status != ContentVerified || verdicts[0].Format != efiFormat.name {
			t.Errorf("an addon ukify built, named %s, is not verified as an EFI image: %+v, %v", name, verdicts, err)
		}
	}
}

// systemdStub returns systemd's kind stub for the running architecture, "linux" for a UKI
// or "addon" for an addon, at the path ukify defaults to, or skips the test: some
// distributions package the stubs apart from ukify.
func systemdStub(t *testing.T, kind string) string {
	t.Helper()
	arch, ok := map[string]string{"amd64": "x64", "arm64": "aa64", "386": "ia32", "riscv64": "riscv64", "loong64": "loongarch64"}[runtime.GOARCH]
	if !ok {
		t.Skipf("systemd ships no %s stub for %s", kind, runtime.GOARCH)
	}
	stub := filepath.Join("/usr/lib/systemd/boot/efi", kind+arch+".efi.stub")
	if _, err := os.Stat(stub); err != nil {
		t.Skipf("no systemd %s stub (systemd-boot not installed): %v", kind, err)
	}
	return stub
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
