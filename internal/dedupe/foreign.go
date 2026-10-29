package dedupe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	maxJscpdReportBytes = 16 << 20
	maxForeignScanFiles = 100000
	maxDuplicatePairs   = 100000
	jscpdVersionTimeout = 10 * time.Second
	jscpdScanTimeout    = 120 * time.Second
	jscpdMinTokens      = 50
	jscpdMinLines       = 5
	jscpdBinaryEnv      = "PRAETOR_JSCPD_BIN"
)

type rootMapping struct {
	raw string
	abs string
}

type jscpdConfig struct {
	FormatsExts map[string][]string `json:"formatsExts"`
	MinTokens   int                 `json:"minTokens"`
	MinLines    int                 `json:"minLines"`
	Reporters   []string            `json:"reporters"`
	Output      string              `json:"output"`
	Absolute    bool                `json:"absolute"`
}

type rawJscpdReport struct {
	Duplicates []rawJscpdDuplicate `json:"duplicates"`
}

type rawJscpdFile struct {
	Name  string `json:"name"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

type rawJscpdDuplicate struct {
	Format     string       `json:"format"`
	Lines      int          `json:"lines"`
	Tokens     int          `json:"tokens"`
	FirstFile  rawJscpdFile `json:"firstFile"`
	SecondFile rawJscpdFile `json:"secondFile"`
}

func scanForeign(ctx context.Context, repoPath string, acrossRoots []string, scope sourceScope, report *DedupeReport) error {
	if ctx == nil {
		return fmt.Errorf("dedupe scan requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	roots, mainAbs, isCrossRoot, err := normalizeScanRoots(repoPath, acrossRoots)
	if err != nil {
		return fmt.Errorf("normalize scan roots: %w", err)
	}
	files, err := collectForeignFiles(ctx, repoPath, mainAbs, roots, scope, isCrossRoot)
	if err != nil {
		return fmt.Errorf("collect foreign files: %w", err)
	}
	if len(files) == 0 {
		return nil
	}
	bin, scanner, skipped := resolveJscpdBinary(ctx, repoPath)
	if skipped != "" {
		report.ForeignSkipped = skipped
		return nil
	}
	report.ForeignScanner = scanner

	data, err := executeJscpd(ctx, repoPath, bin, files)
	if err != nil {
		return fmt.Errorf("execute jscpd: %w", err)
	}
	duplicates, err := parseJscpdReport(data)
	if err != nil {
		return fmt.Errorf("parse jscpd report: %w", err)
	}
	groups := mergeDuplicatePairs(duplicates, roots, isCrossRoot)
	report.ForeignDuplicates = filterDuplicateGroups(groups, repoPath, isCrossRoot)
	updateForeignScannedLanguages(scope, report)
	return nil
}

func normalizeScanRoots(repoPath string, acrossRoots []string) ([]rootMapping, string, bool, error) {
	absRepo, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, "", false, fmt.Errorf("resolve repo root %q: %w", repoPath, err)
	}
	cleanMain := filepath.Clean(absRepo)
	mainMapping := rootMapping{raw: repoPath, abs: cleanMain}
	roots := []rootMapping{mainMapping}
	seen := map[string]bool{cleanMain: true}

	for i := 0; i < len(acrossRoots) && i < 1000; i++ {
		r := acrossRoots[i]
		absR, err := filepath.Abs(r)
		if err != nil {
			return nil, "", false, fmt.Errorf("resolve across root %q: %w", r, err)
		}
		cleanR := filepath.Clean(absR)
		if !seen[cleanR] {
			seen[cleanR] = true
			roots = append(roots, rootMapping{raw: r, abs: cleanR})
		}
	}
	isCrossRoot := len(roots) > 1
	sort.Slice(roots, func(i, j int) bool {
		return len(roots[i].abs) > len(roots[j].abs)
	})
	return roots, cleanMain, isCrossRoot, nil
}

func collectForeignFiles(ctx context.Context, repoPath, mainAbs string, roots []rootMapping, scope sourceScope, isCrossRoot bool) ([]string, error) {
	files := make([]string, 0, len(scope.foreignFiles))
	for i := 0; i < len(scope.foreignFiles) && i < maxForeignScanFiles; i++ {
		files = append(files, filepath.Join(mainAbs, scope.foreignFiles[i]))
	}
	if isCrossRoot {
		var err error
		files, err = collectAcrossRootsFiles(ctx, mainAbs, roots, files)
		if err != nil {
			return nil, err
		}
	}
	if len(files) > maxForeignScanFiles {
		return nil, fmt.Errorf("foreign files count %d exceeds limit %d", len(files), maxForeignScanFiles)
	}
	return files, nil
}

func collectAcrossRootsFiles(ctx context.Context, mainAbs string, roots []rootMapping, files []string) ([]string, error) {
	for i := 0; i < len(roots) && i < 1000; i++ {
		r := roots[i]
		if r.abs == mainAbs {
			continue
		}
		rScope, err := sourceFiles(ctx, r.raw)
		if err != nil {
			return nil, fmt.Errorf("scan root %s: %w", r.raw, err)
		}
		for j := 0; j < len(rScope.foreignFiles) && j < maxForeignScanFiles; j++ {
			files = append(files, filepath.Join(r.abs, rScope.foreignFiles[j]))
		}
	}
	return files, nil
}

func resolveJscpdBinary(ctx context.Context, dir string) (string, string, string) {
	bin := os.Getenv(jscpdBinaryEnv)
	if bin == "" {
		bin = "jscpd"
	}
	versionCtx, cancel := context.WithTimeout(ctx, jscpdVersionTimeout)
	defer cancel()

	expected := "jscpd " + JscpdVersion
	out, err := util.RunCommandBytes(versionCtx, dir, bin, 4096, "--version")
	if err != nil {
		// order engine-dedupe-jscpd-skip-fails-closed-20261027: binary path, error, wanted version, install hint.
		return "", "", fmt.Sprintf("resolve jscpd %q: %v (want %q; install with `npm ci --prefix tools/jscpd` or set %s)",
			bin, err, expected, jscpdBinaryEnv)
	}
	version := strings.TrimSpace(string(out.Stdout))
	if version != expected {
		// order engine-dedupe-jscpd-skip-fails-closed-20261027: found and wanted version both quoted.
		return "", "", fmt.Sprintf("unexpected jscpd version: %q, want %q", version, expected)
	}
	return bin, version, ""
}

func executeJscpd(ctx context.Context, dir, bin string, files []string) (reportData []byte, retErr error) {
	tmpDir, err := os.MkdirTemp("", "praetor-jscpd-*")
	if err != nil {
		return nil, fmt.Errorf("create jscpd temp dir: %w", err)
	}
	defer func() {
		if rmErr := os.RemoveAll(tmpDir); rmErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("remove jscpd temp dir: %w", rmErr))
		}
	}()

	outDir := filepath.Join(tmpDir, "out")
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return nil, fmt.Errorf("create jscpd output dir: %w", err)
	}

	cfgPath := filepath.Join(tmpDir, "jscpd.json")
	if err := writeJscpdConfig(cfgPath, outDir); err != nil {
		return nil, err
	}

	args := make([]string, 0, len(files)+3)
	args = append(args, "-c", cfgPath, "-s")
	args = append(args, files...)

	scanCtx, cancel := context.WithTimeout(ctx, jscpdScanTimeout)
	defer cancel()

	if _, err := util.RunCommandBytes(scanCtx, dir, bin, maxJscpdReportBytes, args...); err != nil {
		return nil, fmt.Errorf("run jscpd: %w", err)
	}

	reportPath := filepath.Join(outDir, "jscpd-report.json")
	data, err := util.ReadFileLimited(reportPath, maxJscpdReportBytes)
	if err != nil {
		return nil, fmt.Errorf("read jscpd report: %w", err)
	}
	return data, nil
}

func writeJscpdConfig(path, outputDir string) error {
	cfg := jscpdConfig{
		FormatsExts: map[string][]string{
			"cpp": {"cpp", "cc", "cxx", "hpp", "cu", "cuh"},
		},
		MinTokens: jscpdMinTokens,
		MinLines:  jscpdMinLines,
		Reporters: []string{"json"},
		Output:    outputDir,
		Absolute:  true,
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal jscpd config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write jscpd config: %w", err)
	}
	return nil
}

func parseJscpdReport(data []byte) ([]rawJscpdDuplicate, error) {
	var rep rawJscpdReport
	if err := json.Unmarshal(data, &rep); err != nil {
		return nil, fmt.Errorf("unmarshal jscpd report: %w", err)
	}
	return rep.Duplicates, nil
}

func rangesOverlap(f1, f2 rawJscpdFile, lines int) bool {
	e1 := f1.End
	if e1 == 0 {
		e1 = f1.Start + lines - 1
	}
	e2 := f2.End
	if e2 == 0 {
		e2 = f2.Start + lines - 1
	}
	return f1.Start <= e2 && f2.Start <= e1
}

func duplicatePairsShareFragment(p1, p2 rawJscpdDuplicate) bool {
	if p1.Format != p2.Format || p1.Lines != p2.Lines {
		return false
	}
	if filepath.Clean(p1.FirstFile.Name) != filepath.Clean(p2.FirstFile.Name) {
		return false
	}
	return rangesOverlap(p1.FirstFile, p2.FirstFile, p1.Lines)
}

func findRoot(parent []int, x int) int {
	curr := x
	for step := 0; step < len(parent) && step < maxDuplicatePairs; step++ {
		if parent[curr] == curr {
			return curr
		}
		curr = parent[curr]
	}
	return curr
}

func unionRoots(parent []int, x, y int) {
	rx := findRoot(parent, x)
	ry := findRoot(parent, y)
	if rx != ry {
		parent[ry] = rx
	}
}

func mergeDuplicatePairs(duplicates []rawJscpdDuplicate, roots []rootMapping, isCrossRoot bool) []DuplicateGroup {
	n := len(duplicates)
	if n == 0 {
		return nil
	}
	if n > maxDuplicatePairs {
		n = maxDuplicatePairs
	}
	parent := make([]int, n)
	for i := 0; i < n; i++ {
		parent[i] = i
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if duplicatePairsShareFragment(duplicates[i], duplicates[j]) {
				unionRoots(parent, i, j)
			}
		}
	}
	components := make(map[int][]rawJscpdDuplicate)
	for i := 0; i < n; i++ {
		r := findRoot(parent, i)
		components[r] = append(components[r], duplicates[i])
	}
	groups := make([]DuplicateGroup, 0, len(components))
	for _, comp := range components {
		g := buildDuplicateGroup(comp, roots, isCrossRoot)
		if len(g.Locations) < 2 {
			continue
		}
		groups = append(groups, g)
	}
	sortDuplicateGroups(groups)
	return groups
}

func sortDuplicateGroups(groups []DuplicateGroup) {
	sort.Slice(groups, func(i, j int) bool {
		if groups[i].Language != groups[j].Language {
			return groups[i].Language < groups[j].Language
		}
		if groups[i].LOC != groups[j].LOC {
			return groups[i].LOC > groups[j].LOC
		}
		return groups[i].Hash < groups[j].Hash
	})
}

func buildDuplicateGroup(dups []rawJscpdDuplicate, roots []rootMapping, isCrossRoot bool) DuplicateGroup {
	locMap := make(map[string]FileLocation)
	lines := dups[0].Lines
	format := dups[0].Format

	for i := 0; i < len(dups) && i < maxDuplicatePairs; i++ {
		d := dups[i]
		loc1 := resolveFileLocation(d.FirstFile, roots, isCrossRoot)
		loc2 := resolveFileLocation(d.SecondFile, roots, isCrossRoot)
		key1 := fmt.Sprintf("%s\x00%s\x00%d", loc1.Root, loc1.Path, loc1.Line)
		key2 := fmt.Sprintf("%s\x00%s\x00%d", loc2.Root, loc2.Path, loc2.Line)
		if key1 == key2 {
			continue
		}
		locMap[key1] = loc1
		locMap[key2] = loc2
	}

	locations := make([]FileLocation, 0, len(locMap))
	for _, loc := range locMap {
		locations = append(locations, loc)
	}
	sortLocations(locations)

	lang := determineLanguage(format, locations)
	hash := computeDuplicateHash(lang, locations)

	return DuplicateGroup{
		Language:  lang,
		Hash:      hash,
		LOC:       lines,
		Locations: locations,
	}
}

func sortLocations(locations []FileLocation) {
	sort.Slice(locations, func(i, j int) bool {
		if locations[i].Root != locations[j].Root {
			return locations[i].Root < locations[j].Root
		}
		if locations[i].Path != locations[j].Path {
			return locations[i].Path < locations[j].Path
		}
		return locations[i].Line < locations[j].Line
	})
}

func determineLanguage(format string, locations []FileLocation) string {
	if len(locations) > 0 {
		if l := sourceLanguageOf(locations[0].Path); l != "" {
			return l
		}
	}
	return format
}

func computeDuplicateHash(language string, locations []FileLocation) string {
	h := sha256.New()
	if _, err := h.Write([]byte(language + "\n")); err != nil {
		return ""
	}
	for i := 0; i < len(locations) && i < maxDuplicatePairs; i++ {
		loc := locations[i]
		if _, err := fmt.Fprintf(h, "%s:%s:%d\n", loc.Root, loc.Path, loc.Line); err != nil {
			return ""
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

func resolveFileLocation(file rawJscpdFile, roots []rootMapping, isCrossRoot bool) FileLocation {
	absPath := filepath.Clean(file.Name)
	for i := 0; i < len(roots) && i < 1000; i++ {
		r := roots[i]
		if absPath == r.abs || strings.HasPrefix(absPath, r.abs+string(filepath.Separator)) {
			rel, err := filepath.Rel(r.abs, absPath)
			if err == nil {
				path := filepath.ToSlash(rel)
				rootName := ""
				if isCrossRoot {
					rootName = r.raw
				}
				return FileLocation{
					Path: path,
					Line: file.Start,
					Root: rootName,
				}
			}
		}
	}
	return FileLocation{
		Path: filepath.ToSlash(file.Name),
		Line: file.Start,
	}
}

func filterDuplicateGroups(groups []DuplicateGroup, mainRoot string, isCrossRoot bool) []DuplicateGroup {
	if !isCrossRoot {
		return groups
	}
	filtered := make([]DuplicateGroup, 0, len(groups))
	for i := 0; i < len(groups) && i < maxDuplicatePairs; i++ {
		if len(groups[i].Locations) >= 2 && shouldKeepGroup(groups[i], mainRoot) {
			filtered = append(filtered, groups[i])
		}
	}
	return filtered
}

func classifyForeignDuplicates(mainRoot string, exceptions []config.Exception, today time.Time, used []bool, report *DedupeReport) {
	groups := report.ForeignDuplicates
	report.ForeignDuplicates = nil
	for i := 0; i < len(groups) && i < maxDuplicatePairs; i++ {
		group, allExcepted := classifyDuplicateGroup(groups[i], mainRoot, exceptions, today, used)
		if allExcepted {
			report.ForeignExcepted = append(report.ForeignExcepted, group)
		} else {
			report.ForeignDuplicates = append(report.ForeignDuplicates, group)
		}
	}
}

func shouldKeepGroup(g DuplicateGroup, mainRoot string) bool {
	roots := make(map[string]struct{})
	for j := 0; j < len(g.Locations) && j < maxDuplicatePairs; j++ {
		roots[g.Locations[j].Root] = struct{}{}
	}
	if len(roots) >= 2 {
		return true
	}
	_, inMain := roots[mainRoot]
	return inMain
}

// updateForeignScannedLanguages records the languages and file counts scanned by jscpd
// in ForeignScanned. Languages remain in Unscanned because foreign groups are reported, not gated.
func updateForeignScannedLanguages(scope sourceScope, report *DedupeReport) {
	if len(scope.foreignFiles) == 0 {
		return
	}
	scannedLangs := make(map[string]bool)
	for i := 0; i < len(scope.foreignFiles) && i < maxForeignScanFiles; i++ {
		l := sourceLanguageOf(scope.foreignFiles[i])
		if l != "" {
			scannedLangs[l] = true
		}
	}
	for lang := range scannedLangs {
		if count, ok := report.Unscanned[lang]; ok {
			if report.ForeignScanned == nil {
				report.ForeignScanned = make(map[string]int)
			}
			report.ForeignScanned[lang] = count
		}
	}
}
