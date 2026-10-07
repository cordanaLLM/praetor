package agentcontext

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// bandFixture writes its bands out of order and carries a Claude-only section, so the emit
// order, the marker removal and the vendor placement are all observable.
var bandFixture = strings.Join([]string{
	"# Harness",
	"",
	BandTailMarker,
	"Tail text.",
	"",
	BandConfigMarker,
	"Config text.",
	"",
	"## Claude Code",
	"Claude only.",
	"",
	BandHeadMarker,
	"Head text.",
	"",
}, "\n")

func TestBandsEmitHeadConfigTailForEveryVendorFile(t *testing.T) {
	result, err := NewTranspiler().CompileContent(bandFixture)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Layered {
		t.Fatal("marked source reported unlayered")
	}
	for _, file := range result.Files {
		if !strings.HasPrefix(file.Content, testFrontmatterFor(file.RelativePath)+testHeader+"# Harness") {
			t.Fatalf("%s: static header or title not first: %q", file.RelativePath, file.Content)
		}
		head := strings.Index(file.Content, "Head text.")
		config := strings.Index(file.Content, "Config text.")
		tail := strings.Index(file.Content, "Tail text.")
		if head < 0 || head >= config || config >= tail {
			t.Fatalf("%s: band order head=%d config=%d tail=%d", file.RelativePath, head, config, tail)
		}
		if strings.Contains(file.Content, "praetor:") {
			t.Fatalf("%s: marker leaked into the compiled file", file.RelativePath)
		}
		if strings.Contains(file.Content, "Claude only.") != (file.RelativePath == "CLAUDE.md") {
			t.Fatalf("%s: vendor section placement wrong", file.RelativePath)
		}
		if strings.Contains(file.Content, "\n\n\n") || !strings.HasSuffix(file.Content, "Tail text.") && !strings.HasSuffix(file.Content, "Tail text.\n") {
			t.Fatalf("%s: band joins left a gap or lost the tail: %q", file.RelativePath, file.Content)
		}
	}
	claude := result.Files[0].Content
	if strings.Index(claude, "Claude only.") < strings.Index(claude, "Config text.") || strings.Index(claude, "Claude only.") > strings.Index(claude, "Tail text.") {
		t.Fatalf("vendor section is not in the config band: %q", claude)
	}
}

func testFrontmatterFor(path string) string {
	if path == ".cursor/rules/hiss-invariants.mdc" {
		return testFrontmatter
	}
	return ""
}

func TestUnmarkedSourceKeepsSourceOrder(t *testing.T) {
	source := "# Harness\nTail first.\n\n## Config\nConfig second.\n"
	result, err := NewTranspiler().CompileContent(source)
	if err != nil {
		t.Fatal(err)
	}
	if result.Layered {
		t.Fatal("unmarked source reported layered")
	}
	if got := result.Files[0].Content; got != testHeader+source {
		t.Fatalf("unmarked source changed: %q", got)
	}
	if _, ok := HeadBand(source); ok {
		t.Fatal("unmarked source has a head band")
	}
}

func TestMarkerInsideFenceOpensNoBand(t *testing.T) {
	source := "# Harness\n\n```\n" + BandTailMarker + "\n```\n"
	result, err := NewTranspiler().CompileContent(source)
	if err != nil {
		t.Fatal(err)
	}
	if result.Layered || !strings.Contains(result.Files[0].Content, BandTailMarker) {
		t.Fatalf("fenced marker acted as a marker: %+v", result)
	}
}

func TestRenderIsIdenticalUnderClockAndSeed(t *testing.T) {
	base, err := NewTranspiler().CompileContent(bandFixture)
	if err != nil {
		t.Fatal(err)
	}
	for seed := int64(0); seed < 20; seed++ {
		tr := NewTranspiler()
		at := time.Unix(seed*1e6, 0)
		tr.Env = &RenderEnv{Now: func() time.Time { return at }, Seed: seed}
		got, err := tr.CompileContent(bandFixture)
		if err != nil {
			t.Fatal(err)
		}
		for i, file := range got.Files {
			if file != base.Files[i] {
				t.Fatalf("seed %d: %s differs", seed, file.RelativePath)
			}
		}
	}
}

func TestEnvOrderIsAPermutation(t *testing.T) {
	var nilEnv *RenderEnv
	if got := nilEnv.order(3); got[0] != 0 || got[1] != 1 || got[2] != 2 {
		t.Fatalf("nil env reordered: %v", got)
	}
	if got := (&RenderEnv{Seed: 7}).order(0); len(got) != 0 {
		t.Fatalf("empty order: %v", got)
	}
	if a, b := (&RenderEnv{Seed: 2}).order(6), (&RenderEnv{Seed: 5}).order(6); slices.Equal(a, b) {
		t.Fatalf("stability seeds give one order: %v", a)
	}
	seen := map[int]bool{}
	moved := false
	for i, v := range (&RenderEnv{Seed: 3}).order(6) {
		seen[v] = true
		moved = moved || i != v
	}
	if len(seen) != 6 || !moved {
		t.Fatalf("order is not a shuffled permutation: %v", seen)
	}
}

func TestHeadEditChangesOnlyHeadBytes(t *testing.T) {
	edited := strings.Replace(bandFixture, "Head text.", "Head text, edited longer.", 1)
	before, err := NewTranspiler().CompileContent(bandFixture)
	if err != nil {
		t.Fatal(err)
	}
	after, err := NewTranspiler().CompileContent(edited)
	if err != nil {
		t.Fatal(err)
	}
	for i := range before.Files {
		a, b := before.Files[i].Content, after.Files[i].Content
		at := strings.Index(a, "Head text.")
		if a[:at] != b[:at] {
			t.Fatalf("%s: bytes before the edit moved", before.Files[i].RelativePath)
		}
		if a[strings.Index(a, "Config text."):] != b[strings.Index(b, "Config text."):] {
			t.Fatalf("%s: bytes after the head moved", before.Files[i].RelativePath)
		}
	}
}

func TestConfigEditLeavesHeadBytesAlone(t *testing.T) {
	edited := strings.Replace(bandFixture, "Config text.", "Config text, edited.", 1)
	before, err := NewTranspiler().CompileContent(bandFixture)
	if err != nil {
		t.Fatal(err)
	}
	after, err := NewTranspiler().CompileContent(edited)
	if err != nil {
		t.Fatal(err)
	}
	for i := range before.Files {
		a, b := before.Files[i].Content, after.Files[i].Content
		cut := strings.Index(a, "Config text.")
		if a[:cut] != b[:cut] {
			t.Fatalf("%s: a config edit moved head bytes", before.Files[i].RelativePath)
		}
	}
}

func TestHeadBandExcludesVendorSectionsAndOtherBands(t *testing.T) {
	head, ok := HeadBand(bandFixture)
	if !ok || head != "# Harness\n\nHead text." {
		t.Fatalf("head = %q, %v", head, ok)
	}
}

func TestScanVolatileFindsEachKindAndNothingInPlainText(t *testing.T) {
	cases := map[string]string{
		"timestamp":     "Built 2026-10-07T12:30:00Z by CI.",
		"digest":        "pin sha256:0123456789abcdef0123",
		"absolute path": "see /home/someone/work/file",
		"run counter":   "run #42 of the suite",
	}
	for kind, line := range cases {
		found := ScanVolatile("stable line\n" + line)
		if len(found) != 1 || found[0].Kind != kind || found[0].Line != 2 {
			t.Fatalf("%s: %+v", kind, found)
		}
	}
	for _, clean := range []string{
		"Rules apply since 2026-10-07.",
		"Use `make verify-all`; evidence: <path> sha256:<12 hex> lines:<n>",
		"run `go test`, count the failures, path ./cmd/standardsctl",
		"",
	} {
		if found := ScanVolatile(clean); len(found) != 0 {
			t.Fatalf("%q flagged: %+v", clean, found)
		}
	}
}

func TestScanVolatileBoundsFindings(t *testing.T) {
	head := strings.Repeat("at 2026-10-07T12:30:00Z\n", maxVolatileFindings*3)
	if got := len(ScanVolatile(head)); got != maxVolatileFindings {
		t.Fatalf("findings = %d, want %d", got, maxVolatileFindings)
	}
}
