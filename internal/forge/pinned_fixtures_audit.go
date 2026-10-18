// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// pinnedFixturesGate names the HISS-21 pinned fixture check on every line it prints.
	pinnedFixturesGate = "Platform neutrality (HISS-21)"

	// Bounds (HISS-02).
	maxTrackedFiles         = 20000
	maxTrackedListingBytes  = 16 * 1024 * 1024
	maxTestFileReadBytes    = 4 * 1024 * 1024
	maxFixtureFileReadBytes = 8 * 1024 * 1024
	maxAttrOutputBytes      = 8 * 1024 * 1024
	pinnedFixturesTimeout   = 60 * time.Second
)

var (
	// hex64Regex matches a 64-character hexadecimal SHA-256 literal.
	hex64Regex = regexp.MustCompile(`\b[0-9a-fA-F]{64}\b`)
)

// PinnedFixtureEOLOptions is what AuditPinnedFixturesEOL reads: the repository root, the
// manifest's exceptions list (the check reads entries of rule HISS-21) and the day
// those entries' expiry is judged against.
type PinnedFixtureEOLOptions struct {
	Root       string
	Exceptions []config.Exception
	Today      time.Time
}

type pinnedFixture struct {
	path     string
	testFile string
}

// AuditPinnedFixturesEOL checks that every pinned text fixture has an explicit eol rule
// or unset text (-text, binary) in .gitattributes (HISS-21).
func AuditPinnedFixturesEOL(ctx context.Context, opts PinnedFixtureEOLOptions) (string, error) {
	if ctx == nil {
		return "", errors.New("[FAIL] " + pinnedFixturesGate + ": the check requires a context")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, pinnedFixturesTimeout)
	defer cancel()

	tracked, err := listTrackedFiles(ctx, opts.Root)
	if err != nil {
		return "", fmt.Errorf("[FAIL] %s: cannot list tracked files: %w", pinnedFixturesGate, err)
	}

	testFiles, testContents, pinnedHashes, err := scanTestFiles(opts.Root, tracked)
	if err != nil {
		return "", fmt.Errorf("[FAIL] %s: cannot scan test files: %w", pinnedFixturesGate, err)
	}

	pinned, err := findPinnedFixtures(opts.Root, tracked, testFiles, testContents, pinnedHashes)
	if err != nil {
		return "", fmt.Errorf("[FAIL] %s: cannot find pinned fixtures: %w", pinnedFixturesGate, err)
	}
	if len(pinned) == 0 {
		return "[PASS] " + pinnedFixturesGate, nil
	}

	unpassed, err := checkAttributes(ctx, opts.Root, pinned)
	if err != nil {
		return "", fmt.Errorf("[FAIL] %s: %w", pinnedFixturesGate, err)
	}
	return evaluateFindings(unpassed, opts.Exceptions, opts.Today)
}

func listTrackedFiles(ctx context.Context, root string) ([]string, error) {
	result, err := util.RunGitProbeWithin(ctx, root, maxTrackedListingBytes, pinnedFixturesTimeout, "ls-files", "-z", "--cached", "--", ".")
	if err != nil {
		return nil, err
	}
	raw := bytes.Split(bytes.TrimSuffix(result.Stdout, []byte{0}), []byte{0})
	if len(raw) > maxTrackedFiles {
		return nil, fmt.Errorf("%s tracks more than %d files", root, maxTrackedFiles)
	}
	tracked := make([]string, 0, len(raw))
	for i := 0; i < len(raw) && i < maxTrackedFiles; i++ {
		if len(raw[i]) > 0 {
			tracked = append(tracked, filepath.ToSlash(string(raw[i])))
		}
	}
	slices.Sort(tracked)
	return slices.Compact(tracked), nil
}

func isTestFile(root, rel string) bool {
	if strings.HasSuffix(rel, "_test.go") {
		return true
	}
	base := path.Base(rel)
	if (strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py")) || strings.HasSuffix(base, "_test.py") {
		return true
	}
	if strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") {
		return true
	}
	if strings.HasSuffix(rel, ".rs") {
		return isRustTest(root, rel)
	}
	return false
}

func isRustTest(root, rel string) bool {
	if strings.HasPrefix(rel, "tests/") || strings.Contains(rel, "/tests/") {
		return true
	}
	data, err := util.ReadFileLimited(filepath.Join(root, rel), 64*1024)
	if err != nil {
		return false
	}
	return bytes.Contains(data, []byte("#[cfg(test)]"))
}

func scanTestFiles(root string, tracked []string) ([]string, map[string]string, map[string]string, error) {
	testFiles := make([]string, 0, len(tracked)/4)
	for i := 0; i < len(tracked) && i < maxTrackedFiles; i++ {
		if isTestFile(root, tracked[i]) {
			testFiles = append(testFiles, tracked[i])
		}
	}
	testContents := make(map[string]string, len(testFiles))
	pinnedHashes := make(map[string]string)
	for i := 0; i < len(testFiles) && i < maxTrackedFiles; i++ {
		data, err := util.ReadFileLimited(filepath.Join(root, testFiles[i]), maxTestFileReadBytes)
		if err != nil {
			continue
		}
		text := string(data)
		testContents[testFiles[i]] = text
		for _, match := range hex64Regex.FindAllString(text, -1) {
			pinnedHashes[strings.ToLower(match)] = testFiles[i]
		}
	}
	return testFiles, testContents, pinnedHashes, nil
}

func findPinnedFixtures(root string, tracked []string, testFiles []string, testContents map[string]string, pinnedHashes map[string]string) ([]pinnedFixture, error) {
	var pinned []pinnedFixture
	for i := 0; i < len(tracked) && i < maxTrackedFiles; i++ {
		f := tracked[i]
		if isTestFile(root, f) {
			continue
		}
		if match, ok := matchFixture(root, f, testFiles, testContents, pinnedHashes); ok {
			pinned = append(pinned, match)
		}
	}
	return pinned, nil
}

func matchFixture(root, f string, testFiles []string, testContents map[string]string, pinnedHashes map[string]string) (pinnedFixture, bool) {
	data, err := util.ReadFileLimited(filepath.Join(root, f), maxFixtureFileReadBytes)
	if err == nil {
		sum := sha256.Sum256(data)
		h := hex.EncodeToString(sum[:])
		if test, ok := pinnedHashes[h]; ok {
			return pinnedFixture{path: f, testFile: test}, true
		}
	}
	if isFixtureCandidate(f) {
		if test := findTestNamingPath(f, testFiles, testContents); test != "" {
			return pinnedFixture{path: f, testFile: test}, true
		}
	}
	return pinnedFixture{}, false
}

func isFixtureCandidate(rel string) bool {
	return strings.HasSuffix(rel, ".golden") ||
		strings.HasPrefix(rel, "testdata/") || strings.Contains(rel, "/testdata/") ||
		strings.HasPrefix(rel, "fixtures/") || strings.Contains(rel, "/fixtures/")
}

func findTestNamingPath(fixture string, testFiles []string, testContents map[string]string) string {
	subpath := fixtureSubpath(fixture)
	isGolden := strings.HasSuffix(fixture, ".golden")
	baseGolden := path.Base(fixture)

	for i := 0; i < len(testFiles) && i < maxTrackedFiles; i++ {
		tf := testFiles[i]
		content := testContents[tf]
		if testNamesPath(content, tf, fixture, subpath, baseGolden, isGolden) {
			return tf
		}
	}
	return ""
}

func testNamesPath(content, testPath, fixture, subpath, baseGolden string, isGolden bool) bool {
	if strings.Contains(content, fixture) ||
		(subpath != "" && strings.Contains(content, subpath)) ||
		(isGolden && strings.Contains(content, baseGolden)) {
		return true
	}
	rel, err := filepath.Rel(filepath.Dir(testPath), fixture)
	if err == nil && !strings.HasPrefix(rel, "..") {
		return strings.Contains(content, filepath.ToSlash(rel))
	}
	return false
}

func fixtureSubpath(rel string) string {
	for _, marker := range []string{"testdata/", "fixtures/"} {
		if idx := strings.Index(rel, marker); idx >= 0 {
			return rel[idx:]
		}
	}
	return ""
}

func checkAttributes(ctx context.Context, root string, pinned []pinnedFixture) ([]pinnedFixture, error) {
	var paths []string
	for i := 0; i < len(pinned) && i < maxTrackedFiles; i++ {
		paths = append(paths, pinned[i].path)
	}
	stdinData := []byte(strings.Join(paths, "\x00") + "\x00")
	stdinCtx, err := util.WithCommandStdin(ctx, stdinData)
	if err != nil {
		return nil, fmt.Errorf("stdin for check-attr: %w", err)
	}
	res, err := util.RunGitProbeWithin(stdinCtx, root, maxAttrOutputBytes, pinnedFixturesTimeout, "check-attr", "--stdin", "-z", "text", "eol")
	if err != nil {
		return nil, fmt.Errorf("git check-attr in %s: %w", root, err)
	}
	attrMap, err := parseCheckAttrOutput(res.Stdout)
	if err != nil {
		return nil, err
	}
	var unpassed []pinnedFixture
	for i := 0; i < len(pinned) && i < maxTrackedFiles; i++ {
		attrs := attrMap[pinned[i].path]
		if !passesAttribute(attrs["text"], attrs["eol"]) {
			unpassed = append(unpassed, pinned[i])
		}
	}
	return unpassed, nil
}

func passesAttribute(textVal, eolVal string) bool {
	return textVal == "unset" || eolVal == "lf" || eolVal == "crlf"
}

func parseCheckAttrOutput(out []byte) (map[string]map[string]string, error) {
	fields := strings.Split(string(out), "\x00")
	if len(fields)%3 != 1 || fields[len(fields)-1] != "" {
		return nil, errors.New("git check-attr answer is not a sequence of NUL-terminated records")
	}
	result := make(map[string]map[string]string)
	for i := 0; i+2 < len(fields) && i < maxTrackedFiles*3; i += 3 {
		p, attr, val := fields[i], fields[i+1], fields[i+2]
		if _, ok := result[p]; !ok {
			result[p] = make(map[string]string, 2)
		}
		result[p][attr] = val
	}
	return result, nil
}

func evaluateFindings(unpassed []pinnedFixture, exceptions []config.Exception, today time.Time) (string, error) {
	entries := config.ExceptionsFor(exceptions, config.ExceptionRulePlatformNeutrality)
	used := make([]bool, len(entries))
	var findings, excepted []string

	for i := 0; i < len(unpassed) && i < maxTrackedFiles; i++ {
		u := unpassed[i]
		live, expired := config.ExceptionFor(entries, u.path, today, used)
		if live != nil {
			excepted = append(excepted, fmt.Sprintf("[PASS] %s: %s excepted until %s: %s", pinnedFixturesGate, u.path, live.Expires, live.Reason))
			continue
		}
		if expired != nil {
			findings = append(findings, fmt.Sprintf("[FAIL] %s: the exceptions entry for %s expired %s; add an eol rule in .gitattributes or renew the entry with a reason", pinnedFixturesGate, u.path, expired.Expires))
		}
		findings = append(findings, formatFixtureFinding(u.path, u.testFile))
	}
	for i := 0; i < len(entries) && i < config.MaxExceptions; i++ {
		if !used[i] {
			findings = append(findings, fmt.Sprintf("[FAIL] %s: the exceptions entry for %s excuses nothing, because the fixture has an eol rule; remove it", pinnedFixturesGate, entries[i].Target()))
		}
	}
	if len(findings) > 0 {
		return "", errors.New(strings.Join(findings, "\n"))
	}
	return strings.Join(append(excepted, "[PASS] "+pinnedFixturesGate), "\n"), nil
}

func formatFixtureFinding(rel, testPath string) string {
	ext := filepath.Ext(rel)
	var fix string
	if ext != "" && ext != "." {
		fix = fmt.Sprintf("'%s -text' or '*%s eol=lf'", rel, ext)
	} else {
		fix = fmt.Sprintf("'%s -text' or '%s eol=lf'", rel, rel)
	}
	return fmt.Sprintf("[FAIL] %s: %s is pinned by %s without an eol rule in .gitattributes; add %s", pinnedFixturesGate, rel, testPath, fix)
}
