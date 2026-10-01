package util

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// gitForWindowsLFSFilter is the driver block a stock Git for Windows install defines in its
// system gitconfig and `git lfs install` writes into the global one, so almost every Windows
// workstation, and every other one that ran `git lfs install`, defines it whether or not a
// repository uses it (#640).
var gitForWindowsLFSFilter = [][2]string{
	{"filter.lfs.clean", "git-lfs clean -- %f"},
	{"filter.lfs.smudge", "git-lfs smudge -- %f"},
	{"filter.lfs.process", "git-lfs filter-process"},
	{"filter.lfs.required", "true"},
}

// configureFilters writes each key and value with git config, into file when it is set and
// into dir's repository configuration otherwise, so git escapes the values itself.
func configureFilters(t *testing.T, dir, file string, entries [][2]string) {
	t.Helper()
	for _, entry := range entries {
		args := []string{"config"}
		if file != "" {
			args = append(args, "--file", file)
		}
		statusGit(t, dir, append(args, entry[0], entry[1])...)
	}
}

// markerFilter is a driver whose every command creates marker, so a test can tell whether git
// ran it. The path is quoted for the shell git runs filter commands through, with forward
// slashes, which Git for Windows' shell reads as well.
func markerFilter(driver, marker string) [][2]string {
	command := "touch '" + filepath.ToSlash(marker) + "'"
	return [][2]string{
		{"filter." + driver + ".clean", command},
		{"filter." + driver + ".smudge", command},
		{"filter." + driver + ".process", command},
		{"filter." + driver + ".required", "true"},
	}
}

func assertNotExecuted(t *testing.T, marker string) {
	t.Helper()
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a filter command ran during the probe: %v", err)
	}
}

// TestRefuseGitStatusFilters_Positive_UnselectedDriverPasses: the driver a stock Git for Windows
// install defines, configured where the probe does read it -- the repository's own
// configuration -- passes while no path's filter attribute selects it, and the status probe
// that follows answers (#640).
func TestRefuseGitStatusFilters_Positive_UnselectedDriverPasses(t *testing.T) {
	dir := statusRepo(t)
	configureFilters(t, dir, "", gitForWindowsLFSFilter)
	if err := RefuseGitStatusFilters(t.Context(), dir); err != nil {
		t.Fatalf("a configured driver no path selects was refused: %v", err)
	}
	writeStatusFile(t, dir, "a.txt", "edited\n")
	if got := mustChanges(t, dir); !slices.Equal(got, []string{" M a.txt"}) {
		t.Fatalf("changes beside an unselected driver = %q", got)
	}
}

// TestRefuseGitStatusFilters_Positive_OperatorDriversNeverReachTheProbe: drivers the system and
// global files define -- where Git for Windows and `git lfs install` put theirs -- are dropped
// with those files, so even a tracked path whose attribute names them is probed without running
// them.
func TestRefuseGitStatusFilters_Positive_OperatorDriversNeverReachTheProbe(t *testing.T) {
	dir := statusRepo(t)
	marker := filepath.Join(t.TempDir(), "filter-ran")
	system := filepath.Join(t.TempDir(), "gitconfig")
	global := filepath.Join(t.TempDir(), "global-gitconfig")
	configureFilters(t, dir, system, markerFilter("lfs", marker))
	configureFilters(t, dir, global, markerFilter("lfs", marker))
	writeStatusFile(t, dir, ".gitattributes", "*.txt filter=lfs\n")
	writeStatusFile(t, dir, "a.txt", "edited\n")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "")
	t.Setenv("GIT_CONFIG_SYSTEM", system)
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	if out, err := RunGit(t.Context(), dir, "config", "--get-all", "filter.lfs.clean"); err != nil || len(strings.Split(out, "\n")) != 2 {
		t.Fatalf("fixture: plain git must see the operator-level drivers: %q (%v)", out, err)
	}
	if err := RefuseGitStatusFilters(t.Context(), dir); err != nil {
		t.Fatalf("drivers outside the repository's configuration were refused: %v", err)
	}
	got := mustChanges(t, dir)
	if !slices.Equal(got, []string{" M a.txt", "?? .gitattributes"}) {
		t.Fatalf("changes = %q", got)
	}
	assertNotExecuted(t, marker)
}

// TestRefuseGitStatusFilters_Negative_SelectedDriverIsRefused: the same driver is refused by
// its clean and process keys once an attribute selects it for a tracked path, the smudge key a
// status never runs stays unnamed, and the refusal comes before any status could run it.
func TestRefuseGitStatusFilters_Negative_SelectedDriverIsRefused(t *testing.T) {
	dir := statusRepo(t)
	configureFilters(t, dir, "", gitForWindowsLFSFilter)
	writeStatusFile(t, dir, ".gitattributes", "*.txt filter=lfs\n")
	err := RefuseGitStatusFilters(t.Context(), dir)
	if !errors.Is(err, ErrGitStatusFilters) {
		t.Fatalf("a selected driver was not refused: %v", err)
	}
	for _, want := range []string{"filter.lfs.clean (filter=lfs on a.txt)", "filter.lfs.process (filter=lfs on a.txt)"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal %q does not name %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "smudge") {
		t.Fatalf("refusal names a key status never runs: %v", err)
	}

	probe := statusRepo(t)
	marker := filepath.Join(t.TempDir(), "filter-ran")
	configureFilters(t, probe, "", markerFilter("probe", marker))
	writeStatusFile(t, probe, ".gitattributes", "a.txt filter=probe\n")
	writeStatusFile(t, probe, "a.txt", "edited\n")
	if _, err := GitWorkingTreeChanges(t.Context(), probe, GitTreeProbeTimeout); !errors.Is(err, ErrGitStatusFilters) {
		t.Fatalf("a selected marker driver was not refused: %v", err)
	}
	assertNotExecuted(t, marker)
}

// TestRefuseGitStatusFilters_Boundary_WhatSelectsADriver: git resolves the attribute -- macros,
// nested and info attributes included -- and only tracked paths count, since status lists an
// untracked path without cleaning it. A bare or unset attribute and another driver's name select
// nothing; a driver name holding dots is matched whole.
func TestRefuseGitStatusFilters_Boundary_WhatSelectsADriver(t *testing.T) {
	lfsKeys := []string{"filter.lfs.clean", "filter.lfs.process"}
	cases := []struct {
		name    string
		files   map[string]string
		drivers [][2]string
		refused []string
	}{
		{"untracked path only", map[string]string{".gitattributes": "*.bin filter=lfs\n", "large.bin": "x\n"}, gitForWindowsLFSFilter, nil},
		{"another driver", map[string]string{".gitattributes": "*.txt filter=other\n"}, gitForWindowsLFSFilter, nil},
		{"bare and unset attribute", map[string]string{".gitattributes": "a.txt filter\nsub/b.txt -filter\n"}, gitForWindowsLFSFilter, nil},
		{"smudge only", map[string]string{".gitattributes": "*.txt filter=lfs\n"}, [][2]string{{"filter.lfs.smudge", "cat"}}, nil},
		{"macro", map[string]string{".gitattributes": "[attr]large filter=lfs\n*.txt large\n"}, gitForWindowsLFSFilter, lfsKeys},
		{"nested attributes", map[string]string{filepath.Join("sub", ".gitattributes"): "b.txt filter=lfs\n"}, gitForWindowsLFSFilter, lfsKeys},
		{"info attributes", map[string]string{filepath.Join(".git", "info", "attributes"): "sub/* filter=lfs\n"}, gitForWindowsLFSFilter, lfsKeys},
		{"dotted driver name", map[string]string{".gitattributes": "*.txt filter=a.b\n"}, [][2]string{{"filter.a.b.clean", "cat"}, {"filter.b.clean", "cat"}}, []string{"filter.a.b.clean"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := statusRepo(t)
			configureFilters(t, dir, "", tc.drivers)
			for rel, content := range tc.files {
				writeStatusFile(t, dir, rel, content)
			}
			assertRefusedKeys(t, RefuseGitStatusFilters(t.Context(), dir), tc.refused)
		})
	}
}

// TestRefuseGitStatusFilters_Boundary_ScopeOfTheScan: a probe from a subdirectory still sees a
// selection anywhere in the repository, and a repository with no tracked path has nothing a
// status could clean.
func TestRefuseGitStatusFilters_Boundary_ScopeOfTheScan(t *testing.T) {
	dir := statusRepo(t)
	configureFilters(t, dir, "", gitForWindowsLFSFilter)
	writeStatusFile(t, dir, ".gitattributes", "a.txt filter=lfs\n")
	err := RefuseGitStatusFilters(t.Context(), filepath.Join(dir, "sub"))
	if !errors.Is(err, ErrGitStatusFilters) || !strings.Contains(err.Error(), "on ../a.txt") {
		t.Fatalf("a subdirectory probe missed a top-level selection: %v", err)
	}

	empty := t.TempDir()
	statusGit(t, empty, "init", "-q")
	configureFilters(t, empty, "", gitForWindowsLFSFilter)
	writeStatusFile(t, empty, ".gitattributes", "* filter=lfs\n")
	writeStatusFile(t, empty, "new.txt", "n\n")
	if err := RefuseGitStatusFilters(t.Context(), empty); err != nil {
		t.Fatalf("a repository with no tracked path was refused: %v", err)
	}
}

// operatorGitFiles points git at a fresh system and global configuration and a fresh home, so
// a test controls every file an effective-configuration read sees. It returns the system and
// global configuration paths and the home's default global attributes file.
func operatorGitFiles(t *testing.T) (system, global, attributes string) {
	t.Helper()
	home := t.TempDir()
	system = filepath.Join(t.TempDir(), "gitconfig")
	global = filepath.Join(home, ".gitconfig")
	attributes = filepath.Join(home, ".config", "git", "attributes")
	for _, file := range []string{system, global} {
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "")
	t.Setenv("GIT_CONFIG_SYSTEM", system)
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	return system, global, attributes
}

// TestRefuseGitStatusFilters_Positive_EffectiveConfigPassesUnselectedOperatorDrivers: read as
// the caller's git commands read it, the stock filter.lfs block in the global configuration and
// a marker driver in the system one both pass while no tracked path selects them, and neither
// runs (#679). A nil option is ignored.
func TestRefuseGitStatusFilters_Positive_EffectiveConfigPassesUnselectedOperatorDrivers(t *testing.T) {
	dir := statusRepo(t)
	system, global, _ := operatorGitFiles(t)
	marker := filepath.Join(t.TempDir(), "filter-ran")
	configureFilters(t, dir, global, gitForWindowsLFSFilter)
	configureFilters(t, dir, system, markerFilter("unused", marker))
	writeStatusFile(t, dir, ".gitattributes", "*.txt text\n")
	if err := RefuseGitStatusFilters(t.Context(), dir, WithEffectiveGitConfig(), nil); err != nil {
		t.Fatalf("operator drivers no tracked path selects were refused: %v", err)
	}
	assertNotExecuted(t, marker)
}

// TestRefuseGitStatusFilters_Negative_EffectiveConfigRefusesSelectedOperatorDrivers: a driver
// the global or system configuration defines, or one the global attributes file selects --
// named by core.attributesFile or found at its default path -- passes the repository-only read,
// which never sees it, and is refused by the effective one, before any status could run it.
func TestRefuseGitStatusFilters_Negative_EffectiveConfigRefusesSelectedOperatorDrivers(t *testing.T) {
	cases := []struct {
		name       string
		inSystem   bool
		attributes string // "tree", "default" or "configured"
	}{
		{"global driver, tree attributes", false, "tree"},
		{"system driver, tree attributes", true, "tree"},
		{"global driver, default global attributes", false, "default"},
		{"global driver, configured global attributes", false, "configured"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := statusRepo(t)
			system, global, defaultAttributes := operatorGitFiles(t)
			marker := filepath.Join(t.TempDir(), "filter-ran")
			definedIn := global
			if tc.inSystem {
				definedIn = system
			}
			configureFilters(t, dir, definedIn, markerFilter("op", marker))
			selectOperatorDriver(t, dir, global, defaultAttributes, tc.attributes, "*.txt filter=op\n")
			if err := RefuseGitStatusFilters(t.Context(), dir); err != nil {
				t.Fatalf("the repository-only read saw an operator-level selection: %v", err)
			}
			err := RefuseGitStatusFilters(t.Context(), dir, WithEffectiveGitConfig())
			assertRefusedKeys(t, err, []string{"filter.op.clean", "filter.op.process"})
			if !strings.Contains(err.Error(), "(filter=op on a.txt)") {
				t.Fatalf("refusal %q does not name the selecting path", err)
			}
			assertNotExecuted(t, marker)
		})
	}
}

// selectOperatorDriver writes selection into the attributes file where names: the work tree's
// .gitattributes, the default global attributes file, or a file core.attributesFile names in
// the global configuration.
func selectOperatorDriver(t *testing.T, dir, global, defaultAttributes, where, selection string) {
	t.Helper()
	switch where {
	case "tree":
		writeStatusFile(t, dir, ".gitattributes", selection)
	case "default":
		writeStatusFile(t, filepath.Dir(defaultAttributes), filepath.Base(defaultAttributes), selection)
	default:
		configured := filepath.Join(t.TempDir(), "attributes")
		writeStatusFile(t, filepath.Dir(configured), filepath.Base(configured), selection)
		configureFilters(t, dir, global, [][2]string{{"core.attributesFile", filepath.ToSlash(configured)}})
	}
}

// TestRefuseGitStatusFilters_Boundary_EffectiveConfigFollowsTheCallerEnvironment: the effective
// read uses the caller's command environment, so the same selected global driver is refused
// under an environment that names the global file and passes under one that drops it; a smudge
// driver, which a status never runs, passes either way, and a read that cannot run is an error.
func TestRefuseGitStatusFilters_Boundary_EffectiveConfigFollowsTheCallerEnvironment(t *testing.T) {
	dir := statusRepo(t)
	_, global, _ := operatorGitFiles(t)
	configureFilters(t, dir, global, [][2]string{{"filter.op.clean", "cat"}, {"filter.side.smudge", "cat"}})
	writeStatusFile(t, dir, ".gitattributes", "a.txt filter=op\nsub/b.txt filter=side\n")
	// A test run from a git hook inherits GIT_DIR and friends; they are dropped, as the default
	// command environment drops them, so the read stays on dir.
	ambient := FilterEnvironment(os.Environ(), func(name string) bool {
		return isGitRepositoryVariable(name) || name == "GIT_CONFIG_GLOBAL"
	})
	named, err := WithCommandEnvironment(t.Context(), append(ambient, "GIT_CONFIG_GLOBAL="+global))
	if err != nil {
		t.Fatal(err)
	}
	assertRefusedKeys(t, RefuseGitStatusFilters(named, dir, WithEffectiveGitConfig()), []string{"filter.op.clean"})
	dropped, err := WithCommandEnvironment(t.Context(), append(ambient, "GIT_CONFIG_GLOBAL="+os.DevNull))
	if err != nil {
		t.Fatal(err)
	}
	assertRefusedKeys(t, RefuseGitStatusFilters(dropped, dir, WithEffectiveGitConfig()), nil)

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := RefuseGitStatusFilters(cancelled, dir, WithEffectiveGitConfig()); err == nil || errors.Is(err, ErrGitStatusFilters) {
		t.Fatalf("an effective read that could not run must be an error, not a verdict: %v", err)
	}
}

func assertRefusedKeys(t *testing.T, err error, refused []string) {
	t.Helper()
	if len(refused) == 0 {
		if err != nil {
			t.Fatalf("want no refusal, got %v", err)
		}
		return
	}
	if !errors.Is(err, ErrGitStatusFilters) {
		t.Fatalf("want a refusal naming %q, got %v", refused, err)
	}
	for _, key := range refused {
		if !strings.Contains(err.Error(), key+" (") {
			t.Fatalf("refusal %q does not name %s", err, key)
		}
	}
	if strings.Count(err.Error(), " (filter=") != len(refused) {
		t.Fatalf("refusal %q names keys beyond %q", err, refused)
	}
}

func TestFilterAttributeValues_3D(t *testing.T) {
	got, err := filterAttributeValues([]byte("a.txt\x00filter\x00lfs\x00b.txt\x00filter\x00lfs\x00c\x00filter\x00unspecified\x00"))
	if err != nil || got["lfs"] != "a.txt" || got["unspecified"] != "c" || len(got) != 2 {
		t.Fatalf("values = %v (%v)", got, err)
	}
	if got, err := filterAttributeValues(nil); err != nil || len(got) != 0 {
		t.Fatalf("empty answer = %v (%v)", got, err)
	}
	for _, malformed := range []string{"a.txt\x00filter\x00", "a.txt\x00filter\x00lfs", "a.txt\x00diff\x00lfs\x00"} {
		if _, err := filterAttributeValues([]byte(malformed)); err == nil {
			t.Fatalf("malformed answer %q accepted", malformed)
		}
	}
}

func TestFilterDriverName_Boundary(t *testing.T) {
	for key, want := range map[string]string{
		"filter.lfs.clean":        "lfs",
		"filter.a.b.process":      "a.b",
		"filter.my driver.clean":  "my driver",
		"filter..clean":           "",
		"filter.Upper.Case.clean": "Upper.Case",
	} {
		if got := filterDriverName(key); got != want {
			t.Fatalf("filterDriverName(%q) = %q, want %q", key, got, want)
		}
	}
}
