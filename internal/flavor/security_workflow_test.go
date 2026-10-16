// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package flavor_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/gomanifest"
	"github.com/cordanaLLM/praetor/internal/semver"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/templates"
)

const (
	securityWorkflowPath = ".github/workflows/security.yml"
	securityPriorDir     = "testdata/security-prior/go"
	securityToolMod      = `module example.com/tools

go 1.27

tool (
	github.com/securego/gosec/v2/cmd/gosec
	golang.org/x/vuln/cmd/govulncheck
)

require (
	github.com/securego/gosec/v2 v2.29.0
	golang.org/x/tools v0.50.0
	golang.org/x/vuln v1.8.0
)
`
)

func securityTemplate(t *testing.T) flavor.TemplateItem {
	t.Helper()
	flv, err := flavor.Get("go-service")
	if err != nil {
		t.Fatal(err)
	}
	for _, tmpl := range flv.RequiredTemplates() {
		if tmpl.Path == securityWorkflowPath {
			return tmpl
		}
	}
	t.Fatal("go-service has no Security Scan template")
	return flavor.TemplateItem{}
}

func renderedSecurityWorkflow(t *testing.T, declaredScanners bool) string {
	t.Helper()
	tmpl := securityTemplate(t)
	ctx := templates.Context{}
	ctx.GoSecurity.DeclaredScanners = declaredScanners
	body, err := templates.RenderFile(tmpl.Source, ctx)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func securityStep(t *testing.T, body, name string) ghworkflow.Step {
	t.Helper()
	spec, err := ghworkflow.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	job, ok := spec.Jobs["vulncheck"]
	if !ok {
		t.Fatal("Security Scan has no vulncheck job")
	}
	for _, step := range job.Steps {
		if step.Name == name {
			return step
		}
	}
	t.Fatalf("Security Scan has no %q step", name)
	return ghworkflow.Step{}
}

func TestSecurityWorkflow_Positive_HostedGateAndPinnedInstalls(t *testing.T) {
	body := renderedSecurityWorkflow(t, false)
	spec, err := ghworkflow.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	pull := util.YAMLMappingValue(&spec.On, "pull_request")
	types := ghworkflow.StringList(util.YAMLMappingValue(pull, "types"))
	wantTypes := []string{"opened", "synchronize", "reopened", ghworkflow.HostedGateReadyType}
	if !slices.Equal(types, wantTypes) {
		t.Errorf("pull_request types = %v, want %v", types, wantTypes)
	}
	if util.YAMLMappingValue(&spec.On, "schedule") == nil {
		t.Error("Security Scan lost its schedule trigger")
	}
	if strings.Contains(body, "@latest") {
		t.Error("Security Scan installs an unpinned latest scanner")
	}
	fallback := securityStep(t, body, "Install Tools").Run
	for _, want := range []string{
		`d="$RUNNER_TEMP/scanners"`,
		`mkdir -p "$d"`,
		`cd "$d"`,
		"go mod init scanners",
		"go get golang.org/x/vuln@" + templates.GoSecurityGovulnVersion,
		"go get github.com/securego/gosec/v2@" + templates.GoSecurityGosecVersion,
		"go get golang.org/x/tools@" + templates.GoSecurityXToolsVersion,
		"go mod download all",
		"go install golang.org/x/vuln/cmd/govulncheck",
		"go install github.com/securego/gosec/v2/cmd/gosec",
	} {
		if !strings.Contains(fallback, want) {
			t.Errorf("fallback install lacks %q:\n%s", want, fallback)
		}
	}
	toolModule := strings.TrimSpace(securityStep(t, renderedSecurityWorkflow(t, true), "Install Tools").Run)
	wantToolModule := "m=tools/go/go.mod\n" +
		"go install -modfile=\"$m\" golang.org/x/vuln/cmd/govulncheck\n" +
		"go install -modfile=\"$m\" github.com/securego/gosec/v2/cmd/gosec"
	if toolModule != wantToolModule {
		t.Errorf("tools-module install = %q, want %q", toolModule, wantToolModule)
	}
}

func TestSecurityWorkflow_Boundary_PushScheduleAndDraftGuardStay(t *testing.T) {
	body := renderedSecurityWorkflow(t, false)
	spec, err := ghworkflow.Parse([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	push := util.YAMLMappingValue(&spec.On, "push")
	branches := ghworkflow.StringList(util.YAMLMappingValue(push, "branches"))
	if !slices.Equal(branches, []string{ghworkflow.HostedGateDefaultBranch}) {
		t.Errorf("push branches = %v", branches)
	}
	wantSchedule := "  schedule:\n    - cron: '" + ghworkflow.HostedGateScheduleCron + "'\n"
	if !strings.Contains(body, wantSchedule) {
		t.Errorf("Security Scan lost schedule %q", wantSchedule)
	}
	steps := spec.Jobs["vulncheck"].Steps
	if len(steps) < 2 || ghworkflow.DraftStepFault(&steps[0]) != nil {
		t.Fatalf("Security Scan does not start with the hosted draft guard: %+v", steps)
	}
	for i := 1; i < len(steps) && i < ghworkflow.MaxStepsPerJob; i++ {
		condition, closed := ghworkflow.UnwrapExpression(steps[i].If)
		if !closed || condition != ghworkflow.HostedGateNotDraft {
			t.Errorf("step %d (%s) runs on a draft: %q", i+1, steps[i].Name, steps[i].If)
		}
	}
}

func TestSecurityWorkflowPriorTextsAreRecorded(t *testing.T) {
	tmpl := securityTemplate(t)
	fixtures := readPriorFixtures(t, securityPriorDir)
	assertPriorFixturesRecorded(t, tmpl.Prior, fixtures, securityPriorDir)
	for _, declared := range []bool{false, true} {
		current := renderedSecurityWorkflow(t, declared)
		if _, known, _ := util.LookupCanonicalText([]byte(current), tmpl.Prior); known {
			t.Errorf("current Security Scan rendering (declared scanners %t) is recorded as an earlier text", declared)
		}
	}
}

func securityRepo(t *testing.T) string {
	t.Helper()
	return repoWithFiles(t, map[string]string{
		"go.mod":          "module example.com/service\n\ngo 1.27\n",
		"cmd/service.go":  "package main\n\nfunc main() {}\n",
		"tools/go/go.mod": securityToolMod,
	})
}

func TestSecurityWorkflow_Positive_UneditedPriorIsRefreshed(t *testing.T) {
	prior := readPriorFixtures(t, securityPriorDir)["1371.yml"]
	for _, declared := range []bool{false, true} {
		t.Run(map[bool]string{false: "fallback", true: "tools-module"}[declared], func(t *testing.T) {
			repo := securityRepo(t)
			if !declared {
				if err := os.RemoveAll(filepath.Join(repo, "tools")); err != nil {
					t.Fatal(err)
				}
			}
			workflow := filepath.Join(repo, filepath.FromSlash(securityWorkflowPath))
			if err := os.MkdirAll(filepath.Dir(workflow), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(workflow, []byte(prior), 0o600); err != nil {
				t.Fatal(err)
			}
			report, err := flavor.ApplyFlavorWith(t.Context(), repo, "go-service", flavor.ApplyOptions{TemplatesOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(workflow)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != renderedSecurityWorkflow(t, declared) || !slices.Contains(report.RefreshedTemplates, securityWorkflowPath) {
				t.Fatalf("unedited prior was not refreshed: refreshed %v\n%s", report.RefreshedTemplates, got)
			}
		})
	}
}

func TestSecurityWorkflow_Negative_EditedPriorIsKeptAndReported(t *testing.T) {
	fixtures := readPriorFixtures(t, securityPriorDir)
	prior := fixtures["1371.yml"]
	edited := strings.Replace(prior, "name: Security Scan", "name: Custom Security Scan", 1)
	repo := securityRepo(t)
	workflow := filepath.Join(repo, filepath.FromSlash(securityWorkflowPath))
	if err := os.MkdirAll(filepath.Dir(workflow), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workflow, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := flavor.ApplyFlavorWith(t.Context(), repo, "go-service", flavor.ApplyOptions{TemplatesOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(workflow)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != edited || !slices.Contains(report.SkippedTemplates, securityWorkflowPath) {
		t.Fatalf("edited workflow changed or was not reported skipped: skipped %v\n%s", report.SkippedTemplates, got)
	}
}

func TestSecurityWorkflow_Positive_ApplySelectsBothManagedRenderings(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"no tools module": nil,
		"incomplete tools module": {
			"tools/go/go.mod": "module example.com/tools\n\nrequire golang.org/x/vuln v1.8.0\n",
		},
		"declared scanners": {"tools/go/go.mod": securityToolMod},
	} {
		t.Run(name, func(t *testing.T) {
			repoFiles := map[string]string{
				"go.mod":         "module example.com/service\n\ngo 1.27\n",
				"cmd/service.go": "package main\n\nfunc main() {}\n",
			}
			for path, content := range files {
				repoFiles[path] = content
			}
			repo := repoWithFiles(t, repoFiles)
			report, err := flavor.ApplyFlavorWith(t.Context(), repo, "go-service", flavor.ApplyOptions{TemplatesOnly: true})
			if err != nil || len(report.UnmetTemplates) != 0 {
				t.Fatalf("apply: %v, unmet %v", err, report.UnmetTemplates)
			}
			body, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(securityWorkflowPath)))
			if err != nil {
				t.Fatal(err)
			}
			declared := name == "declared scanners"
			if string(body) != renderedSecurityWorkflow(t, declared) {
				t.Errorf("rendered wrong install for %s:\n%s", name, body)
			}
			if !flavor.TemplateSatisfied(repo, securityTemplate(t)) {
				t.Errorf("flavor audit rejects the managed %s rendering", name)
			}
		})
	}
}

func TestGoSecurityVersionsMatchToolsModule(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", gomanifest.ToolModuleFile))
	if err != nil {
		t.Fatal(err)
	}
	versions := map[string]string{}
	for _, requirement := range gomanifest.ParseManifest(data).Requires {
		versions[requirement.Path] = requirement.Version
	}
	for module, want := range map[string]string{
		"golang.org/x/vuln":            templates.GoSecurityGovulnVersion,
		"github.com/securego/gosec/v2": templates.GoSecurityGosecVersion,
		"golang.org/x/tools":           templates.GoSecurityXToolsVersion,
	} {
		if got := versions[module]; got != want {
			t.Errorf("%s in tools/go/go.mod = %q, fallback constant = %q; update the templates.GoSecurity*Version constants when Renovate updates tools/go/go.mod", module, got, want)
		}
	}
}

func TestSecurityWorkflow_Boundary_Go1272FallbackScanners(t *testing.T) {
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("Go toolchain absent: %v", err)
	}
	version, err := exec.Command(goBinary, "env", "GOVERSION").Output()
	if err != nil || strings.TrimSpace(string(version)) != "go1.27.2" {
		t.Skipf("Go 1.27.2 absent: GOVERSION=%q, error=%v", strings.TrimSpace(string(version)), err)
	}
	body := renderedSecurityWorkflow(t, false)
	install := strings.TrimSpace(securityStep(t, body, "Install Tools").Run)
	if strings.Contains(install, "@latest") || !strings.Contains(install, "golang.org/x/tools@"+templates.GoSecurityXToolsVersion) {
		t.Fatalf("workflow does not render the pinned fallback install: %q", install)
	}
	dir := securityScanFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	binDir := filepath.Join(dir, "bin")
	output, err := runSecurityCommand(ctx, dir, binDir, "bash", "-c", "set -euo pipefail\n"+install)
	if err != nil {
		if isNetworkFailure(output) {
			t.Skipf("scanner modules unavailable from the network: %v\n%s", err, output)
		}
		t.Fatalf("install scanners: %v\n%s", err, output)
	}
	assertCleanScanners(t, ctx, dir, binDir)
	assertGovulncheckFindsGO20221059(t, ctx, goBinary, binDir)
	assertGosecFindsG101(t, ctx, binDir)
	assertBareGosecFailsBelowFloor(t, ctx, dir)
}

// xtoolsMeetsFloor reports whether a golang.org/x/tools version reads go1.27.2 export data.
func xtoolsMeetsFloor(t *testing.T, version string) bool {
	t.Helper()
	got, okGot := semver.Parse(version)
	floor, okFloor := semver.Parse(templates.GoSecurityXToolsFloor)
	if !okGot || !okFloor {
		t.Fatalf("unparsable x/tools version %q or floor %q", version, templates.GoSecurityXToolsFloor)
	}
	return semver.Compare(got, floor) >= 0
}

// TestGoSecurityXToolsVersionMeetsTheExportDataFloor: the pinned golang.org/x/tools is at or above
// the floor, and the one gosec install guide names the tools module and both pins, never @latest.
func TestGoSecurityXToolsVersionMeetsTheExportDataFloor(t *testing.T) {
	for version, want := range map[string]bool{"v0.49.0": false, templates.GoSecurityXToolsFloor: true, "v0.52.0": true} {
		if got := xtoolsMeetsFloor(t, version); got != want {
			t.Errorf("x/tools %s meets the floor %s = %v, want %v", version, templates.GoSecurityXToolsFloor, got, want)
		}
	}
	if !xtoolsMeetsFloor(t, templates.GoSecurityXToolsVersion) {
		t.Errorf("templates.GoSecurityXToolsVersion %s is below the floor %s: gosec cannot read go1.27.2 export data",
			templates.GoSecurityXToolsVersion, templates.GoSecurityXToolsFloor)
	}
	guide := flavor.GosecInstallGuide
	for _, want := range []string{"-modfile=" + gomanifest.ToolModuleFile, templates.GoSecurityGosecVersion, "golang.org/x/tools " + templates.GoSecurityXToolsVersion} {
		if !strings.Contains(guide, want) {
			t.Errorf("gosec install guide lacks %q: %s", want, guide)
		}
	}
	if strings.Contains(guide, "@latest") {
		t.Errorf("gosec install guide installs @latest: %s", guide)
	}
}

// assertBareGosecFailsBelowFloor is the mutation for the x/tools pin: gosec built the unpinned way
// (go install at the pinned gosec version, no module raising golang.org/x/tools) must fail on the
// clean fixture while its x/tools is below the floor, as plurano/pertica main did on 2026-10-16.
func assertBareGosecFailsBelowFloor(t *testing.T, ctx context.Context, dir string) {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "bin")
	output, err := runSecurityCommand(ctx, dir, bare, "go", "install", "github.com/securego/gosec/v2/cmd/gosec@"+templates.GoSecurityGosecVersion)
	if err != nil {
		if isNetworkFailure(output) {
			t.Skipf("gosec module unavailable from the network: %v\n%s", err, output)
		}
		t.Fatalf("bare gosec install: %v\n%s", err, output)
	}
	gosec := filepath.Join(bare, "gosec")
	info, err := runSecurityCommand(ctx, dir, bare, "go", "version", "-m", gosec)
	if err != nil {
		t.Fatalf("go version -m gosec: %v\n%s", err, info)
	}
	version := ""
	for _, line := range strings.Split(info, "\n") {
		if fields := strings.Fields(line); len(fields) >= 3 && fields[0] == "dep" && fields[1] == "golang.org/x/tools" {
			version = fields[2]
		}
	}
	if version == "" || xtoolsMeetsFloor(t, version) {
		t.Logf("bare gosec %s builds golang.org/x/tools %q, not below the floor: the pin no longer carries the scan", templates.GoSecurityGosecVersion, version)
		return
	}
	output, err = runSecurityCommand(ctx, dir, bare, gosec, "-conf", ".gosec.json", "./...")
	if err == nil || !strings.Contains(output, "export data version") {
		t.Fatalf("gosec with golang.org/x/tools %s (floor %s) loaded go1.27.2 export data: %v\n%s", version, templates.GoSecurityXToolsFloor, err, output)
	}
}

func securityScanFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/securityboundary\n\ngo 1.27\n",
		"clean.go": "package securityboundary\n\nimport \"fmt\"\n\n" +
			"func Sum(a, b int) string { return fmt.Sprint(a + b) }\n",
	}
	config, err := templates.RenderFile("go/.gosec.json.tmpl", templates.Context{})
	if err != nil {
		t.Fatal(err)
	}
	files[".gosec.json"] = config
	writeSecurityFixture(t, dir, files)
	return dir
}

func writeSecurityFixture(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func assertCleanScanners(t *testing.T, ctx context.Context, dir, binDir string) {
	t.Helper()
	output, err := runSecurityCommand(ctx, dir, binDir, filepath.Join(binDir, "govulncheck"), "./...")
	if err != nil {
		t.Fatalf("govulncheck clean fixture: %v\n%s", err, output)
	}
	output, err = runSecurityCommand(ctx, dir, binDir, filepath.Join(binDir, "gosec"), "-conf", ".gosec.json", "./...")
	if strings.Contains(output, "type errors") || err != nil {
		t.Fatalf("gosec clean fixture: %v\n%s", err, output)
	}
}

func assertGovulncheckFindsGO20221059(t *testing.T, ctx context.Context, goBinary, binDir string) {
	t.Helper()
	dir := t.TempDir()
	writeSecurityFixture(t, dir, map[string]string{
		"go.mod":  "module example.com/vulnerable\n\ngo 1.27\n\nrequire golang.org/x/text v0.3.7\n",
		"main.go": "package main\n\nimport \"golang.org/x/text/language\"\n\nfunc main() { _, _, _ = language.ParseAcceptLanguage(\"en-US\") }\n",
	})
	output, err := runSecurityCommand(ctx, dir, binDir, goBinary, "mod", "download", "golang.org/x/text")
	if err != nil && isNetworkFailure(output) {
		t.Skipf("GO-2022-1059 fixture dependency unavailable from the network: %v\n%s", err, output)
	}
	if err != nil {
		t.Fatalf("download GO-2022-1059 fixture dependency: %v\n%s", err, output)
	}
	database := localVulnerabilityDatabase(t)
	output, err = runSecurityCommand(ctx, dir, binDir, filepath.Join(binDir, "govulncheck"), "-db", database, "./...")
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 3 || !strings.Contains(output, "GO-2022-1059") {
		t.Fatalf("govulncheck planted GO-2022-1059 exit = %v, want 3 and advisory output\n%s", err, output)
	}
}

func localVulnerabilityDatabase(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "govuln", "testdata", "vulndb"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.ToSlash(abs)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path // a Windows drive path, D:/...: file:///D:/..., not a host named D:
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func assertGosecFindsG101(t *testing.T, ctx context.Context, binDir string) {
	t.Helper()
	dir := t.TempDir()
	config, err := templates.RenderFile("go/.gosec.json.tmpl", templates.Context{})
	if err != nil {
		t.Fatal(err)
	}
	writeSecurityFixture(t, dir, map[string]string{
		"go.mod":        "module example.com/insecure\n\ngo 1.27\n",
		"credential.go": "package insecure\n\nconst password = \"f62e5bcda4fae4f82370da0c6f20697b8f8447ef\"\n",
		".gosec.json":   config,
	})
	output, err := runSecurityCommand(ctx, dir, binDir, filepath.Join(binDir, "gosec"), "-conf", ".gosec.json", "./...")
	if err == nil || !strings.Contains(output, "G101") {
		t.Fatalf("gosec accepted planted G101 credential: %v\n%s", err, output)
	}
}

func runSecurityCommand(ctx context.Context, dir, binDir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOBIN="+binDir, "GOFLAGS=-p=10", "GOTOOLCHAIN=local", "GOWORK=off", "RUNNER_TEMP="+filepath.Join(dir, "runner"))
	output, err := cmd.CombinedOutput()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return string(output), ctx.Err()
	}
	return string(output), err
}

func isNetworkFailure(output string) bool {
	for _, marker := range []string{"network is unreachable", "no such host", "dial tcp", "TLS handshake timeout", "i/o timeout", "proxyconnect tcp"} {
		if strings.Contains(output, marker) {
			return true
		}
	}
	return false
}
