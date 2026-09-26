package docdistill

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/nodemanifest"
)

func TestDeclaredDependenciesRejectsIncompleteSources(t *testing.T) {
	for _, fixture := range []struct{ name, body string }{
		{"package.json", "{"},
		{"go.mod", strings.Repeat("// filler\n", 5001)},
		{"go.mod", strings.Repeat("x", 70000)},
	} {
		t.Run(fmt.Sprintf("%s_%d_bytes", fixture.name, len(fixture.body)), func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, fixture.name), []byte(fixture.body), 0600); err != nil {
				t.Fatal(err)
			}
			if refs, err := ScanDeclaredDependencies(t.Context(), root, true); err == nil || len(refs) != 0 {
				t.Fatalf("invalid source accepted %d references: %v", len(refs), err)
			}
		})
	}
}

func TestAuditDocumentationCoverageRejectsInvalidRoot(t *testing.T) {
	for _, root := range []string{filepath.Join(t.TempDir(), "missing"), filepath.Join(t.TempDir(), "file")} {
		if strings.HasSuffix(root, "file") {
			if err := os.WriteFile(root, []byte("x"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := AuditDocumentationCoverage(t.Context(), root, DefaultDistillOptions()); err == nil {
			t.Fatalf("invalid root %q accepted", root)
		}
	}
}

func TestDeclaredDependenciesPreservesDirectAndTransitiveFlags(t *testing.T) {
	root := t.TempDir()
	body := "module example.com/app\nrequire (\nexample.com/direct v1.0.0\nexample.com/indirect v2.0.0 // indirect\n)\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	direct, err := ScanDeclaredDependencies(t.Context(), root, false)
	if err != nil || len(direct) != 1 || !direct[0].Direct {
		t.Fatalf("direct references=%v, %v", direct, err)
	}
	all, err := ScanDeclaredDependencies(t.Context(), root, true)
	if err != nil || len(all) != 2 || all[1].Direct {
		t.Fatalf("all references=%v, %v", all, err)
	}
}

func TestDeclaredDependenciesRejectsEscapingSymlink(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "package.json"), []byte(`{"dependencies":{"private":"1.0.0"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "package.json"), filepath.Join(root, "package.json")); err != nil {
		t.Fatal(err)
	}
	if refs, err := ScanDeclaredDependencies(t.Context(), root, true); err == nil || len(refs) != 0 {
		t.Fatalf("escaping source read: %v, %v", refs, err)
	}
}

func TestFailedHarvestDoesNotBecomeCoverage(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	t.Setenv("GOPATH", t.TempDir())
	t.Setenv("GOMODCACHE", t.TempDir())
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/app\nrequire example.com/missing v1.0.0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if cat, err := SyncRepositoryDocs(t.Context(), root, DistillOptions{OfflineOnly: true}); err == nil || cat != nil {
		t.Fatalf("placeholder catalog created: %v, %v", cat, err)
	}
	cat, err := LoadCatalog(root)
	if err != nil || len(cat.Packages) != 0 {
		t.Fatalf("failed harvest cached: %v, %v", cat, err)
	}
	result, err := AuditDocumentationCoverage(t.Context(), root, DefaultDistillOptions())
	if err != nil || result.Passed || result.Documented != 0 {
		t.Fatalf("failed harvest counted as coverage: %+v, %v", result, err)
	}
}

func TestHarvestRejectsInvalidAndCancelledRequests(t *testing.T) {
	for _, ref := range []PackageRef{
		{Name: "-exec", Kind: KindGoModule},
		{Name: "example.com/pkg", Kind: PackageKind("unsupported")},
		{Name: "actions/checkout", Version: "v4", Kind: KindGitHubAction},
	} {
		if content, err := HarvestDocumentation(t.Context(), t.TempDir(), ref, true); err == nil || content != "" {
			t.Fatalf("unavailable documentation accepted: %q, %v", content, err)
		}
	}
	if content, err := HarvestDocumentation(t.Context(), " ", PackageRef{Name: "example.com/pkg", Kind: KindGoModule}, true); err == nil || content != "" {
		t.Fatalf("harvest without a repository accepted: %q, %v", content, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := HarvestDocumentation(ctx, t.TempDir(), PackageRef{Name: "example.com/pkg", Kind: KindGoModule}, true); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestFetchDocumentationEnforcesCompleteResponseBoundary(t *testing.T) {
	for _, size := range []int{0, 256 * 1024, 256*1024 + 1} {
		t.Run(fmt.Sprintf("%d_bytes", size), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if _, err := io.WriteString(w, strings.Repeat("x", size)); err != nil {
					t.Errorf("write fixture: %v", err)
				}
			}))
			defer server.Close()
			content, err := fetchURLWithTimeout(t.Context(), server.URL)
			if size == 256*1024 {
				if err != nil || len(content) != size {
					t.Fatalf("exact boundary=%d bytes, %v", len(content), err)
				}
			} else if err == nil || content != "" {
				t.Fatalf("incomplete response accepted: %d bytes, %v", len(content), err)
			}
		})
	}
}

func TestWorkflowActionBoundsAndInputExtraction(t *testing.T) {
	if _, err := workflowPackageRefs(strings.Repeat("uses: actions/checkout@v4\n", 101), "fixture.yml"); err == nil {
		t.Fatal("truncated action list accepted")
	}
	inputs := extractAPISurface("inputs:\n  token:\n    required: true\noutputs:\n  report:\nruns:\n  using: node20\n", KindGitHubAction)
	if len(inputs) != 1 || inputs[0] != "token" {
		t.Fatalf("input extraction drift: %v", inputs)
	}
}

// writeNodeFile creates rel below root, making parent directories as needed.
func writeNodeFile(t *testing.T, root, rel, body string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func nodeRefNames(refs []PackageRef) map[string]bool {
	names := make(map[string]bool, len(refs))
	for _, ref := range refs {
		if ref.Kind == KindNodePackage {
			names[ref.Name] = true
		}
	}
	return names
}

// Regression for #96: a workspace repository's dependencies live in the member
// manifests, and its root manifest commonly declares only devDependencies. Both
// were invisible — the scanner read the root alone, and treated devDependencies
// as transitive — so the audit reported a confident coverage figure over none of
// the repository's npm packages.
func TestDeclaredDependenciesReadsWorkspaceMembersAndDevDependencies(t *testing.T) {
	root := t.TempDir()
	writeNodeFile(t, root, "package.json", `{"name":"root","devDependencies":{"turbo":"2.10.12"}}`)
	writeNodeFile(t, root, "pnpm-workspace.yaml", "packages:\n  - 'packages/*'\n")
	writeNodeFile(t, root, "packages/alpha/package.json", `{"name":"alpha","dependencies":{"zod":"4.6.1"}}`)
	writeNodeFile(t, root, "packages/beta/package.json", `{"name":"beta","dependencies":{"marked":"18.0.12"}}`)

	// Without the transitive flag: every declared npm package is still found,
	// because each is declared in a manifest this repository owns.
	refs, err := ScanDeclaredDependencies(t.Context(), root, false)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	names := nodeRefNames(refs)
	for _, want := range []string{"turbo", "zod", "marked"} {
		if !names[want] {
			t.Errorf("%s not found; got %v", want, names)
		}
	}
	for _, ref := range refs {
		if ref.Kind == KindNodePackage && !ref.Direct {
			t.Errorf("%s reported as indirect; every declared npm dependency is direct", ref.Name)
		}
	}
}

// Boundary: the manifest path is carried through, so a reference from a
// workspace member is attributable. It used to read "package.json" for every
// npm reference regardless of which manifest declared it.
func TestNodeReferencesCarryTheirManifestPath(t *testing.T) {
	root := t.TempDir()
	writeNodeFile(t, root, "package.json", `{"name":"root","workspaces":["packages/*"]}`)
	writeNodeFile(t, root, "packages/alpha/package.json", `{"name":"alpha","dependencies":{"zod":"4.6.1"}}`)

	refs, err := ScanDeclaredDependencies(t.Context(), root, false)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, ref := range refs {
		if ref.Name == "zod" {
			if ref.Manifest != "packages/alpha/package.json" {
				t.Errorf("manifest=%q, want packages/alpha/package.json", ref.Manifest)
			}
			return
		}
	}
	t.Fatalf("zod not found in %v", refs)
}

// Negative: confinement still holds for a workspace member, not just the root.
func TestWorkspaceMemberCannotEscapeTheRepository(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "package.json"), []byte(`{"dependencies":{"private":"1.0.0"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	writeNodeFile(t, root, "package.json", `{"name":"root","workspaces":["packages/*"]}`)
	if err := os.MkdirAll(filepath.Join(root, "packages", "alpha"), 0o750); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "packages", "alpha", "package.json")
	if err := os.Symlink(filepath.Join(outside, "package.json"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	refs, err := ScanDeclaredDependencies(t.Context(), root, false)
	if err == nil && nodeRefNames(refs)["private"] {
		t.Fatalf("read a manifest outside the repository: %v", refs)
	}
}

// Negative: a workspace past nodemanifest.MaxWorkspaceDirs fails the scan, as
// the go.mod, workflow-directory and action caps in this package do, instead of
// returning the references of a silently shortened manifest set.
func TestDeclaredDependenciesRejectsTruncatedWorkspace(t *testing.T) {
	root := t.TempDir()
	writeNodeFile(t, root, "package.json", `{"name":"root","workspaces":["packages/*"]}`)
	for i := range nodemanifest.MaxWorkspaceDirs {
		writeNodeFile(t, root, fmt.Sprintf("packages/p%04d/package.json", i), `{"name":"p"}`)
	}

	refs, err := ScanDeclaredDependencies(t.Context(), root, false)
	if !errors.Is(err, nodemanifest.ErrWorkspaceTruncated) {
		t.Fatalf("err = %v, refs = %d, want ErrWorkspaceTruncated", err, len(refs))
	}
}
