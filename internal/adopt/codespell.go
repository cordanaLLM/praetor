// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/managedasset"

	"github.com/cordanaLLM/praetor/internal/util"
)

// maxCodespellConfigBytes bounds an adopter's setup.cfg, .codespellrc or pyproject.toml read
// for its codespell section (HISS-02).
const maxCodespellConfigBytes = 4 << 20

const (
	maxCodespellLines = 4096
	maxCodespellSkips = 512
)

// CodespellConfigType identifies the configuration file format for codespell.
type CodespellConfigType int

const (
	ConfigTypeNone CodespellConfigType = iota
	ConfigTypeCodespellRC
	ConfigTypeSetupCfg
	ConfigTypePyprojectToml
)

// CodespellConfig holds the detected codespell configuration details.
type CodespellConfig struct {
	Type     CodespellConfigType
	RelPath  string
	FullPath string
}

// DetectCodespellConfig finds the active codespell configuration file in repoPath.
// Priority: .codespellrc, setup.cfg ([codespell]), pyproject.toml ([tool.codespell]).
func DetectCodespellConfig(repoPath string) (CodespellConfig, error) {
	rcPath := filepath.Join(repoPath, ".codespellrc")
	if fileExists(rcPath) {
		return CodespellConfig{Type: ConfigTypeCodespellRC, RelPath: ".codespellrc", FullPath: rcPath}, nil
	}
	cfgPath := filepath.Join(repoPath, "setup.cfg")
	if fileExists(cfgPath) {
		data, err := util.ReadFileLimited(cfgPath, maxCodespellConfigBytes)
		if err == nil && hasIniCodespellSection(string(data)) {
			return CodespellConfig{Type: ConfigTypeSetupCfg, RelPath: "setup.cfg", FullPath: cfgPath}, nil
		}
	}
	tomlPath := filepath.Join(repoPath, "pyproject.toml")
	if fileExists(tomlPath) {
		data, err := util.ReadFileLimited(tomlPath, maxCodespellConfigBytes)
		if err == nil && hasTomlCodespellSection(string(data)) {
			return CodespellConfig{Type: ConfigTypePyprojectToml, RelPath: "pyproject.toml", FullPath: tomlPath}, nil
		}
	}
	return CodespellConfig{Type: ConfigTypeNone}, nil
}

func hasIniCodespellSection(content string) bool {
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines) && i < maxCodespellLines; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "[codespell]" {
			return true
		}
	}
	return false
}

func hasTomlCodespellSection(content string) bool {
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines) && i < maxCodespellLines; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "[tool.codespell]" {
			return true
		}
	}
	return false
}

// IsEngineEmittedPath reports whether relPath is a path managed or emitted by the engine.
func IsEngineEmittedPath(relPath string) bool {
	clean := normalizeCodespellPath(relPath)
	families := managedasset.Families()
	for i := 0; i < len(families) && i < managedasset.MaxFamilies; i++ {
		fam := families[i]
		for _, p := range fam.ManagedPaths() {
			if clean == p {
				return true
			}
		}
		for oldPath := range fam.Moved {
			if clean == oldPath {
				return true
			}
		}
	}
	return false
}

// ParseCodespellSkip extracts the list of skipped items from config content.
func ParseCodespellSkip(content string, cfgType CodespellConfigType) []string {
	lines := strings.Split(content, "\n")
	switch cfgType {
	case ConfigTypeCodespellRC, ConfigTypeSetupCfg:
		return parseIniCodespellSkip(lines)
	case ConfigTypePyprojectToml:
		return parseTomlCodespellSkip(lines)
	default:
		return nil
	}
}

func parseIniCodespellSkip(lines []string) []string {
	inSection := false
	for i := 0; i < len(lines) && i < maxCodespellLines; i++ {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inSection = (line == "[codespell]")
			continue
		}
		if inSection && (strings.HasPrefix(line, "skip =") || strings.HasPrefix(line, "skip:")) {
			return splitCommaSeparatedSkips(extractIniSkipValue(lines, i))
		}
	}
	return nil
}

func extractIniSkipValue(lines []string, start int) string {
	parts := strings.SplitN(lines[start], "=", 2)
	if len(parts) < 2 {
		parts = strings.SplitN(lines[start], ":", 2)
	}
	val := ""
	if len(parts) == 2 {
		val = parts[1]
	}
	for i := start + 1; i < len(lines) && i < maxCodespellLines; i++ {
		raw := lines[i]
		if len(raw) > 0 && (raw[0] == ' ' || raw[0] == '\t') {
			val += "," + strings.TrimSpace(raw)
		} else {
			break
		}
	}
	return val
}

func splitCommaSeparatedSkips(val string) []string {
	raw := strings.Split(val, ",")
	skips := make([]string, 0, len(raw))
	for i := 0; i < len(raw) && i < maxCodespellSkips; i++ {
		item := strings.TrimSpace(raw[i])
		if item != "" && !slices.Contains(skips, item) {
			skips = append(skips, item)
		}
	}
	return skips
}

func parseTomlCodespellSkip(lines []string) []string {
	inSection := false
	for i := 0; i < len(lines) && i < maxCodespellLines; i++ {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inSection = (line == "[tool.codespell]")
			continue
		}
		if inSection && strings.HasPrefix(line, "skip") {
			return parseTomlSkipValue(lines, i)
		}
	}
	return nil
}

func parseTomlSkipValue(lines []string, start int) []string {
	parts := strings.SplitN(lines[start], "=", 2)
	if len(parts) < 2 {
		return nil
	}
	val := strings.TrimSpace(parts[1])
	if strings.HasPrefix(val, "[") {
		return parseTomlArraySkips(lines, start, val)
	}
	val = strings.Trim(val, `"'`)
	return splitCommaSeparatedSkips(val)
}

func parseTomlArraySkips(lines []string, start int, initial string) []string {
	combined := initial
	for i := start + 1; !strings.Contains(combined, "]") && i < len(lines) && i < maxCodespellLines; i++ {
		combined += " " + strings.TrimSpace(lines[i])
	}
	startIdx := strings.Index(combined, "[")
	endIdx := strings.LastIndex(combined, "]")
	if startIdx < 0 || endIdx <= startIdx {
		return nil
	}
	inner := combined[startIdx+1 : endIdx]
	var skips []string
	for _, raw := range strings.Split(inner, ",") {
		item := strings.Trim(strings.TrimSpace(raw), `"'`)
		if item != "" && !slices.Contains(skips, item) {
			skips = append(skips, item)
		}
	}
	return skips
}

// UpdateCodespellSkip rewrites the config content with newSkips.
func UpdateCodespellSkip(content string, cfgType CodespellConfigType, newSkips []string) string {
	lines := strings.Split(content, "\n")
	switch cfgType {
	case ConfigTypeCodespellRC, ConfigTypeSetupCfg:
		return strings.Join(updateIniCodespellSkip(lines, newSkips), "\n")
	case ConfigTypePyprojectToml:
		return strings.Join(updateTomlCodespellSkip(lines, newSkips), "\n")
	default:
		return content
	}
}

func updateIniCodespellSkip(lines []string, newSkips []string) []string {
	sectionIdx, skipStart, skipEnd := findIniSkipRange(lines)
	if sectionIdx < 0 {
		return lines
	}
	return applySkipRangeReplacement(lines, sectionIdx, skipStart, skipEnd, formatIniSkipLine(newSkips))
}

func findIniSkipRange(lines []string) (sectionIdx, skipStart, skipEnd int) {
	sectionIdx, skipStart, skipEnd = -1, -1, -1
	inSection := false
	for i := 0; i < len(lines) && i < maxCodespellLines; i++ {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			if inSection {
				break
			}
			if line == "[codespell]" {
				inSection = true
				sectionIdx = i
			}
			continue
		}
		if inSection && skipStart < 0 && isIniSkipLine(line) {
			skipStart = i
			skipEnd = iniContinuationEnd(lines, i)
		}
	}
	return sectionIdx, skipStart, skipEnd
}

func isIniSkipLine(line string) bool {
	return strings.HasPrefix(line, "skip =") || strings.HasPrefix(line, "skip:")
}

func iniContinuationEnd(lines []string, start int) int {
	end := start + 1
	for j := start + 1; j < len(lines) && j < maxCodespellLines; j++ {
		if len(lines[j]) > 0 && (lines[j][0] == ' ' || lines[j][0] == '\t') {
			end = j + 1
		} else {
			break
		}
	}
	return end
}

func formatIniSkipLine(newSkips []string) string {
	if len(newSkips) == 0 {
		return ""
	}
	return "skip = " + strings.Join(newSkips, ",")
}

func updateTomlCodespellSkip(lines []string, newSkips []string) []string {
	sectionIdx, skipStart, skipEnd, isArray := findTomlSkipRange(lines)
	if sectionIdx < 0 {
		return lines
	}
	return applySkipRangeReplacement(lines, sectionIdx, skipStart, skipEnd, formatTomlSkipLine(newSkips, isArray))
}

func findTomlSection(lines []string) (start, end int) {
	start = -1
	for i := 0; i < len(lines) && i < maxCodespellLines; i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "[") || !strings.HasSuffix(line, "]") {
			continue
		}
		if start >= 0 {
			return start, i
		}
		if line == "[tool.codespell]" {
			start = i
		}
	}
	if start >= 0 {
		return start, len(lines)
	}
	return -1, -1
}

func findTomlSkipInRange(lines []string, start, end int) (skipStart, skipEnd int, isArray bool) {
	for i := start; i < end && i < maxCodespellLines; i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "skip") {
			continue
		}
		isArray = isTomlArraySkipLine(line)
		if isArray {
			return i, tomlArrayEnd(lines, i), true
		}
		return i, i + 1, false
	}
	return -1, -1, false
}

func findTomlSkipRange(lines []string) (sectionIdx, skipStart, skipEnd int, isArray bool) {
	secStart, secEnd := findTomlSection(lines)
	if secStart < 0 {
		return -1, -1, -1, false
	}
	sStart, sEnd, arr := findTomlSkipInRange(lines, secStart, secEnd)
	return secStart, sStart, sEnd, arr
}

func isTomlArraySkipLine(line string) bool {
	parts := strings.SplitN(line, "=", 2)
	return len(parts) == 2 && strings.HasPrefix(strings.TrimSpace(parts[1]), "[")
}

func tomlArrayEnd(lines []string, start int) int {
	for j := start; j < len(lines) && j < maxCodespellLines; j++ {
		if strings.Contains(lines[j], "]") {
			return j + 1
		}
	}
	return start + 1
}

func formatTomlSkipLine(newSkips []string, asArray bool) string {
	if len(newSkips) == 0 {
		return ""
	}
	if asArray {
		return `skip = ["` + strings.Join(newSkips, `", "`) + `"]`
	}
	return `skip = "` + strings.Join(newSkips, ",") + `"`
}

func applySkipRangeReplacement(lines []string, sectionIdx, skipStart, skipEnd int, newLine string) []string {
	if skipStart >= 0 {
		if newLine == "" {
			return append(slices.Clone(lines[:skipStart]), lines[skipEnd:]...)
		}
		res := slices.Clone(lines[:skipStart])
		res = append(res, newLine)
		return append(res, lines[skipEnd:]...)
	}
	if newLine == "" {
		return lines
	}
	res := make([]string, 0, len(lines)+1)
	res = append(res, lines[:sectionIdx+1]...)
	res = append(res, newLine)
	return append(res, lines[sectionIdx+1:]...)
}

func reconcileCodespell(ctx context.Context, s *adoptSession) error {
	cfg, err := DetectCodespellConfig(s.repoPath)
	if err != nil || cfg.Type == ConfigTypeNone {
		return err
	}
	data, err := os.ReadFile(cfg.FullPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", cfg.RelPath, err)
	}
	manifestExceptions := loadSessionExceptions(ctx, s)
	today := time.Now()
	nextSkips := computeNextCodespellSkips(ParseCodespellSkip(string(data), cfg.Type), manifestExceptions, today)
	if !slices.Equal(ParseCodespellSkip(string(data), cfg.Type), nextSkips) {
		updated := UpdateCodespellSkip(string(data), cfg.Type, nextSkips)
		if !s.opts.DryRun {
			if err := util.WriteFileAtomic(cfg.FullPath, []byte(updated), 0o644); err != nil {
				return fmt.Errorf("write %s: %w", cfg.RelPath, err)
			}
		}
	}
	if enabled, err := documentationEnabledForSession(s); err == nil && enabled {
		maybeProposeCodespellExceptions(s, cfg.RelPath, manifestExceptions, today)
	}
	return nil
}

func loadSessionExceptions(ctx context.Context, s *adoptSession) []config.Exception {
	full := filepath.Join(s.repoPath, manifestFile)
	data, err := s.readManifestData(ctx, full)
	if err != nil || len(data) == 0 {
		return nil
	}
	manifest, err := config.DecodeManifest(data)
	if err != nil {
		return nil
	}
	return manifest.Exceptions
}

func computeNextCodespellSkips(current []string, exceptions []config.Exception, today time.Time) []string {
	liveExceptions := liveCodespellExceptionsMap(exceptions, today)
	nextSkips := retainActiveSkips(current, liveExceptions)
	return appendMissingLiveSkips(nextSkips, liveExceptions)
}

func liveCodespellExceptionsMap(exceptions []config.Exception, today time.Time) map[string]bool {
	live := make(map[string]bool)
	for _, e := range config.ExceptionsFor(exceptions, config.ExceptionRuleCodespell) {
		if !e.Expired(today) {
			live[normalizeCodespellPath(e.Path)] = true
		}
	}
	return live
}

func retainActiveSkips(current []string, live map[string]bool) []string {
	var next []string
	for i := 0; i < len(current) && i < maxCodespellSkips; i++ {
		item := current[i]
		clean := normalizeCodespellPath(item)
		if IsEngineEmittedPath(clean) {
			if live[clean] {
				if !strings.HasPrefix(item, "./") {
					item = "./" + clean
				}
				next = append(next, item)
			}
		} else {
			next = append(next, item)
		}
	}
	return next
}

func appendMissingLiveSkips(next []string, live map[string]bool) []string {
	for path := range live {
		if IsEngineEmittedPath(path) && !hasNormalizedSkip(next, path) {
			next = append(next, "./"+path)
		}
	}
	return next
}

func normalizeCodespellPath(p string) string {
	clean := filepath.ToSlash(filepath.Clean(p))
	return strings.TrimPrefix(clean, "./")
}

func hasNormalizedSkip(skips []string, target string) bool {
	norm := normalizeCodespellPath(target)
	for i := 0; i < len(skips) && i < maxCodespellSkips; i++ {
		if normalizeCodespellPath(skips[i]) == norm {
			return true
		}
	}
	return false
}

func maybeProposeCodespellExceptions(s *adoptSession, relConfig string, exceptions []config.Exception, today time.Time) {
	live := make(map[string]bool)
	for _, e := range config.ExceptionsFor(exceptions, config.ExceptionRuleCodespell) {
		if !e.Expired(today) {
			clean := normalizeCodespellPath(e.Path)
			live[clean] = true
		}
	}
	const playerPath = "tools/figures/player/player.js"
	const loaderPath = "tools/figures/player/loader.js"
	if live[playerPath] && live[loaderPath] {
		return
	}
	todayStr := today.Format("2006-01-02")
	expiresStr := today.AddDate(0, 0, 10).Format("2006-01-02")
	proposed := fmt.Sprintf("codespell config detected (%s); to exempt minified figure player bundles, declare in .standards.yaml:\n"+
		"exceptions:\n"+
		"  - rule: codespell\n"+
		"    path: %s\n"+
		"    category: C\n"+
		"    reason: minified third-party interfig bundle\n"+
		"    entered: %s\n"+
		"    expires: %s\n"+
		"  - rule: codespell\n"+
		"    path: %s\n"+
		"    category: C\n"+
		"    reason: minified third-party interfig bundle\n"+
		"    entered: %s\n"+
		"    expires: %s", relConfig, playerPath, todayStr, expiresStr, loaderPath, todayStr, expiresStr)
	fmt.Println(proposed)
	s.report.addWarning("%s", proposed)
}

// AuditCodespell verifies that codespell skips contain only authorized engine-emitted files.
func AuditCodespell(ctx context.Context, rootDir string, exceptions []config.Exception, today time.Time) error {
	cfg, err := DetectCodespellConfig(rootDir)
	if err != nil || cfg.Type == ConfigTypeNone {
		return err
	}
	data, err := os.ReadFile(cfg.FullPath)
	if err != nil {
		return fmt.Errorf("[FAIL] codespell: read %s: %w", cfg.RelPath, err)
	}
	skips := ParseCodespellSkip(string(data), cfg.Type)
	live := make(map[string]config.Exception)
	for _, e := range config.ExceptionsFor(exceptions, config.ExceptionRuleCodespell) {
		if !e.Expired(today) {
			clean := normalizeCodespellPath(e.Path)
			live[clean] = e
			fmt.Printf("[PASS] codespell: %s, excepted until %s by the exceptions entry (rule %s, Category %s): %s\n",
				clean, e.Expires, e.Rule, e.Category, e.Reason)
		}
	}
	for i := 0; i < len(skips) && i < maxCodespellSkips; i++ {
		item := skips[i]
		clean := normalizeCodespellPath(item)
		if IsEngineEmittedPath(clean) {
			if _, ok := live[clean]; !ok {
				return fmt.Errorf("[FAIL] codespell: skip names engine-emitted file %s without a live codespell exception entry", clean)
			}
		}
	}
	return nil
}
