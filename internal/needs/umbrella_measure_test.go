package needs

import (
	"strings"
	"testing"
)

// graphListing is a go list listing in moduleGraphTemplate's shape: an application importing
// the umbrella, the umbrella grouping two packages of its own module and a store module, and
// the runtime the go command adds without an import naming it.
const graphListing = "runtime\ttrue\t\t\tunsafe\n" +
	"unsafe\ttrue\t\t\t\n" +
	"fmt\ttrue\t\t\tunsafe\n" +
	"example.com/kit/config\ttrue\texample.com/kit\tfalse\t\n" +
	"example.com/kit/httpx\ttrue\texample.com/kit\tfalse\tfmt\n" +
	"example.com/kit/store\ttrue\texample.com/kit/store\tfalse\tunsafe\n" +
	"example.com/kit\ttrue\texample.com/kit\tfalse\texample.com/kit/config example.com/kit/httpx example.com/kit/store\n" +
	"example.com/app\tfalse\texample.com/app\ttrue\texample.com/kit\n"

func mustGraph(t *testing.T, listing string) moduleGraph {
	t.Helper()
	graph, err := parseModuleGraph([]byte(listing), maxModuleGraphPackages)
	if err != nil {
		t.Fatal(err)
	}
	return graph
}

// The switch removes what only the umbrella reached; the runtime and its imports stay.
func TestModuleGraphMeasureSwitch_3D(t *testing.T) {
	graph := mustGraph(t, graphListing)
	replacements := map[string][]string{"example.com/app": {"example.com/kit/config", "example.com/kit/httpx"}}
	got := graph.measureSwitch("example.com/kit", replacements)
	want := UmbrellaMeasurement{Measured: true, ModulesBefore: 2, ModulesAfter: 1, PackagesBefore: 8, PackagesAfter: 6}
	if got != want {
		t.Fatalf("measurement = %+v, want %+v", got, want)
	}
	for name, tc := range map[string]struct {
		umbrella     string
		replacements map[string][]string
		want         string
	}{
		"unlisted replacement": {"example.com/kit", map[string][]string{"example.com/app": {"example.com/kit/mail"}}, "does not list example.com/kit/mail"},
		"unlisted umbrella":    {"example.com/other", replacements, "does not list the umbrella example.com/other"},
		"unscanned importer":   {"example.com/kit", map[string][]string{}, "from a source the scan did not read"},
	} {
		got := graph.measureSwitch(tc.umbrella, tc.replacements)
		if got.Measured || !strings.Contains(got.Reason, tc.want) || got.PackagesBefore != 0 {
			t.Errorf("%s: measurement = %+v, want not measured with %q", name, got, tc.want)
		}
	}
}

// A listing holds at most limit packages and only lines of the template's shape.
func TestParseModuleGraphBounds_3D(t *testing.T) {
	lines := strings.Count(graphListing, "\n")
	if _, err := parseModuleGraph([]byte(graphListing), lines); err != nil {
		t.Fatalf("a listing at the bound was refused: %v", err)
	}
	if _, err := parseModuleGraph([]byte(graphListing), lines-1); err == nil || !strings.Contains(err.Error(), "more than 7 packages") {
		t.Fatalf("a listing past the bound was read: %v", err)
	}
	if graph, err := parseModuleGraph([]byte(strings.ReplaceAll(graphListing, "\n", "\r\n")), lines); err != nil || len(graph) != lines {
		t.Fatalf("CRLF listing: %v (%d packages)", err, len(graph))
	}
	for name, listing := range map[string]string{
		"short line": "example.com/app\tfalse\n",
		"no path":    "\tfalse\texample.com/app\ttrue\t\n",
		"empty":      "\n",
	} {
		if _, err := parseModuleGraph([]byte(listing), maxModuleGraphPackages); err == nil {
			t.Errorf("%s: malformed listing was read", name)
		}
	}
}

// An unmapped reference skips go list altogether: the umbrella import stays.
func TestMeasureUmbrellaSwitchUnmappedSkipsGoList(t *testing.T) {
	got, err := measureUmbrellaSwitch(t.Context(), t.TempDir(), "example.com/kit", []string{"Version", "a blank import"}, nil)
	if err != nil || got.Measured || got.Reason != "the umbrella import stays for Version, a blank import, which no grouping describes" {
		t.Fatalf("measurement = %+v, %v", got, err)
	}
	if mode := moduleGraphMode(t.TempDir()); mode != "-mod=readonly" {
		t.Fatalf("a project without vendor/modules.txt reads %s", mode)
	}
	vendored := t.TempDir()
	writeFixture(t, vendored, "vendor/modules.txt", "# example.com/kit v1.0.0\n")
	if mode := moduleGraphMode(vendored); mode != "-mod=vendor" {
		t.Fatalf("a vendoring project reads %s", mode)
	}
}
