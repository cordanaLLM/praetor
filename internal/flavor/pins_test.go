package flavor_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
)

// goServiceFiles is a Go service: a module with a cmd/ entry point.
func goServiceFiles() map[string]string {
	return map[string]string{"go.mod": "module example.com/svc\n\ngo 1.27\n", "cmd/svc/main.go": "package main\n\nfunc main() {}\n"}
}

// manifestWithPins declares a profile and a flavors list.
func manifestWithPins(profile, pins string) string {
	return "version: 1\nrepository:\n  owner: fixture\n  name: fixture\nprofiles:\n  - " + profile + "\n" + pins
}

// TestResolve_Positive_AppServiceReachesGoService is #1103's measured case: a Go service under
// the app-service profile. Before the fix the profile's flavors were python-ml, frontend-svelte,
// typescript-node, mobile-flutter and jvm-service, and auto detection failed with
// ErrNoFlavorMatched.
func TestResolve_Positive_AppServiceReachesGoService(t *testing.T) {
	repo := declaringRepo(t, "app-service", goServiceFiles())
	if got, err := flavor.Resolve(repo); err != nil || got != "go-service" {
		t.Fatalf("Resolve = %q, %v; want go-service under app-service", got, err)
	}
	reports, err := flavor.AuditTargetsContext(t.Context(), repo)
	if err != nil || len(reports) != 1 || reports[0].Flavor != "go-service" {
		t.Fatalf("auto audit = %+v, %v; want one go-service report", reports, err)
	}
}

// TestResolve_Boundary_NativeFlavorOutranksSecondaryClaim: a Node stack under app-service keeps
// its own flavor although go-service precedes nothing it matches; go-service answers only when
// no flavor built for app-service does.
func TestResolve_Boundary_NativeFlavorOutranksSecondaryClaim(t *testing.T) {
	files := goServiceFiles()
	files["package.json"] = `{"name": "web"}`
	if got, err := flavor.Resolve(declaringRepo(t, "app-service", files)); err != nil || got != "typescript-node" {
		t.Fatalf("Resolve = %q, %v; want typescript-node ahead of the secondary go-service claim", got, err)
	}
}

// TestResolve_Negative_NoFlavorNamesThePin: a repository matching no flavor still fails, and
// the message names the setting that pins one.
func TestResolve_Negative_NoFlavorNamesThePin(t *testing.T) {
	repo := declaringRepo(t, "app-service", map[string]string{"README.md": "# nothing\n"})
	_, err := flavor.AuditTargetsContext(t.Context(), repo)
	if !errors.Is(err, flavor.ErrNoFlavorMatched) {
		t.Fatalf("err = %v; want ErrNoFlavorMatched", err)
	}
	if !strings.Contains(err.Error(), "flavors entry in .standards.yaml") {
		t.Errorf("the refusal must name the pin setting, got %q", err)
	}
	if strings.Contains(err.Error(), "--flavor") {
		t.Errorf("the refusal must not name a flag gate run lacks, got %q", err)
	}
}

// TestResolveTargets_Positive_PinReplacesDetection: a pinned flavor is used where detection
// would fail, and for the root pin an empty path and "." are the same.
func TestResolveTargets_Positive_PinReplacesDetection(t *testing.T) {
	repo := declaringRepo(t, "app-service", map[string]string{"README.md": "# nothing\n"})
	for _, pins := range []string{"flavors:\n  - name: go-service\n", "flavors:\n  - name: go-service\n    path: .\n"} {
		writeManifest(t, repo, manifestWithPins("app-service", pins))
		targets, err := flavor.ResolveTargets(repo)
		if err != nil || len(targets) != 1 || targets[0] != (flavor.Target{Flavor: "go-service", Path: "."}) {
			t.Fatalf("targets = %+v, %v for %q; want go-service at the root", targets, err, pins)
		}
	}
}

// TestAuditTargets_Boundary_PathScopedPinsAuditSeparately: two components, two flavors, each
// audited against its own directory only. The api/ report does not see web/'s files.
func TestAuditTargets_Boundary_PathScopedPinsAuditSeparately(t *testing.T) {
	repo := declaringRepo(t, "app-service", map[string]string{
		"api/go.mod": "module example.com/api\n", "api/cmd/api/main.go": "package main\n",
		"web/package.json": `{"name": "web"}`,
	})
	writeManifest(t, repo, manifestWithPins("app-service",
		"flavors:\n  - name: go-service\n    path: api\n  - name: typescript-node\n    path: web\n"))
	reports, err := flavor.AuditTargetsContext(t.Context(), repo)
	if err != nil || len(reports) != 2 {
		t.Fatalf("reports = %+v, %v; want two", reports, err)
	}
	want := []struct{ flavor, path string }{{"go-service", "api"}, {"typescript-node", "web"}}
	for i, w := range want {
		if reports[i].Flavor != w.flavor || reports[i].Path != w.path {
			t.Errorf("report %d = %s at %s; want %s at %s", i, reports[i].Flavor, reports[i].Path, w.flavor, w.path)
		}
		if got := filepath.Base(reports[i].RepoPath); got != w.path {
			t.Errorf("report %d audited %q; want the %s directory only", i, reports[i].RepoPath, w.path)
		}
	}
}

// TestResolveTargets_Negative_RefusesUnusablePins: an unknown flavor and a missing directory
// are refused, never skipped or replaced by detection.
func TestResolveTargets_Negative_RefusesUnusablePins(t *testing.T) {
	cases := map[string]string{
		"unknown flavor":    "flavors:\n  - name: no-such-flavor\n",
		"missing directory": "flavors:\n  - name: go-service\n    path: nowhere\n",
	}
	for name, pins := range cases {
		repo := declaringRepo(t, "app-service", goServiceFiles())
		writeManifest(t, repo, manifestWithPins("app-service", pins))
		if targets, err := flavor.ResolveTargets(repo); err == nil {
			t.Errorf("%s: resolved %+v; want a refusal", name, targets)
		}
	}
}

// TestResolveTargets_Negative_MalformedManifestIsNotIgnored: an unreadable manifest must not
// silently drop the pins and fall back to detection.
func TestResolveTargets_Negative_MalformedManifestIsNotIgnored(t *testing.T) {
	repo := repoWithFiles(t, goServiceFiles())
	writeManifest(t, repo, "version: 1\nflavors: [unclosed\n")
	if targets, err := flavor.ResolveTargets(repo); err == nil {
		t.Fatalf("resolved %+v from a malformed manifest; want an error", targets)
	}
}

// writeManifest replaces the repository's .standards.yaml.
func writeManifest(t *testing.T, repo, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, ".standards.yaml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}
