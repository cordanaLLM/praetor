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
	result, err := AuditDocumentationCoverage(t.Context(), root)
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
		if content, err := HarvestDocumentation(t.Context(), ref, true); err == nil || content != "" {
			t.Fatalf("unavailable documentation accepted: %q, %v", content, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := HarvestDocumentation(ctx, PackageRef{Name: "example.com/pkg", Kind: KindGoModule}, true); !errors.Is(err, context.Canceled) {
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
