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

// compileFixture compiles source and fails the test on an error.
func compileFixture(t *testing.T, source string) *CompileResult {
	t.Helper()
	result, err := NewTranspiler().CompileContent(source)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Layered {
		t.Fatal("marked source reported unlayered")
	}
	return result
}

func TestBandsEmitHeadConfigTailInOrderForEveryVendorFile(t *testing.T) {
	for _, file := range compileFixture(t, bandFixture).Files {
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
	}
}

func TestBandsJoinWithOneBlankLineAndKeepTheFinalNewline(t *testing.T) {
	for _, file := range compileFixture(t, bandFixture).Files {
		if strings.Contains(file.Content, "\n\n\n") || !strings.HasSuffix(file.Content, "\n\nTail text.\n") {
			t.Fatalf("%s: band joins left a gap or lost the tail: %q", file.RelativePath, file.Content)
		}
	}
}

func TestVendorSectionJoinsTheConfigBandOfItsOwnFileOnly(t *testing.T) {
	for _, file := range compileFixture(t, bandFixture).Files {
		isClaude := file.RelativePath == "CLAUDE.md"
		if strings.Contains(file.Content, "Claude only.") != isClaude {
			t.Fatalf("%s: vendor section placement wrong", file.RelativePath)
		}
		c := file.Content
		if isClaude && !strings.Contains(c, "Config text.\n\n## Claude Code\nClaude only.\n\nTail text.") {
			t.Fatalf("vendor section is not in the config band: %q", c)
		}
	}
}

// endingCases lists band combinations; each ends with a newline exactly when its source does.
var endingCases = map[string]string{
	"head only":              "# H\n\n" + BandHeadMarker + "\nHead.\n",
	"head and config":        "# H\n\n" + BandHeadMarker + "\nHead.\n\n" + BandConfigMarker + "\nConfig.\n",
	"head and tail":          "# H\n\n" + BandHeadMarker + "\nHead.\n\n" + BandTailMarker + "\nTail.\n",
	"out of order":           "# H\n\n" + BandTailMarker + "\nTail.\n" + BandConfigMarker + "\nConfig.\n",
	"tail then vendor":       "# H\n\n" + BandTailMarker + "\nTail.\n\n## Claude Code\nClaude only.\n",
	"config then blank tail": "# H\n\n" + BandConfigMarker + "\nConfig.\n\n" + BandTailMarker + "\n\n",
}

func TestLayeredOutputEndsWithNewlineExactlyWhenTheSourceDoes(t *testing.T) {
	for name, source := range endingCases {
		for _, trimmed := range []bool{false, true} {
			text := source
			if trimmed {
				text = strings.TrimRight(source, "\n")
			}
			for _, file := range compileFixture(t, text).Files {
				body := strings.TrimPrefix(file.Content, testFrontmatterFor(file.RelativePath)+testHeader)
				if strings.HasSuffix(body, "\n") != !trimmed || strings.HasSuffix(body, "\n\n") {
					t.Fatalf("%s (trimmed=%v) %s: ending wrong: %q", name, trimmed, file.RelativePath, body)
				}
			}
		}
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
	if _, ok, err := HeadBand(source); ok || err != nil {
		t.Fatalf("unmarked source has a head band: %v, %v", ok, err)
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
	head, ok, err := HeadBand(bandFixture)
	if err != nil || !ok || head != "# Harness\n\nHead text." {
		t.Fatalf("head = %q, %v, %v", head, ok, err)
	}
}

var sha64 = strings.Repeat("0123456789abcdef", 4)

// volatileShapes lists one planted positive per exact shape the scanner flags.
var volatileShapes = []struct{ kind, line string }{
	{"timestamp", "Built 2026-10-07T12:30:00Z by CI."},
	{"timestamp", "Built 2026-10-07T12:30:00.123+02:00 by CI."},
	{"timestamp", "Built 2026-10-07T12:30:00."},
	{"timestamp", "2026-10-07 12:30:00Z"},
	{"digest", "pin sha256:" + sha64},
	{"digest", "sha256:abcdef012345"},
	{"absolute path", "see /home/someone/work/file"},
	{"absolute path", "dir `/Users/me/x`"},
	{"absolute path", "config at /etc/ssl/certs/ca.pem"},
	{"absolute path", "cache=/usr/local/share/app"},
	{"absolute path", "mounted /private/var/folders/x"},
	{"absolute path", "disk /Volumes/Data/work"},
	{"absolute path", "temp /tmp/tmp.Xa3bQ9"},
	{"absolute path", "cache /root/.cache/pkg"},
	{"absolute path", `path C:\Users\me\work`},
	{"absolute path", "path D:/work/out"},
	{"absolute path", `share \\server\share\dir`},
	{"run counter", "run #42 of the suite"},
	{"run counter", "run_id: 7f3a9"},
	{"run counter", "run-id=1234"},
	{"run counter", "build number 1234"},
	{"run counter", "Build #88"},
}

// proseLines look like a volatile shape but are not: each must pass the scanner.
var proseLines = []string{
	"Rules apply since 2026-10-07.",
	"Meeting at 2026-10-07 12:30.",
	"Use `make verify-all`; evidence: <path> sha256:<12 hex> lines:<n>",
	"Pinned to 0123456789abcdef0123456789abcdef01234567 for good.",
	"retry count: 3",
	"Run: 2 passes",
	"run `go test`, count the failures, path ./cmd/standardsctl",
	"Scratch goes to /var/tmp in a sentence.",
	"Keep /etc and /usr out of it.",
	"Read src/etc/x/y and a/home/b/c, not https://example.com/home/x/y.",
	"The build takes 3 targets; attempt 3 of 5.",
	"",
}

func TestScanVolatileFlagsEachPlantedShape(t *testing.T) {
	for _, c := range volatileShapes {
		found := ScanVolatile("stable line\n" + c.line)
		if len(found) != 1 || found[0].Kind != c.kind || found[0].Line != 2 {
			t.Fatalf("%q: %+v", c.line, found)
		}
	}
}

func TestScanVolatilePassesOrdinaryProse(t *testing.T) {
	for _, line := range proseLines {
		if found := ScanVolatile(line); len(found) != 0 {
			t.Fatalf("%q flagged: %+v", line, found)
		}
	}
}

func TestScanVolatileBoundsFindings(t *testing.T) {
	head := strings.Repeat("at 2026-10-07T12:30:00Z\n", maxVolatileFindings*3)
	if got := len(ScanVolatile(head)); got != maxVolatileFindings {
		t.Fatalf("findings = %d, want %d", got, maxVolatileFindings)
	}
}

// Negative (Finding 3): scanner catches mktemp paths, no-zone timestamps, space-separator
// timestamps and short digests.
func TestScanVolatile_Negative_MissedTokens(t *testing.T) {
	cases := []struct {
		token string
		kind  string
		want  string
	}{
		{"/tmp/tmp.Xa3bQ9", "absolute path", "/tmp/tmp.Xa3bQ9"},
		{"Built 2026-10-07T12:30:00.", "timestamp", "2026-10-07T12:30:00"},
		{"2026-10-07 12:30:00Z", "timestamp", "2026-10-07 12:30:00Z"},
		{"sha256:abcdef012345", "digest", "sha256:abcdef012345"},
	}
	for _, c := range cases {
		found := ScanVolatile(c.token)
		if len(found) != 1 {
			t.Fatalf("%q: expected 1 token, got %d: %+v", c.token, len(found), found)
		}
		if found[0].Kind != c.kind {
			t.Errorf("%q: kind = %q, want %q", c.token, found[0].Kind, c.kind)
		}
		if found[0].Match != c.want {
			t.Errorf("%q: match = %q, want %q", c.token, found[0].Match, c.want)
		}
	}
}

// Boundary (Finding 3 nit): leading boundary characters like '=' and '(' are trimmed from Match.
func TestScanVolatile_BoundaryTrim(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  string
	}{
		{"=/home/kilian", "/home/kilian"},
		{"(/home/u/x)", "/home/u/x"},
		{"`/Users/me/x`", "/Users/me/x"},
	} {
		found := ScanVolatile(tc.input)
		if len(found) != 1 || found[0].Match != tc.want {
			t.Fatalf("%q: got %+v, want match %q", tc.input, found, tc.want)
		}
	}
}

// Finding 4: a vendor section opened in the head with no blank line before the next marker
// relocates to the config band and is joined with a blank line instead of glued to config text.
func TestLayoutBands_RelocatedVendorSectionWithoutTrailingBlankLine(t *testing.T) {
	source := "# Harness\n\n<!-- praetor:head -->\nHead.\n## Claude Code\nClaude only.\n<!-- praetor:config -->\nConfig.\n"
	result, err := NewTranspiler().CompileContent(source)
	if err != nil {
		t.Fatal(err)
	}
	var claude string
	for _, file := range result.Files {
		if file.RelativePath == "CLAUDE.md" {
			claude = file.Content
			break
		}
	}
	if claude == "" {
		t.Fatal("CLAUDE.md missing from compilation result")
	}
	want := "Head.\n\n## Claude Code\nClaude only.\n\nConfig.\n"
	if !strings.HasSuffix(claude, want) {
		t.Fatalf("CLAUDE.md vendor section glued: got %q, want suffix %q", claude, want)
	}
}

// vendorBandFixture opens a Claude-only section inside the head band and another inside the
// tail band, so only the relocation rule can place them in the config band.
var vendorBandFixture = strings.Join([]string{
	"# Harness",
	"",
	BandHeadMarker,
	"Head text.",
	"",
	"## Claude Code",
	"Claude head section.",
	"",
	BandConfigMarker,
	"Config text.",
	"",
	BandTailMarker,
	"Tail text.",
	"",
	"## Claude Code",
	"Claude tail section.",
	"",
}, "\n")

func TestVendorSectionsInHeadAndTailRelocateToConfigBand(t *testing.T) {
	result, err := NewTranspiler().CompileContent(vendorBandFixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range result.Files {
		c := file.Content
		isClaude := file.RelativePath == "CLAUDE.md"
		for _, section := range []string{"Claude head section.", "Claude tail section."} {
			if strings.Contains(c, section) != isClaude {
				t.Fatalf("%s: %q presence wrong", file.RelativePath, section)
			}
		}
		if !isClaude {
			continue
		}
		want := "Head text.\n\n## Claude Code\nClaude head section.\n\nConfig text.\n\n## Claude Code\nClaude tail section.\n\nTail text.\n"
		if !strings.HasSuffix(c, want) {
			t.Fatalf("%s: vendor sections not relocated into the config band, got %q", file.RelativePath, c)
		}
	}
}

func TestHeadBandOmitsVendorSectionsOpenedInHeadAndTail(t *testing.T) {
	head, ok, err := HeadBand(vendorBandFixture)
	if err != nil || !ok {
		t.Fatalf("marked source: %v, %v", ok, err)
	}
	if head != "# Harness\n\nHead text." {
		t.Fatalf("head = %q, want exactly the head text without vendor sections", head)
	}
}

// Negative: an over-budget source is an error, never an unmarked source.
func TestHeadBandReportsAnOverBudgetSource(t *testing.T) {
	big := BandHeadMarker + "\n" + strings.Repeat("x\n", maxCanonicalLines+1)
	if head, ok, err := HeadBand(big); err == nil || ok || head != "" {
		t.Fatalf("over-budget source: %q, %v, %v", head, ok, err)
	}
}

func TestInsertIntoConfigBand(t *testing.T) {
	section := "## Register\n\nBlock."
	cases := map[string]struct{ in, want string }{
		"after the config marker": {
			"# H\n\n" + BandHeadMarker + "\nHead.\n\n" + BandConfigMarker + "\nConfig.\n\n" + BandTailMarker + "\nTail.\n",
			"# H\n\n" + BandHeadMarker + "\nHead.\n\n" + BandConfigMarker + "\n\n" + section + "\n\nConfig.\n\n" + BandTailMarker + "\nTail.\n",
		},
		"new config marker before the tail": {
			"# H\n\n" + BandHeadMarker + "\nHead.\n\n" + BandTailMarker + "\nTail.\n",
			"# H\n\n" + BandHeadMarker + "\nHead.\n\n" + BandConfigMarker + "\n\n" + section + "\n\n" + BandTailMarker + "\nTail.\n",
		},
		"new config marker at the end": {
			"# H\n\n" + BandHeadMarker + "\nHead.\n",
			"# H\n\n" + BandHeadMarker + "\nHead.\n\n" + BandConfigMarker + "\n\n" + section + "\n",
		},
	}
	for name, c := range cases {
		got, layered := InsertIntoConfigBand(c.in, section)
		if !layered || got != c.want {
			t.Fatalf("%s: layered=%v got %q want %q", name, layered, got, c.want)
		}
	}
	if got, layered := InsertIntoConfigBand("# H\nPlain.\n", section); layered || got != "# H\nPlain.\n" {
		t.Fatalf("unmarked source changed: %q, %v", got, layered)
	}
}
