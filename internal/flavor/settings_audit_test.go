package flavor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
)

// conformingGoLibrary builds a repository carrying every go-library template and both of its
// settings, so a case can change exactly one thing and read the effect off the score.
func conformingGoLibrary(t *testing.T, settings map[string]string) string {
	t.Helper()
	files := map[string]string{
		".standards.yaml":            "version: 1\n",
		".standards.lock":            "version: 1\n",
		".golangci.yml":              "version: \"2\"\n",
		".github/workflows/ci.yml":   "name: ci\n",
		".workingdir/STATE.md":       "# state\n",
		".workingdir/BUGS.md":        "# bugs\n",
		".workingdir/QUESTIONS.md":   "# questions\n",
		"lefthook.yml":               "pre-commit:\n  commands:\n    gofmt:\n      run: gofmt -l .\n",
		".github/rulesets/main.json": "{\"name\": \"main\", \"enforcement\": \"active\"}\n",
	}
	for name, body := range settings {
		files[name] = body
	}
	dir := t.TempDir()
	for name, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatalf("mkdir for %s: %v", name, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// emptyPATH points tool resolution at a directory holding no binaries, so the toolchain
// terms of the report describe a known host rather than whichever one runs the suite.
func emptyPATH(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

// TestAuditFlavor_Positive_ConformingRepositoryScoresExactlyFull pins the score the audit
// used to leave unasserted. The repository carries all 7 go-library templates and both
// settings; no toolchain is installed. The previous formula scored this 9/12 = 75.0% and
// failed it, because three of its terms were the auditing machine's PATH.
func TestAuditFlavor_Positive_ConformingRepositoryScoresExactlyFull(t *testing.T) {
	emptyPATH(t)
	repo := conformingGoLibrary(t, nil)

	report, err := flavor.AuditFlavor(repo, "go-library")
	if err != nil {
		t.Fatalf("audit a conforming repository: %v", err)
	}
	if report.Score != 100.0 {
		t.Fatalf("expected exactly 100.0, got %v (settings %d/%d, templates %d/%d)",
			report.Score, report.SettingsValid, report.SettingsTotal,
			report.TemplatesPresent, report.TemplatesTotal)
	}
	if !report.Passed {
		t.Fatalf("a conforming repository must pass, got %+v", report)
	}
	if report.ToolchainsAvailable != 0 || len(report.MissingToolchains) != report.ToolchainsTotal {
		t.Fatalf("expected every toolchain reported missing on an empty PATH, got %d/%d",
			report.ToolchainsAvailable, report.ToolchainsTotal)
	}
}

// TestAuditFlavor_Negative_MalformedSettingIsInvalidAndNamed is the settings half: a
// lefthook.yml that is present but does not parse is not configuration, and the operator is
// told which file to fix.
func TestAuditFlavor_Negative_MalformedSettingIsInvalidAndNamed(t *testing.T) {
	emptyPATH(t)
	repo := conformingGoLibrary(t, map[string]string{"lefthook.yml": "pre-commit: [unterminated\n"})

	report, err := flavor.AuditFlavor(repo, "go-library")
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	want := 8.0 / 9.0 * 100.0
	if report.Score != want {
		t.Fatalf("expected exactly %v for 8 of 9 required items, got %v", want, report.Score)
	}
	if report.SettingsValid != 1 {
		t.Fatalf("expected 1 of 2 settings valid, got %d", report.SettingsValid)
	}
	named := false
	for _, s := range report.MissingSettings {
		if s.Path == "lefthook.yml" {
			named = true
		}
	}
	if !named {
		t.Fatalf("an invalid setting must be named in MissingSettings, got %+v", report.MissingSettings)
	}
}

// TestAuditFlavor_Boundary_EmptySettingFileConfiguresNothing covers the file that exists and
// says nothing: it parsed as YAML under any tolerant check, and it installs no hook.
func TestAuditFlavor_Boundary_EmptySettingFileConfiguresNothing(t *testing.T) {
	emptyPATH(t)
	repo := conformingGoLibrary(t, map[string]string{"lefthook.yml": ""})

	report, err := flavor.AuditFlavor(repo, "go-library")
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if report.SettingsValid != 1 {
		t.Fatalf("an empty lefthook.yml must not count as configuration, got %d/%d valid",
			report.SettingsValid, report.SettingsTotal)
	}
}

// TestAuditFlavor_Boundary_EmptyMappingConfiguresNothing covers the file that parses and
// still says nothing. A lefthook.yml holding `{}` installs no hook, so the report must not
// count it, and the operator must be told which file to fix.
func TestAuditFlavor_Boundary_EmptyMappingConfiguresNothing(t *testing.T) {
	emptyPATH(t)
	repo := conformingGoLibrary(t, map[string]string{"lefthook.yml": "{}\n"})

	report, err := flavor.AuditFlavor(repo, "go-library")
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if report.SettingsValid != 1 {
		t.Fatalf("a lefthook.yml holding {} must not count as configuration, got %d/%d valid",
			report.SettingsValid, report.SettingsTotal)
	}
	named := false
	for _, s := range report.MissingSettings {
		if s.Path == "lefthook.yml" {
			named = true
		}
	}
	if !named {
		t.Fatalf("an empty mapping must be named in MissingSettings, got %+v", report.MissingSettings)
	}
}

// TestAuditFlavor_Boundary_ToolchainsDoNotDecidePassOrFail runs the same repository against
// two hosts. Only the PATH differs, so the two reports must agree on Score and Passed.
func TestAuditFlavor_Boundary_ToolchainsDoNotDecidePassOrFail(t *testing.T) {
	repo := conformingGoLibrary(t, nil)

	emptyPATH(t)
	bare, err := flavor.AuditFlavor(repo, "go-library")
	if err != nil {
		t.Fatalf("audit with an empty PATH: %v", err)
	}

	tooled := t.TempDir()
	writeStubTool(t, tooled, "go")
	t.Setenv("PATH", tooled)
	equipped, err := flavor.AuditFlavor(repo, "go-library")
	if err != nil {
		t.Fatalf("audit with a populated PATH: %v", err)
	}

	if bare.Score != equipped.Score || bare.Passed != equipped.Passed {
		t.Fatalf("the host changed the verdict: %v/%v then %v/%v",
			bare.Score, bare.Passed, equipped.Score, equipped.Passed)
	}
	if equipped.ToolchainsAvailable <= bare.ToolchainsAvailable {
		t.Fatalf("the advisory count must still observe the host: %d then %d",
			bare.ToolchainsAvailable, equipped.ToolchainsAvailable)
	}
}

func TestSettingSatisfied_Positive(t *testing.T) {
	repo := conformingGoLibrary(t, nil)
	for _, path := range []string{".github/rulesets/main.json", "lefthook.yml"} {
		setting := settingsFor(t, "go-library", path)[0]
		if !flavor.SettingSatisfied(repo, setting) {
			t.Errorf("%s is present and well-formed, but was reported unsatisfied", path)
		}
	}
	// A setting with no validator is satisfied by presence, which is all that can be
	// claimed about a file with no checkable shape.
	free := flavor.SettingItem{Name: "free form", Path: ".standards.lock"}
	if !flavor.SettingSatisfied(repo, free) {
		t.Errorf("a setting without a validator must be satisfied by presence")
	}
}

func TestSettingSatisfied_Negative(t *testing.T) {
	repo := conformingGoLibrary(t, nil)
	jsonSetting := settingsFor(t, "go-library", ".github/rulesets/main.json")[0]
	yamlSetting := settingsFor(t, "go-library", "lefthook.yml")[0]

	absent := flavor.SettingItem{Name: "absent", Path: ".vscode/settings.json", Validator: jsonSetting.Validator}
	if flavor.SettingSatisfied(repo, absent) {
		t.Errorf("an absent setting must not be satisfied")
	}

	rewrite(t, repo, ".github/rulesets/main.json", "[\"a rule list is not a ruleset object\"]")
	if flavor.SettingSatisfied(repo, jsonSetting) {
		t.Errorf("a JSON array is not a settings object")
	}

	// Strict JSON, as internal/clientsetup already requires of client configuration:
	// comments are rejected rather than tolerated.
	rewrite(t, repo, ".github/rulesets/main.json", "{ // operator note\n\"name\": \"main\"}")
	if flavor.SettingSatisfied(repo, jsonSetting) {
		t.Errorf("JSON with comments must not be reported as valid JSON")
	}

	rewrite(t, repo, "lefthook.yml", "just a string")
	if flavor.SettingSatisfied(repo, yamlSetting) {
		t.Errorf("a YAML scalar configures nothing and must not be satisfied")
	}
}

func TestSettingSatisfied_Boundary(t *testing.T) {
	repo := conformingGoLibrary(t, nil)
	jsonSetting := settingsFor(t, "go-library", ".github/rulesets/main.json")[0]
	yamlSetting := settingsFor(t, "go-library", "lefthook.yml")[0]

	rewrite(t, repo, ".github/rulesets/main.json", "")
	if flavor.SettingSatisfied(repo, jsonSetting) {
		t.Errorf("an empty file is not a JSON object")
	}

	rewrite(t, repo, ".github/rulesets/main.json", "null")
	if flavor.SettingSatisfied(repo, jsonSetting) {
		t.Errorf("a JSON null is not a settings object")
	}

	// A container with no members parses and still configures nothing. The validators are
	// documented as requiring a non-empty mapping or object, and docs/guides/onboarding.md
	// ships that claim to operators; `{}` used to be reported valid by both of them.
	for _, empty := range []string{"{}", "{ }", "  {}  \n", "---\n{}\n"} {
		rewrite(t, repo, "lefthook.yml", empty)
		if flavor.SettingSatisfied(repo, yamlSetting) {
			t.Errorf("an empty YAML mapping (%q) installs no hook and must not be satisfied", empty)
		}
	}
	for _, empty := range []string{"{}", "{ }", "  {}  \n"} {
		rewrite(t, repo, ".github/rulesets/main.json", empty)
		if flavor.SettingSatisfied(repo, jsonSetting) {
			t.Errorf("an empty JSON object (%q) restricts nothing and must not be satisfied", empty)
		}
	}

	// One byte past the cap: valid YAML that no configuration file would ever be.
	oversized := "key: " + strings.Repeat("y", (1<<20)-4)
	rewrite(t, repo, "lefthook.yml", oversized)
	if len(oversized) <= 1<<20 {
		t.Fatalf("fixture must exceed the 1 MiB cap, got %d bytes", len(oversized))
	}
	if flavor.SettingSatisfied(repo, yamlSetting) {
		t.Errorf("a file past the size cap must not be reported valid; it was never read")
	}

	// A path that leaves the repository is refused rather than resolved.
	escaping := flavor.SettingItem{Name: "escape", Path: filepath.Join("..", "outside.json"), Validator: jsonSetting.Validator}
	if flavor.SettingSatisfied(repo, escaping) {
		t.Errorf("a setting path must not resolve outside the repository")
	}

	// A directory where a file is required reads as unsatisfied, not as a crash.
	if err := os.RemoveAll(filepath.Join(repo, "lefthook.yml")); err != nil {
		t.Fatalf("remove fixture: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "lefthook.yml"), 0o700); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}
	if flavor.SettingSatisfied(repo, yamlSetting) {
		t.Errorf("a directory is not a settings file")
	}
}

// settingsFor returns the registered flavor's setting items for one path, so the tests
// exercise the validators the catalog actually declares rather than a copy of them.
func settingsFor(t *testing.T, flavorName, path string) []flavor.SettingItem {
	t.Helper()
	flv, err := flavor.Get(flavorName)
	if err != nil {
		t.Fatalf("get flavor %s: %v", flavorName, err)
	}
	matched := make([]flavor.SettingItem, 0, 1)
	for _, s := range flv.RequiredSettings() {
		if s.Path == path {
			matched = append(matched, s)
		}
	}
	if len(matched) == 0 {
		t.Fatalf("flavor %s declares no setting at %s", flavorName, path)
	}
	return matched
}

func rewrite(t *testing.T, repo, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, filepath.FromSlash(name)), []byte(body), 0o600); err != nil {
		t.Fatalf("rewrite %s: %v", name, err)
	}
}

// writeStubTool places an executable named tool in dir. The content is irrelevant: only
// exec.LookPath resolution is under test, and nothing ever runs the file. On Windows the
// name carries a PATHEXT extension, which is how LookPath resolves a command there.
func writeStubTool(t *testing.T, dir, tool string) {
	t.Helper()
	name := tool
	body := "#!/bin/sh\nexit 0\n"
	if os.PathSeparator == '\\' {
		name += ".bat"
		body = "@echo off\r\n"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o700); err != nil {
		t.Fatalf("write stub %s: %v", name, err)
	}
}
