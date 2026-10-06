// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package managedasset

import (
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/strictjson"
	"github.com/cordanaLLM/praetor/internal/util"
)

// renovateConfig is the part of renovate.json that keeps a hosted workflow's SHA pins and a
// family package.json's exact pins current.
type renovateConfig struct {
	GitHubActions struct {
		ManagerFilePatterns []string `json:"managerFilePatterns"`
	} `json:"github-actions"`
	PackageRules []renovatePackageRule `json:"packageRules"`
}

// renovatePackageRule is the part of one packageRules entry these tests read.
type renovatePackageRule struct {
	MatchManagers  []string `json:"matchManagers"`
	MatchFileNames []string `json:"matchFileNames"`
	MatchDepTypes  []string `json:"matchDepTypes"`
	PinDigests     bool     `json:"pinDigests"`
	RangeStrategy  string   `json:"rangeStrategy"`
	GroupName      string   `json:"groupName"`
}

func readRenovateConfig(t *testing.T) renovateConfig {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "renovate.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config renovateConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	return config
}

// renovateJSONBounds bound the strict scan of renovate.json (HISS-02).
var renovateJSONBounds = strictjson.Options{MaxBytes: 1 << 20, MaxDepth: 16, Names: strictjson.ExactNames}

// Negative, a tripwire for merges: two branches that each add a top-level list such as
// customManagers merge without a conflict into one file holding the key twice, and a JSON
// reader then keeps the last list and drops the other without a word (Renovate's validator
// included). renovate.json repeats no member name in any object.
func TestRenovateConfigRepeatsNoMemberName(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "renovate.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := strictjson.Validate(raw, renovateJSONBounds); err != nil {
		t.Fatalf("renovate.json: %v", err)
	}
	twice := []byte(`{"customManagers": [{"customType": "regex"}], "customManagers": []}`)
	if err := strictjson.Validate(twice, renovateJSONBounds); !errors.Is(err, strictjson.ErrDuplicate) {
		t.Fatalf("a repeated customManagers key was accepted: %v", err)
	}
}

// githubActionsReads reports whether one of the github-actions manager's added file patterns,
// a /re2/ literal as Renovate spells it, matches rel.
func githubActionsReads(t *testing.T, config renovateConfig, rel string) bool {
	t.Helper()
	for _, pattern := range config.GitHubActions.ManagerFilePatterns {
		expr, ok := strings.CutPrefix(pattern, "/")
		expr, closed := strings.CutSuffix(expr, "/")
		if !ok || !closed {
			t.Fatalf("github-actions file pattern %q is not a /regex/ literal", pattern)
		}
		if regexp.MustCompile(expr).MatchString(rel) {
			return true
		}
	}
	return false
}

// hostedFamilies returns the registered families with a hosted workflow; there must be one.
func hostedFamilies(t *testing.T) []Family {
	t.Helper()
	hosted := slices.DeleteFunc(Families(), func(f Family) bool { return f.WorkflowFile == "" })
	if len(hosted) == 0 {
		t.Fatal("no family declares a hosted workflow; the Renovate checks would pass vacuously")
	}
	return hosted
}

// Positive: for every family with a hosted workflow, Renovate's github-actions manager reads the
// template source as well as the repository's own copy, pins digests, and groups the two files
// into one branch, so an update never leaves them apart for the audit byte lock to reject. Every
// uses: line sits on a line of its own in the source, where the manager's line-based extractor
// finds it.
func TestRenovateUpdatesTemplatePinsWithWorkflowCopy(t *testing.T) {
	config := readRenovateConfig(t)
	for _, family := range hostedFamilies(t) {
		if !githubActionsReads(t, config, family.Source) {
			t.Fatalf("renovate.json github-actions.managerFilePatterns does not cover %s", family.Source)
		}
		pinned, grouped := false, false
		for _, rule := range config.PackageRules {
			if !slices.Contains(rule.MatchManagers, "github-actions") {
				continue
			}
			pinned = pinned || (rule.PinDigests && len(rule.MatchFileNames) == 0)
			grouped = grouped || (rule.GroupName != "" && slices.Contains(rule.MatchFileNames, family.WorkflowFile) &&
				slices.Contains(rule.MatchFileNames, family.Source))
		}
		if !pinned || !grouped {
			t.Fatalf("renovate.json github-actions rules: pinDigests=%v, one group over %s and %s=%v",
				pinned, family.WorkflowFile, family.Source, grouped)
		}
		source, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(family.Source)))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(source), "\n")
		for _, line := range strings.Split(family.Workflow, "\n") {
			if strings.Contains(line, "uses:") && !slices.Contains(lines, line) {
				t.Fatalf("uses line %q is not a line of its own in %s", line, family.Source)
			}
		}
	}
}

// Negative and boundary: each added pattern names a template source alone, not its test or the
// neighbouring assets, which carry no workflow.
func TestRenovateTemplatePatternIsExact(t *testing.T) {
	config := readRenovateConfig(t)
	for _, family := range hostedFamilies(t) {
		neighbours := []string{path.Join(family.Directory, "assets_test.go"), "x" + family.Source, family.Source + ".bak"}
		neighbours = append(neighbours, family.AssetPaths()...)
		for _, rel := range neighbours {
			if githubActionsReads(t, config, rel) {
				t.Fatalf("renovate.json github-actions.managerFilePatterns also matches %s", rel)
			}
		}
	}
}

// renovateNpmSections are the package.json members Renovate's npm manager extracts, each as
// the depType of its entries (lib/modules/manager/npm/extract/common/package-file.ts).
var renovateNpmSections = []string{
	"dependencies", "devDependencies", "optionalDependencies", "peerDependencies", "engines",
	"volta", "resolutions", "packageManager", "overrides", "pnpm",
}

// renovateRangeSections hold ranges by design: engines states which runtimes a consumer may
// use, peerDependencies what a host must provide. Renovate's npm manager returns an explicit
// rangeStrategy unchanged for every depType (lib/modules/manager/npm/range.ts), and its lookup
// proposes a pin for any value that is not one version, so a pin rule reaching either section
// rewrites the range into one exact release. Renovate's :pinAllExceptPeerDependencies preset
// resets both to auto for that reason; a rule here scopes its pin with matchDepTypes instead.
var renovateRangeSections = []string{"engines", "peerDependencies"}

// npmSections returns the members of a package.json that Renovate's npm manager extracts, in
// renovateNpmSections order.
func npmSections(t *testing.T, manifest []byte) []string {
	t.Helper()
	var members map[string]json.RawMessage
	if err := json.Unmarshal(manifest, &members); err != nil {
		t.Fatalf("decode package.json: %v", err)
	}
	return slices.DeleteFunc(slices.Clone(renovateNpmSections), func(section string) bool {
		_, declared := members[section]
		return !declared
	})
}

// pinsSection reports whether rule sets rangeStrategy pin for the npm manager's entries of one
// section of the package.json at rel. The file match is Renovate's matchFileNames, through the
// one implementation adoption also reads it with (util.RenovatePatternsCover);
// unevaluablePinPatterns keeps every pin rule's patterns inside the subset it evaluates.
func pinsSection(rule renovatePackageRule, rel, section string) bool {
	return rule.RangeStrategy == "pin" &&
		(len(rule.MatchManagers) == 0 || slices.Contains(rule.MatchManagers, "npm")) &&
		(len(rule.MatchDepTypes) == 0 || slices.Contains(rule.MatchDepTypes, section)) &&
		(len(rule.MatchFileNames) == 0 || util.RenovatePatternsCover(rule.MatchFileNames, rel))
}

// unevaluablePinPatterns returns the matchFileNames patterns of the pin rules in rules that fall
// outside the glob subset util.RenovatePatternsCover evaluates (util.RenovateGlobSupported): the
// pin check cannot tell which files such a pattern reaches.
func unevaluablePinPatterns(rules []renovatePackageRule) []string {
	var outside []string
	for _, rule := range rules {
		if rule.RangeStrategy != "pin" {
			continue
		}
		for _, pattern := range rule.MatchFileNames {
			if !util.RenovateGlobSupported(pattern) {
				outside = append(outside, pattern)
			}
		}
	}
	return outside
}

// pinnedSections returns the sections of the package.json at rel that some rule pins.
func pinnedSections(rules []renovatePackageRule, rel string, sections []string) []string {
	var pinned []string
	for _, section := range sections {
		if slices.ContainsFunc(rules, func(rule renovatePackageRule) bool { return pinsSection(rule, rel, section) }) {
			pinned = append(pinned, section)
		}
	}
	return pinned
}

// familyManifestPins checks every package.json of family that a pin rule in rules reaches: it
// is pinned in every section the npm manager reads except the range sections. It returns how
// many such manifests it checked.
func familyManifestPins(t *testing.T, rules []renovatePackageRule, family Family) (covered int) {
	t.Helper()
	for _, name := range family.Names() {
		if path.Base(name) != "package.json" {
			continue
		}
		data, err := family.Read(name)
		if err != nil {
			t.Fatal(err)
		}
		rel, sections := family.AssetPath(name), npmSections(t, data)
		pinned := pinnedSections(rules, rel, sections)
		if len(pinned) == 0 {
			continue
		}
		want := slices.DeleteFunc(slices.Clone(sections), func(section string) bool {
			return slices.Contains(renovateRangeSections, section)
		})
		if !slices.Equal(pinned, want) {
			t.Errorf("renovate.json pins %v of %s, want %v: scope the pin with matchDepTypes", pinned, rel, want)
		}
		covered++
	}
	return covered
}

// Positive: every family package.json a Renovate pin rule reaches keeps exact pins in every
// section the npm manager reads except the range sections. The Markdown gate's dependencies and
// its katex override move in the markdown gate lock group, and its engines.node stays the range
// >=22 rather than becoming one Node release that npm ci in an adopter on another Node major
// reports as EBADENGINE (#793).
func TestRenovatePinsFamilyManifestsExceptRanges(t *testing.T) {
	config := readRenovateConfig(t)
	if outside := unevaluablePinPatterns(config.PackageRules); len(outside) != 0 {
		t.Fatalf("pin rules match files by %q, outside the glob subset util.RenovatePatternsCover evaluates", outside)
	}
	covered := 0
	for _, family := range Families() {
		covered += familyManifestPins(t, config.PackageRules, family)
	}
	if covered == 0 {
		t.Fatal("no pin rule in renovate.json reaches a family package.json; the check would pass vacuously")
	}
}

// Negative: a pin rule over the manifest without matchDepTypes, the shape the markdown gate
// lock rule first had, reaches engines and peerDependencies, and a pin rule naming engines
// reaches it through a directory glob.
func TestRenovatePinReachesUnscopedRangeSections(t *testing.T) {
	const rel = "tools/markdownlint/package.json"
	sections := []string{"dependencies", "peerDependencies", "engines", "overrides"}
	unscoped := renovatePackageRule{MatchManagers: []string{"npm"}, MatchFileNames: []string{rel}, RangeStrategy: "pin"}
	if got := pinnedSections([]renovatePackageRule{unscoped}, rel, sections); !slices.Equal(got, sections) {
		t.Fatalf("an unscoped pin rule pins %v, want %v", got, sections)
	}
	named := renovatePackageRule{MatchFileNames: []string{"tools/**"}, MatchDepTypes: []string{"engines"}, RangeStrategy: "pin"}
	if got := pinnedSections([]renovatePackageRule{named}, rel, sections); !slices.Equal(got, []string{"engines"}) {
		t.Fatalf("a pin rule naming engines pins %v", got)
	}
	unevaluable := []renovatePackageRule{
		{MatchFileNames: []string{"/markdownlint/", "!docs/**", "{tools,docs}/**", rel}, RangeStrategy: "pin"},
		{MatchFileNames: []string{"/figures/"}, GroupName: "not a pin rule"},
	}
	want := []string{"/markdownlint/", "!docs/**", "{tools,docs}/**"}
	if got := unevaluablePinPatterns(unevaluable); !slices.Equal(got, want) {
		t.Fatalf("unevaluablePinPatterns = %q, want %q", got, want)
	}
}

// Boundary: a rule that does not pin, names another manager or other files, a glob below the
// file, or only a section the manifest lacks pins nothing; "*", a directory "**" and a "**/" prefix reach the
// file. npmSections reads only the members the npm manager extracts.
func TestRenovatePinSectionsBoundary(t *testing.T) {
	const rel = "tools/markdownlint/package.json"
	sections := []string{"dependencies", "engines"}
	for name, rule := range map[string]renovatePackageRule{
		"auto":          {MatchFileNames: []string{rel}, RangeStrategy: "auto"},
		"unset":         {MatchFileNames: []string{rel}},
		"other manager": {MatchManagers: []string{"pip-compile"}, RangeStrategy: "pin"},
		"other files":   {MatchFileNames: []string{"docs/presets/starlight/**", rel + ".bak", "tools/markdownlint"}, RangeStrategy: "pin"},
		// minimatch, as Renovate calls it, reads a trailing "**" as one or more segments, so a
		// glob below the file does not reach the file itself.
		"below the file": {MatchFileNames: []string{rel + "/**"}, RangeStrategy: "pin"},
		"absent section": {MatchDepTypes: []string{"devDependencies"}, RangeStrategy: "pin"},
	} {
		if got := pinnedSections([]renovatePackageRule{rule}, rel, sections); len(got) != 0 {
			t.Errorf("%s: pins %v", name, got)
		}
	}
	for _, pattern := range []string{"*", "tools/markdownlint/**", "**/package.json"} {
		rule := renovatePackageRule{MatchFileNames: []string{pattern}, MatchDepTypes: []string{"dependencies"}, RangeStrategy: "pin"}
		if got := pinnedSections([]renovatePackageRule{rule}, rel, sections); !slices.Equal(got, []string{"dependencies"}) {
			t.Errorf("pattern %q pins %v", pattern, got)
		}
	}
	manifest := []byte(`{"name": "x", "scripts": {}, "overrides": {"a": "1.0.0"}, "engines": {"node": ">=22"}}`)
	if got := npmSections(t, manifest); !slices.Equal(got, []string{"engines", "overrides"}) {
		t.Fatalf("npmSections = %v", got)
	}
}
