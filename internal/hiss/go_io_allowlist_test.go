// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/semver"
)

// Constructors that ignore the context (#841, pattern 4): an allow-listed third-party function
// is accepted only at a version whose source was read, as the calling module's go.mod states it.

const (
	otlpTraceModule = "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	otlpLogModule   = "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
)

// otelRoot writes a module whose telemetry package builds the trace and log exporters with
// context.Background(), and whose go.mod carries requires; it returns the root.
func otelRoot(t *testing.T, requires string) string {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, "go.mod", "module example.com/app\n\ngo 1.27\n\n"+requires)
	writeFixture(t, root, "telemetry/telemetry.go", strings.Join([]string{
		"package telemetry", // 1
		"",                  // 2
		"import (",          // 3
		"\t\"context\"",
		"",
		"\t\"" + otlpLogModule + "\"",
		"\t\"" + otlpTraceModule + "\"",
		")",              // 8
		"",               // 9
		"func Setup() {", // 10
		"\t_, _ = otlptracegrpc.New(context.Background())", // 11
		"\t_, _ = otlploggrpc.New(context.Background())",   // 12
		"}", // 13
		"",
	}, "\n"))
	return root
}

// hiss02Only drops the HISS-07 findings the blank assignments in otelRoot's file add.
func hiss02Only(rep *ScanReport) []InvariantViolation {
	var kept []InvariantViolation
	for i := 0; i < len(rep.Violations); i++ {
		if rep.Violations[i].RuleID == "HISS-02" {
			kept = append(kept, rep.Violations[i])
		}
	}
	return kept
}

// Negative: both exporters at the versions read are accepted.
func TestGoIOAllowlist_Negative_CheckedVersionsAreAccepted(t *testing.T) {
	root := otelRoot(t, "require (\n\t"+otlpLogModule+" v0.22.0\n\t"+otlpTraceModule+" v1.46.0 // indirect\n)\n")
	if found := hiss02Only(scanFixture(t, root, ScanOptions{})); len(found) != 0 {
		t.Fatalf("exporters at the checked versions ignore the context: %+v", found)
	}
}

// Positive: a version outside the range, a replaced module, a pseudo-version, an unparsable
// version, a module the go.mod does not require and a call without any go.mod are reported.
func TestGoIOAllowlist_Positive_UncheckedVersionsAreReported(t *testing.T) {
	cases := map[string]string{
		"newer":       "require " + otlpTraceModule + " v1.47.0\nrequire " + otlpLogModule + " v0.21.0\n",
		"replaced":    "require " + otlpTraceModule + " v1.46.0\nrequire " + otlpLogModule + " v0.22.0\nreplace " + otlpTraceModule + " => ../fork\nreplace " + otlpLogModule + " v0.22.0 => example.com/fork v0.22.0\n",
		"pseudo":      "require " + otlpTraceModule + " v1.46.1-0.20260101000000-abcdefabcdef\nrequire " + otlpLogModule + " v0.22.0-rc.1\n",
		"unparsable":  "require " + otlpTraceModule + " latest\nrequire " + otlpLogModule + " master\n",
		"notrequired": "",
	}
	for name, requires := range cases {
		t.Run(name, func(t *testing.T) {
			found := hiss02Only(scanFixture(t, otelRoot(t, requires), ScanOptions{}))
			if len(found) != 2 {
				t.Fatalf("both exporters must be reported at %s: %+v", name, found)
			}
		})
	}
	bare := otelRoot(t, "")
	writeFixture(t, bare, "go.mod", "")
	if found := hiss02Only(scanFixture(t, bare, ScanOptions{})); len(found) != 2 {
		t.Fatalf("a go.mod without a module states no version: %+v", found)
	}
}

// Boundary: versionWithin is inclusive at both ends and refuses what does not parse.
func TestGoIOAllowlist_Boundary_VersionRange(t *testing.T) {
	within := []string{"v1.2.0", "v1.3.5", "v1.4.0", "v1.4.0+incompatible"}
	for _, v := range within {
		if !versionWithin(v, "v1.2.0", "v1.4.0") {
			t.Errorf("%s lies within v1.2.0..v1.4.0", v)
		}
	}
	outside := []string{"v1.1.9", "v1.4.1", "v1.2.0-rc.1", "", "1.3", "v1.3.0 extra"}
	for _, v := range outside {
		if versionWithin(v, "v1.2.0", "v1.4.0") {
			t.Errorf("%q lies outside v1.2.0..v1.4.0", v)
		}
	}
	if versionWithin("v1.3.0", "bad", "v1.4.0") || versionWithin("v1.3.0", "v1.2.0", "bad") {
		t.Error("an unparsable bound admits nothing")
	}
}

// TestContextIgnoredByCitesItsRange: every allow-list entry names its module, a range that parses
// with its minimum not above its maximum, and source evidence.
func TestContextIgnoredByCitesItsRange(t *testing.T) {
	if len(contextIgnoredBy) == 0 {
		t.Fatal("the allow-list must hold the OTLP exporters")
	}
	for fn, entry := range contextIgnoredBy {
		lo, loOK := semver.Parse(entry.Min)
		hi, hiOK := semver.Parse(entry.Max)
		if !loOK || !hiOK || semver.Compare(lo, hi) > 0 {
			t.Errorf("%+v: range %s..%s does not parse or is inverted", fn, entry.Min, entry.Max)
		}
		if entry.Module == "" || !strings.HasPrefix(fn.Path, entry.Module) || !strings.Contains(entry.Evidence, ".go") {
			t.Errorf("%+v: module %q or evidence %q missing", fn, entry.Module, entry.Evidence)
		}
	}
}
