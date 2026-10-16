package gating

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/govuln"
	"github.com/cordanaLLM/praetor/internal/util"
	apiassets "github.com/cordanaLLM/praetor/tools/apicompat"
)

func trackedModuleFixture(t *testing.T, nested bool) (string, *stageConfig, *[]recordedCommand) {
	t.Helper()
	repo := newHermeticGitRepo(t)
	seedGoModule(t, repo)
	files := []string{"go.mod"}
	if nested {
		if err := os.MkdirAll(filepath.Join(repo, "tooling"), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(repo, "tooling", "go.mod"), "module example.test/tooling\n")
		files = append(files, "tooling/go.mod")
	}
	writeLockfiles(t, repo)
	writeFile(t, filepath.Join(repo, GosecConfigFile), "{\"global\":{}}\n")
	cfg, recorded := newTestConfig(t, repo, false)
	cfg.moduleDiscovery = func(context.Context, string) ([]string, error) { return files, nil }
	return repo, cfg, recorded
}

func TestGoModules_Negative_DiscoveryIsRequired(t *testing.T) {
	cfg := &stageConfig{repoDir: t.TempDir()}
	_, err := cfg.goModules(t.Context())
	if !errors.Is(err, ErrModuleDiscoveryMissing) {
		t.Fatalf("missing discovery error = %v, want %v", err, ErrModuleDiscoveryMissing)
	}
}

func TestHoldsToolchainMarker_PositiveAndBoundary_UsesRealDiscovery(t *testing.T) {
	cases := []struct {
		name string
		file string
		want bool
	}{
		{name: "tooling module", file: "tooling/go.mod", want: true},
		{name: "testdata module", file: "testdata/go.mod", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newHermeticGitRepo(t)
			commitFile(t, repo, tc.file, "module example.test/fixture\n")
			cfg := &stageConfig{repoDir: repo, moduleDiscovery: apiassets.TrackedModuleFiles}
			got, err := holdsToolchainMarker(t.Context(), cfg)
			if err != nil || got != tc.want {
				t.Fatalf("holdsToolchainMarker = %t, %v; want %t", got, err, tc.want)
			}
		})
	}
}

func TestGoStages_Positive_RunEveryTrackedModule(t *testing.T) {
	repo, cfg, recorded := trackedModuleFixture(t, true)
	cfg.run = func(ctx context.Context, dir, name string, args ...string) (string, error) {
		*recorded = append(*recorded, recordedCommand{dir: dir, name: name, args: args})
		if name == govuln.DefaultScanner {
			return cleanGovulnStream, nil
		}
		if name == "go" && len(args) > 0 && args[0] == "list" {
			return dir + "\n", nil
		}
		return "", nil
	}
	if msg, err := runGoPrefetch(t.Context(), cfg); err != nil || !strings.Contains(msg, "go.mod") || !strings.Contains(msg, "tooling/go.mod") {
		t.Fatalf("prefetch = %q, %v", msg, err)
	}
	if msg, err := runGoSecurity(t.Context(), cfg); err != nil || !strings.Contains(msg, "tooling/go.mod") {
		t.Fatalf("security = %q, %v", msg, err)
	}
	if got := countCommands(*recorded, "go", "mod", "verify"); got != 2 {
		t.Errorf("go mod verify count = %d, want 2", got)
	}
	if got := countCommandDirs(*recorded, "gosec", filepath.Join(repo, "tooling")); got != 1 {
		t.Errorf("nested gosec directory count = %d, want 1", got)
	}
}

func TestGoStages_Positive_RootNamesItsModule(t *testing.T) {
	_, cfg, _ := trackedModuleFixture(t, false)
	if msg, err := runGoPrefetch(t.Context(), cfg); err != nil || !strings.Contains(msg, "go.mod") {
		t.Fatalf("root prefetch = %q, %v", msg, err)
	}
}

// Positive: a tracked module containing only go.mod is a successful, stated skip for both
// package-aware Go stages; the real go list is used against the committed fixture.
func TestGoStages_Positive_EmptyModuleSkipsSecurityAndRace(t *testing.T) {
	repo := newHermeticGitRepo(t)
	commitFile(t, repo, "go.mod", "module gating.test\n\ngo 1.27\n")
	commitFile(t, repo, GosecConfigFile, "{\"global\":{}}\n")
	cfg, _ := newTestConfig(t, repo, false)
	cfg.run = util.RunCommand
	if msg, err := runGoSecurity(t.Context(), cfg); err != nil || !strings.Contains(msg, NoPackagesNote) {
		t.Fatalf("empty security module = %q, %v", msg, err)
	}
	if msg, err := runGoTests(t.Context(), cfg); err != nil || !strings.Contains(msg, NoPackagesNote) {
		t.Fatalf("empty race module = %q, %v", msg, err)
	}
}

func TestGoStages_Negative_TooManyModulesRefused(t *testing.T) {
	dir := t.TempDir()
	writeLockfiles(t, dir)
	cfg, _ := newTestConfig(t, dir, false)
	cfg.moduleDiscovery = func(context.Context, string) ([]string, error) {
		files := make([]string, apiassets.MaxTrackedModules+1)
		for i := range files {
			files[i] = filepath.ToSlash(filepath.Join("module", string(rune('a'+i%26)), "go.mod"))
		}
		return files, nil
	}
	_, err := runGoPrefetch(t.Context(), cfg)
	if !errors.Is(err, ErrTooManyModules) || !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("too many modules error = %v", err)
	}
}

func TestGoTests_Negative_NestedFailureNamesModule(t *testing.T) {
	_, cfg, recorded := trackedModuleFixture(t, true)
	base := cfg.run
	cfg.run = func(ctx context.Context, dir, name string, args ...string) (string, error) {
		if name == "go" && len(args) > 0 && args[0] == "test" && strings.HasSuffix(filepath.ToSlash(dir), "/tooling") {
			if _, err := base(ctx, dir, name, args...); err != nil {
				t.Logf("expected injected test failure after base command: %v", err)
			}
			return "nested failure", errors.New("exit status 1")
		}
		return base(ctx, dir, name, args...)
	}
	_, err := runGoTests(t.Context(), cfg)
	if err == nil || !strings.Contains(filepath.ToSlash(err.Error()), "/tooling") {
		t.Fatalf("nested test failure = %v", err)
	}
	if len(testInvocations(*recorded)) != 2 {
		t.Fatalf("test invocations = %+v, want both modules", testInvocations(*recorded))
	}
}

func TestGoStages_MeasureOneVsTwoModules(t *testing.T) {
	for _, nested := range []bool{false, true} {
		_, cfg, recorded := trackedModuleFixture(t, nested)
		cfg.run = func(ctx context.Context, dir, name string, args ...string) (string, error) {
			*recorded = append(*recorded, recordedCommand{dir: dir, name: name, args: args})
			if name == govuln.DefaultScanner {
				return cleanGovulnStream, nil
			}
			if name == "go" && len(args) > 0 && args[0] == "list" {
				return dir + "\n", nil
			}
			if name == "go" && len(args) == 2 && args[0] == "env" {
				return map[string]string{"CGO_ENABLED": "1", "CC": "gcc"}[args[1]], nil
			}
			return "", nil
		}
		for _, stage := range []struct {
			name string
			run  func(context.Context, *stageConfig) (string, error)
		}{
			{"prefetch", runGoPrefetch},
			{"security", runGoSecurity},
			{"race", runGoTests},
		} {
			start := time.Now()
			if _, err := stage.run(t.Context(), cfg); err != nil {
				t.Fatalf("%s with nested=%t: %v", stage.name, nested, err)
			}
			t.Logf("measurement stage=%s modules=%d elapsed=%s", stage.name, 1+boolInt(nested), time.Since(start).Round(time.Millisecond))
		}
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func countCommands(commands []recordedCommand, name string, args ...string) int {
	count := 0
	for _, command := range commands {
		if command.name == name && strings.Join(command.args, "\x00") == strings.Join(args, "\x00") {
			count++
		}
	}
	return count
}

func countCommandDirs(commands []recordedCommand, name, dir string) int {
	count := 0
	for _, command := range commands {
		if command.name == name && command.dir == dir {
			count++
		}
	}
	return count
}
