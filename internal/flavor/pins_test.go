package flavor_test

import (
	"context"
	"errors"
	"io/fs"
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

// conformingGoService writes a go-service repository that satisfies every template and setting.
// With a scope, the stack sits below it and the repository-level files stay at the root, which
// is where a workflow, the ruleset and the editor settings always live.
func conformingGoService(t *testing.T, scope string) string {
	t.Helper()
	flv, err := flavor.Get("go-service")
	if err != nil {
		t.Fatalf("go-service flavor: %v", err)
	}
	files := map[string]string{}
	place := func(rel, body string) {
		if scope != "" && !strings.HasPrefix(rel, ".github/") && !strings.HasPrefix(rel, ".vscode/") && rel != "lefthook.yml" {
			rel = scope + "/" + rel
		}
		files[rel] = body
	}
	for _, tmpl := range flv.RequiredTemplates() {
		place(tmpl.Path, conformingTemplateBody(t, tmpl))
	}
	for _, s := range flv.RequiredSettings() {
		place(s.Path, fixtureSetting)
	}
	prefix := ""
	if scope != "" {
		prefix = scope + "/"
	}
	files[prefix+"go.mod"] = "module example.com/svc\n\ngo 1.27\n"
	files[prefix+"cmd/svc/main.go"] = "package main\n\nfunc main() {}\n"
	files[".standards.yaml"] = manifestWithPins("app-service", "")
	return repoWithFiles(t, files)
}

// TestAuditTargets_Positive_ConformingScopedPinPasses: a conforming repository whose Go service
// sits in api/ passes. Before the fix the five repository-level files were required below api/
// and the audit measured 37.5% on a repository with nothing missing.
func TestAuditTargets_Positive_ConformingScopedPinPasses(t *testing.T) {
	repo := conformingGoService(t, "api")
	writeManifest(t, repo, manifestWithPins("app-service", "flavors:\n  - name: go-service\n    path: api\n"))
	reports, err := flavor.AuditTargetsContext(t.Context(), repo)
	if err != nil || len(reports) != 1 {
		t.Fatalf("reports = %+v, %v; want one", reports, err)
	}
	if r := reports[0]; !r.Passed || r.Score != 100 || len(r.MissingTemplates) != 0 || len(r.MissingSettings) != 0 {
		t.Fatalf("a conforming scoped repository must pass: score %.1f, missing %+v %+v", r.Score, r.MissingTemplates, r.MissingSettings)
	}
}

// TestAuditTargets_Negative_ScopedPinStillRequiresBothLevels: dropping a repository-level file
// from the root, or a stack file from the pinned directory, fails the audit and names the file.
func TestAuditTargets_Negative_ScopedPinStillRequiresBothLevels(t *testing.T) {
	for _, missing := range []string{".github/rulesets/main.json", ".github/workflows/ci.yml", "api/.golangci.yml", "api/Dockerfile"} {
		repo := conformingGoService(t, "api")
		writeManifest(t, repo, manifestWithPins("app-service", "flavors:\n  - name: go-service\n    path: api\n"))
		if err := os.Remove(filepath.Join(repo, filepath.FromSlash(missing))); err != nil {
			t.Fatalf("remove %s: %v", missing, err)
		}
		reports, err := flavor.AuditTargetsContext(t.Context(), repo)
		if err != nil || len(reports) != 1 {
			t.Fatalf("%s: reports = %+v, %v", missing, reports, err)
		}
		r := reports[0]
		var named []string
		for _, tmpl := range r.MissingTemplates {
			named = append(named, tmpl.Path)
		}
		for _, s := range r.MissingSettings {
			named = append(named, s.Path)
		}
		if r.Score == 100 || len(named) != 1 || !strings.HasSuffix(missing, named[0]) {
			t.Errorf("%s: score %.1f, missing %v; want exactly that file reported", missing, r.Score, named)
		}
	}
}

// TestAuditTargets_Positive_ConformingAutoAudit: without pins a conforming Go service passes the
// auto audit at the root (the spec's positive case for AuditTargetsContext).
func TestAuditTargets_Positive_ConformingAutoAudit(t *testing.T) {
	repo := conformingGoService(t, "")
	reports, err := flavor.AuditTargetsContext(t.Context(), repo)
	if err != nil || len(reports) != 1 || !reports[0].Passed || reports[0].Path != "." {
		t.Fatalf("auto audit = %+v, %v; want one passing report at the root", reports, err)
	}
}

// TestAuditTargets_Negative_NilAndCancelledContext: a nil context is refused, and a cancelled
// one stops the audit with the context's error, for a root target and a scoped one alike.
func TestAuditTargets_Negative_NilAndCancelledContext(t *testing.T) {
	var nilCtx context.Context
	if _, err := flavor.AuditTargetsContext(nilCtx, conformingGoService(t, "")); err == nil {
		t.Error("a nil context must be refused")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	scoped := conformingGoService(t, "api")
	writeManifest(t, scoped, manifestWithPins("app-service", "flavors:\n  - name: go-service\n    path: api\n"))
	for name, repo := range map[string]string{"root": conformingGoService(t, ""), "scoped": scoped} {
		if _, err := flavor.AuditTargetsContext(cancelled, repo); !errors.Is(err, context.Canceled) {
			t.Errorf("%s: err = %v; want context.Canceled", name, err)
		}
	}
}

// TestResolveTargets_Negative_RefusesUnsafePinPaths: a missing directory wraps the cause, a file
// is not a directory, and a symlink inside the repository pointing outside it is refused.
func TestResolveTargets_Negative_RefusesUnsafePinPaths(t *testing.T) {
	pin := "flavors:\n  - name: go-service\n    path: link\n"
	repo := declaringRepo(t, "app-service", map[string]string{"file": "x"})
	writeManifest(t, repo, manifestWithPins("app-service", "flavors:\n  - name: go-service\n    path: nowhere\n"))
	if _, err := flavor.ResolveTargets(repo); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing directory: err = %v; want the stat error wrapped (fs.ErrNotExist)", err)
	}
	writeManifest(t, repo, manifestWithPins("app-service", "flavors:\n  - name: go-service\n    path: file\n"))
	if _, err := flavor.ResolveTargets(repo); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("file pin: err = %v; want not a directory", err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(repo, "link")); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	writeManifest(t, repo, manifestWithPins("app-service", pin))
	if _, err := flavor.ResolveTargets(repo); err == nil || !strings.Contains(err.Error(), "outside the repository") {
		t.Errorf("symlink pin: err = %v; want a refusal naming the repository", err)
	}
	inside := declaringRepo(t, "app-service", map[string]string{"real/x": "x"})
	if err := os.Symlink(filepath.Join(inside, "real"), filepath.Join(inside, "link")); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	writeManifest(t, inside, manifestWithPins("app-service", pin))
	if targets, err := flavor.ResolveTargets(inside); err != nil || len(targets) != 1 {
		t.Errorf("a symlink to a directory inside the repository must resolve, got %+v, %v", targets, err)
	}
}

// TestResolveTargets_Boundary_PinNameIsTrimmedOnce: a name with surrounding spaces validates
// and resolves as the trimmed name, never as an unknown flavor.
func TestResolveTargets_Boundary_PinNameIsTrimmedOnce(t *testing.T) {
	repo := declaringRepo(t, "app-service", goServiceFiles())
	writeManifest(t, repo, manifestWithPins("app-service", "flavors:\n  - name: \" go-service \"\n"))
	targets, err := flavor.ResolveTargets(repo)
	if err != nil || len(targets) != 1 || targets[0].Flavor != "go-service" {
		t.Fatalf("targets = %+v, %v; want go-service", targets, err)
	}
}
