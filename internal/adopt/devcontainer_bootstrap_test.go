package adopt

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/devcontainer"
)

func adoptBootstrapSource(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	initTestGit(t, root)
	for name, content := range map[string]string{
		"go.mod": "module github.com/cordanaLLM/praetor\n\ngo 1.27\n", "go.sum": "", "LICENSE": "Synthetic test license\n",
		"cmd/standardsctl/main.go": "package main\nfunc main() {}\n",
	} {
		mustWrite(t, filepath.Join(root, name), content)
	}
	return root
}

func bootstrapAdoptSession(t *testing.T, source string, dryRun bool) *adoptSession {
	t.Helper()
	return &adoptSession{repoPath: t.TempDir(), repoName: "adopted/app", arch: "framework", opts: AdoptOptions{LockSourceRoot: source, DryRun: dryRun}, report: &AdoptReport{}}
}

func TestAdoptDevContainerBundleDryRunAndApply(t *testing.T) {
	source := adoptBootstrapSource(t)
	preview := bootstrapAdoptSession(t, source, true)
	if err := reconcileDevContainer(t.Context(), preview); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(preview.repoPath)
	if err != nil || len(entries) != 0 || len(preview.report.CreatedFiles) < 3 {
		t.Fatalf("dry run writes or omits companions: %v %#v", err, preview.report)
	}
	applied := bootstrapAdoptSession(t, source, false)
	if err := reconcileDevContainer(t.Context(), applied); err != nil {
		t.Fatal(err)
	}
	base, err := devcontainer.Synthesize(adoptionManifest(t, applied))
	if err != nil {
		t.Fatal(err)
	}
	if err := devcontainer.Verify(t.Context(), filepath.Join(applied.repoPath, devcontainerFile), base); err != nil {
		t.Fatal(err)
	}
	if strings.Join(preview.report.CreatedFiles, "\n") != strings.Join(applied.report.CreatedFiles, "\n") {
		t.Fatal("preview and applied companion sets differ")
	}
}

func TestAdoptDevContainerUnavailableAndCustomRemainExplicit(t *testing.T) {
	session := bootstrapAdoptSession(t, t.TempDir(), false)
	if err := reconcileDevContainer(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(session.report.Warnings, "\n"), "bootstrap unavailable") {
		t.Fatal("config-only source omitted unavailable bootstrap")
	}
	path := filepath.Join(session.repoPath, devcontainerFile)
	base, err := devcontainer.Synthesize(adoptionManifest(t, session))
	if err != nil {
		t.Fatal(err)
	}
	if err := devcontainer.Verify(t.Context(), path, base); !errors.Is(err, devcontainer.ErrBootstrapUnavailable) {
		t.Fatalf("unavailable adoption implied readiness: %v", err)
	}
	custom := "{\"image\":\"operator/custom:tag\"}\n"
	mustWrite(t, path, custom)
	session.opts.LockSourceRoot = filepath.Join(t.TempDir(), "missing")
	if err := reconcileDevContainer(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	if mustRead(t, path) != custom || !strings.Contains(strings.Join(session.report.Warnings, "\n"), "preserved") {
		t.Fatal("custom container was overwritten or certified")
	}
}

func TestAdoptDevContainerInvalidExplicitSourceDoesNotWrite(t *testing.T) {
	session := bootstrapAdoptSession(t, filepath.Join(t.TempDir(), "missing"), false)
	if err := reconcileDevContainer(t.Context(), session); err == nil {
		t.Fatal("invalid explicit source fell back")
	}
	entries, err := os.ReadDir(session.repoPath)
	if err != nil || len(entries) != 0 {
		t.Fatal("failed bootstrap wrote companion")
	}
}

func TestAdoptDevContainerUsesActualManifestIdentityAndProfiles(t *testing.T) {
	session := bootstrapAdoptSession(t, adoptBootstrapSource(t), false)
	manifestPath := filepath.Join(session.repoPath, manifestFile)
	mustWrite(t, manifestPath, "repository:\n  owner: actual-owner\n  name: actual-name\nprofiles: [native-gpu-systems]\nfacets: [security:high]\n")
	if err := reconcileDevContainer(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	manifest, err := config.LoadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := devcontainer.Synthesize(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := devcontainer.Verify(t.Context(), filepath.Join(session.repoPath, devcontainerFile), expected); err != nil {
		t.Fatalf("adoption generated a container rejected by its own manifest audit: %v", err)
	}
}

// TestAdoptDevContainerForceKeepsRecordedBaseImage pins #536 for adoption: a forced
// re-adoption keeps the adopter's recorded base image by the rule devcontainer generate
// applies and reports it; a first adoption takes the reviewed default without a note.
func TestAdoptDevContainerForceKeepsRecordedBaseImage(t *testing.T) {
	adopterBase := "registry.example/team/dev-toolchains@sha256:" + strings.Repeat("d", 64)
	fresh := bootstrapAdoptSession(t, adoptBootstrapSource(t), false)
	if err := reconcileDevContainer(t.Context(), fresh); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(fresh.report.Warnings, "\n"), "RECORDED IMAGE") {
		t.Fatalf("first adoption reported a recorded image: %v", fresh.report.Warnings)
	}
	session := bootstrapAdoptSession(t, adoptBootstrapSource(t), false)
	path := filepath.Join(session.repoPath, devcontainerFile)
	recorded, err := devcontainer.PrepareBundle(t.Context(), "adopted/app", nil, nil, devcontainer.BootstrapOptions{SourceRoot: adoptBootstrapSource(t), BaseImage: adopterBase})
	if err != nil {
		t.Fatal(err)
	}
	if err := devcontainer.WriteBundle(t.Context(), path, recorded, false); err != nil {
		t.Fatal(err)
	}
	session.opts.Force = true
	if err := reconcileDevContainer(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	dc, err := devcontainer.LoadDevContainer(t.Context(), path)
	if err != nil || dc.Customizations.Praetor.Bootstrap.BaseImage != adopterBase {
		t.Fatalf("forced re-adoption replaced the recorded base image: %v", err)
	}
	if !strings.Contains(strings.Join(session.report.Warnings, "\n"), "[RECORDED IMAGE KEPT] base image "+adopterBase) {
		t.Fatalf("kept image not reported: %v", session.report.Warnings)
	}
}
