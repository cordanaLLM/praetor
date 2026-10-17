package adopt

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/buildid"
)

// staleToolsModule pins the engine tool at an older pseudo-version, as the adopter of #1054 did.
const staleToolsModule = "module example.com/x/tools\n\ngo 1.27\n\ntool github.com/cordanaLLM/praetor/cmd/standardsctl\n\n" +
	"require github.com/cordanaLLM/praetor v0.0.0-20261009120000-b0d27ad5a227\n"

// pinSession returns a session over a fresh directory, holding toolsModule as tools/go/go.mod
// when it is not empty, run by engine.
func pinSession(t *testing.T, toolsModule string, engine buildid.Identity) *adoptSession {
	t.Helper()
	repo := t.TempDir()
	if toolsModule != "" {
		mustWrite(t, filepath.Join(repo, "tools", "go", "go.mod"), toolsModule)
	}
	return &adoptSession{repoPath: repo, report: &AdoptReport{}, opts: AdoptOptions{Path: repo, Engine: engine}}
}

// Positive: a tools module pinning an older engine than the one adopting is reported with the
// file, the pin, the running engine and the command that moves the pin.
func TestReportStaleEnginePin_Positive_OlderPinIsReported(t *testing.T) {
	s := pinSession(t, staleToolsModule, buildid.Identity{Revision: "048c3d51be91"})
	if err := reportStaleEnginePin(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"tools/go/go.mod pins the audit engine github.com/cordanaLLM/praetor at v0.0.0-20261009120000-b0d27ad5a227",
		"ran engine 048c3d51be91", "go get -modfile=tools/go/go.mod github.com/cordanaLLM/praetor@048c3d51be91"} {
		if !hasWarningContaining(s.report, want) {
			t.Errorf("warnings %v lack %q", s.report.Warnings, want)
		}
	}
}

// Negative: nothing is reported without a tools module, for one that requires the engine module
// without the engine tool, for a pin at the running revision, or when the running engine
// identifies no build.
func TestReportStaleEnginePin_Negative_NothingToMove(t *testing.T) {
	withoutTool := strings.Replace(staleToolsModule, "tool github.com/cordanaLLM/praetor/cmd/standardsctl\n", "", 1)
	for name, c := range map[string]struct {
		module string
		engine buildid.Identity
	}{
		"no tools module":   {"", buildid.Identity{Revision: "048c3d51be91"}},
		"no engine tool":    {withoutTool, buildid.Identity{Revision: "048c3d51be91"}},
		"same revision":     {staleToolsModule, buildid.Identity{Revision: "b0d27ad5a227"}},
		"unknown engine":    {staleToolsModule, buildid.Identity{Unknown: "no build information"}},
		"dirty same commit": {staleToolsModule, buildid.Identity{Revision: "b0d27ad5a227", Modified: true}},
	} {
		s := pinSession(t, c.module, c.engine)
		if err := reportStaleEnginePin(t.Context(), s); err != nil || len(s.report.Warnings) != 0 {
			t.Errorf("%s: err %v, warnings %v; want none", name, err, s.report.Warnings)
		}
	}
}

// Boundary: the tool directive in a block with a comment counts; a release pin is compared as a
// release and moves to the running release; a pin and an engine of the other kind are stale and
// move to the engine; a pseudo-version that identifies nothing is never stale.
func TestReportStaleEnginePin_Boundary_FormsAndKinds(t *testing.T) {
	block := "module example.com/x/tools\n\ntool (\n\tgithub.com/cordanaLLM/praetor/cmd/standardsctl // audit engine\n\tgolang.org/x/vuln/cmd/govulncheck\n)\n\n" +
		"require (\n\tgithub.com/cordanaLLM/praetor v1.4.0\n)\n"
	if version, ok := pinnedEngineVersion([]byte(block)); !ok || version != "v1.4.0" {
		t.Fatalf("block tool directive read as %q, %v", version, ok)
	}
	for name, c := range map[string]struct {
		pinned  string
		running buildid.Identity
		target  string
		stale   bool
	}{
		"same release":         {"v1.4.0", buildid.Identity{Release: "v1.4.0"}, "v1.4.0", false},
		"older release":        {"v1.3.0", buildid.Identity{Release: "v1.4.0"}, "v1.4.0", true},
		"release pin, rev run": {"v1.4.0", buildid.Identity{Revision: "048c3d51be91"}, "048c3d51be91", true},
		"rev pin, release run": {"v0.0.0-20261009120000-b0d27ad5a227", buildid.Identity{Release: "v1.4.0"}, "v1.4.0", true},
		"unreadable pin":       {"latest", buildid.Identity{Revision: "048c3d51be91"}, "", false},
	} {
		if target, stale := staleEnginePin(c.pinned, c.running); target != c.target || stale != c.stale {
			t.Errorf("%s: staleEnginePin = %q, %v; want %q, %v", name, target, stale, c.target, c.stale)
		}
	}
}
