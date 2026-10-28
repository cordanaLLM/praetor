// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package supplychain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// ImageRefRuleID is the HISS rule identifier for unpinned container image references.
const ImageRefRuleID = "HISS-11"

// maxScannedFiles bounds how many repository paths one scan reads (HISS-02).
const maxScannedFiles = 8192

// maxImageRefFileBytes bounds one scanned file (HISS-02): a larger tracked file is refused by
// util.ReadConfinedLimited and fails the scan with its name, never read in full or skipped.
const maxImageRefFileBytes = 16 << 20

// maxFileLines bounds how many lines one file scan processes (HISS-02).
const maxFileLines = 16384

// maxLineLiterals bounds how many string literals one source line scan processes (HISS-02).
const maxLineLiterals = 64

// maxScopeBytes bounds the NUL-separated listing git returns (HISS-02).
const maxScopeBytes = 8 << 20

var (
	// taggedDigest matches "<repository>:<tag>@sha256:<64 hex>", the tag-plus-digest form
	// HISS-11 asks for.
	taggedDigest = regexp.MustCompile(`([A-Za-z0-9][A-Za-z0-9._/-]*):([A-Za-z0-9][A-Za-z0-9._-]*)@sha256:([0-9a-f]{64})`)

	// digestPinPattern matches a valid sha256 digest suffix on a container image reference.
	digestPinPattern = regexp.MustCompile(`@sha256:[0-9a-f]{64}(?:$|\s)`)

	// ociRepoPattern matches a lowercase OCI repository path ("library/golang", "org/runner").
	ociRepoPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._/-][a-z0-9]+)*$`)

	// ociTagPattern matches an OCI tag token ("1.4.2", "latest", "1.27-alpine").
	ociTagPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]{0,127}$`)

	// devcontainerImageField matches `"image": "..."` in devcontainer.json files.
	devcontainerImageField = regexp.MustCompile(`"image"\s*:\s*"([^"]+)"`)

	// quotedLiteralPattern matches double-quoted, single-quoted, or backtick string literals.
	quotedLiteralPattern = regexp.MustCompile(`"([^"\\\n]+)"|'([^'\\\n]+)'|` + "`" + `([^` + "`" + `\n]+)` + "`")
)

// pinnedFileNames are the formats that carry image pins compared across files.
var pinnedFileNames = map[string]bool{".go": true, ".json": true, ".yml": true, ".yaml": true, ".tmpl": true}

// sourceCodeExts are source extensions scanned for fully qualified image references.
var sourceCodeExts = map[string]bool{
	".go": true, ".py": true, ".rs": true,
	".c": true, ".h": true, ".cc": true, ".cpp": true, ".cxx": true, ".hpp": true, ".hh": true,
	".cu": true, ".hip": true, ".mm": true,
	".ts": true, ".tsx": true, ".js": true, ".jsx": true, ".mjs": true, ".cjs": true, ".svelte": true,
}

// ImagePin is one "<image>:<tag>@sha256:<digest>" occurrence and where it was read.
type ImagePin struct {
	File   string
	Image  string
	Digest string
}

// ImageRefFinding is one container image reference that lacks an immutable @sha256: digest pin.
type ImageRefFinding struct {
	File         string
	Line         int
	Reference    string
	RuleID       string
	Fingerprint  string
	Unresolvable bool
}

// ImageRefFingerprint returns the line-independent HISS-11 baseline fingerprint for file and reference.
func ImageRefFingerprint(file, reference string) string {
	return util.NormalizeSlashes(file) + ":" + ImageRefRuleID + ":image:" + strings.TrimSpace(reference)
}

// CollectImagePins extracts every tagged-digest reference from one file's content.
func CollectImagePins(file, content string) []ImagePin {
	matches := taggedDigest.FindAllStringSubmatch(content, -1)
	pins := make([]ImagePin, 0, len(matches))
	for i := 0; i < len(matches) && i < maxFileLines; i++ {
		match := matches[i]
		pins = append(pins, ImagePin{
			File:   file,
			Image:  NormalizeImage(match[1]) + ":" + match[2],
			Digest: match[3],
		})
	}
	return pins
}

// NormalizeImage strips the implicit Docker Hub prefixes so "docker.io/library/golang"
// and "golang" compare as one image.
func NormalizeImage(repository string) string {
	prefixes := []string{"docker.io/library/", "docker.io/", "index.docker.io/library/", "index.docker.io/"}
	for i := 0; i < len(prefixes); i++ {
		if strings.HasPrefix(repository, prefixes[i]) {
			return strings.TrimPrefix(repository, prefixes[i])
		}
	}
	return repository
}

// ConflictingPins groups pins by image:tag and returns only the groups carrying more than
// one digest.
func ConflictingPins(pins []ImagePin) map[string][]ImagePin {
	byImage := make(map[string][]ImagePin, len(pins))
	for i := 0; i < len(pins) && i < maxScannedFiles; i++ {
		pin := pins[i]
		byImage[pin.Image] = append(byImage[pin.Image], pin)
	}
	conflicts := make(map[string][]ImagePin)
	for image, group := range byImage {
		digests := make(map[string]bool, len(group))
		for i := 0; i < len(group) && i < maxScannedFiles; i++ {
			digests[group[i].Digest] = true
		}
		if len(digests) > 1 {
			conflicts[image] = group
		}
	}
	return conflicts
}

// RepositoryFiles asks git which paths belong to root: tracked files plus untracked files
// git is not ignoring.
func RepositoryFiles(ctx context.Context, root string) ([]string, error) {
	out, err := util.RunGitProbe(ctx, root, maxScopeBytes,
		"ls-files", "--cached", "--others", "--exclude-standard", "--deduplicate", "-z", "--", ".")
	if err != nil {
		return nil, fmt.Errorf("enumerate image pin scope in %s: %w", root, err)
	}
	parts := strings.Split(string(out.Stdout), "\x00")
	var files []string
	for i := 0; i < len(parts); i++ {
		rel := parts[i]
		if rel == "" {
			continue
		}
		if len(files) >= maxScannedFiles {
			return nil, fmt.Errorf("image pin scope passed %d files; narrow the listing", maxScannedFiles)
		}
		files = append(files, rel)
	}
	return files, nil
}

// ScannedFile reports whether a repository path is a format that carries image pins.
func ScannedFile(rel string) bool {
	slashed := filepath.ToSlash(rel)
	if isTestdataPath(slashed) {
		return false
	}
	base := filepath.Base(slashed)
	return pinnedFileNames[filepath.Ext(base)] || strings.HasPrefix(base, "Dockerfile")
}

// ScanRepositoryPins collects every tagged-digest reference the repository owns.
func ScanRepositoryPins(ctx context.Context, root string) ([]ImagePin, error) {
	files, err := RepositoryFiles(ctx, root)
	if err != nil {
		return nil, err
	}
	var pins []ImagePin
	for i := 0; i < len(files) && i < maxScannedFiles; i++ {
		rel := files[i]
		if !ScannedFile(rel) {
			continue
		}
		content, readErr := util.ReadConfinedLimited(root, filepath.FromSlash(rel), maxImageRefFileBytes)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return nil, readErr
		}
		pins = append(pins, CollectImagePins(filepath.ToSlash(rel), string(content))...)
	}
	return pins, nil
}

// isTestdataPath reports whether slashed is inside a testdata directory.
func isTestdataPath(slashed string) bool {
	return strings.HasPrefix(slashed, "testdata/") || strings.Contains(slashed, "/testdata/")
}

// ScannedImageRefFile reports whether a repository path is scanned for HISS-11 image references.
func ScannedImageRefFile(rel string) bool {
	slashed := filepath.ToSlash(rel)
	if isTestdataPath(slashed) || strings.HasSuffix(slashed, "_test.go") {
		return false
	}
	return isDockerfile(slashed) || isYAMLFile(slashed) || isDevcontainerJSON(slashed) || isSourceCodeFile(slashed)
}

// ScanImageRefs scans root for container image references lacking an @sha256: digest pin.
func ScanImageRefs(ctx context.Context, root string) ([]ImageRefFinding, error) {
	files, err := imageRefCandidateFiles(ctx, root)
	if err != nil {
		return nil, err
	}
	var findings []ImageRefFinding
	for i := 0; i < len(files) && i < maxScannedFiles; i++ {
		rel := files[i]
		if !ScannedImageRefFile(rel) {
			continue
		}
		content, readErr := util.ReadConfinedLimited(root, filepath.FromSlash(rel), maxImageRefFileBytes)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if errors.Is(readErr, util.ErrPathEscapesRoot) {
			// A link that leaves root is never followed (confinement); it is reported as not
			// verified rather than failing the scan or passing silently.
			findings = append(findings, newImageRefFinding(filepath.ToSlash(rel), 0, imageRefLinkOutside, true))
			continue
		}
		if readErr != nil {
			return nil, readErr
		}
		findings = append(findings, CollectImageRefFindings(filepath.ToSlash(rel), string(content))...)
	}
	return findings, nil
}

// imageRefLinkOutside is the reference text of a scanned path whose link leaves the root.
const imageRefLinkOutside = "link outside the repository, not followed"

// imageRefCandidateFiles lists files under root via git when root is a git worktree, or
// via a bounded directory queue for fixture directories without .git.
func imageRefCandidateFiles(ctx context.Context, root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("stat image scan root %s: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("image scan root %s is not a directory", root)
	}
	if _, gitErr := os.Stat(filepath.Join(root, ".git")); gitErr == nil {
		return RepositoryFiles(ctx, root)
	}
	return walkDirectoryFiles(root)
}

// walkDirectoryFiles enumerates relative file paths under root without recursion (HISS-01, HISS-02).
func walkDirectoryFiles(root string) ([]string, error) {
	dirs := []string{"."}
	var files []string
	for d := 0; d < len(dirs) && d < maxScannedFiles; d++ {
		relDir := dirs[d]
		entries, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(relDir)))
		if err != nil {
			return nil, fmt.Errorf("read directory %s: %w", relDir, err)
		}
		for i := 0; i < len(entries) && i < maxScannedFiles; i++ {
			name := entries[i].Name()
			if name == ".git" || name == ".workingdir" || name == "node_modules" || name == "vendor" {
				continue
			}
			child := name
			if relDir != "." {
				child = relDir + "/" + name
			}
			if entries[i].IsDir() {
				dirs = append(dirs, child)
				continue
			}
			if len(files) >= maxScannedFiles {
				return nil, fmt.Errorf("image scan scope passed %d files", maxScannedFiles)
			}
			files = append(files, child)
		}
	}
	return files, nil
}

// CollectImageRefFindings extracts unpinned container image references from one file's content.
func CollectImageRefFindings(file, content string) []ImageRefFinding {
	slashed := util.NormalizeSlashes(file)
	switch {
	case isDockerfile(slashed):
		return collectDockerfileFindings(slashed, content)
	case isYAMLFile(slashed):
		return collectYAMLFindings(slashed, content)
	case isDevcontainerJSON(slashed):
		return collectDevcontainerFindings(slashed, content)
	case isSourceCodeFile(slashed):
		return collectSourceLiteralFindings(slashed, content)
	default:
		return nil
	}
}

func newImageRefFinding(file string, line int, ref string, unresolvable bool) ImageRefFinding {
	trimmed := strings.TrimSpace(ref)
	return ImageRefFinding{
		File:         util.NormalizeSlashes(file),
		Line:         line,
		Reference:    trimmed,
		RuleID:       ImageRefRuleID,
		Fingerprint:  ImageRefFingerprint(file, trimmed),
		Unresolvable: unresolvable,
	}
}

func hasDigestPin(ref string) bool {
	return digestPinPattern.MatchString(strings.TrimSpace(ref))
}

func isDockerfile(slashed string) bool {
	base := filepath.Base(slashed)
	lower := strings.ToLower(base)
	if strings.HasSuffix(lower, ".tmpl") {
		lower = strings.TrimSuffix(lower, ".tmpl")
	}
	return lower == "dockerfile" || lower == "containerfile" ||
		strings.HasPrefix(lower, "dockerfile.") || strings.HasPrefix(lower, "containerfile.") ||
		strings.HasSuffix(lower, ".dockerfile") || strings.HasSuffix(lower, ".containerfile")
}

func isYAMLFile(slashed string) bool {
	ext := strings.ToLower(filepath.Ext(slashed))
	return ext == ".yml" || ext == ".yaml"
}

func isWorkflowFile(slashed string) bool {
	return strings.HasPrefix(slashed, ".github/workflows/") ||
		strings.Contains(slashed, "/.github/workflows/") ||
		strings.Contains(slashed, "github-actions/")
}

func isDevcontainerJSON(slashed string) bool {
	base := strings.ToLower(filepath.Base(slashed))
	return base == "devcontainer.json" || base == ".devcontainer.json"
}

func isSourceCodeFile(slashed string) bool {
	return sourceCodeExts[strings.ToLower(filepath.Ext(slashed))]
}

type numberedLine struct {
	line int
	text string
}

func collectDockerfileFindings(file, content string) []ImageRefFinding {
	stmts := dockerfileStatements(content)
	stages := make(map[string]bool, len(stmts))
	for i := 0; i < len(stmts) && i < maxFileLines; i++ {
		if target, stage := parseDockerfileFrom(stmts[i].text); target != "" && stage != "" {
			stages[strings.ToLower(stage)] = true
		}
	}
	var findings []ImageRefFinding
	for i := 0; i < len(stmts) && i < maxFileLines; i++ {
		target, _ := parseDockerfileFrom(stmts[i].text)
		if target == "" || strings.EqualFold(target, "scratch") || stages[strings.ToLower(target)] {
			continue
		}
		if hasDigestPin(target) {
			continue
		}
		unresolvable := strings.Contains(target, "$") || strings.Contains(target, "{{")
		findings = append(findings, newImageRefFinding(file, stmts[i].line, target, unresolvable))
	}
	return findings
}

func dockerfileStatements(content string) []numberedLine {
	raw := strings.Split(content, "\n")
	var out []numberedLine
	startLine := 0
	var current strings.Builder
	for i := 0; i < len(raw) && i < maxFileLines; i++ {
		trimmed := strings.TrimSpace(raw[i])
		if current.Len() == 0 {
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			startLine = i + 1
		}
		if strings.HasSuffix(trimmed, `\`) {
			current.WriteString(strings.TrimSpace(strings.TrimSuffix(trimmed, `\`)) + " ")
			continue
		}
		current.WriteString(trimmed)
		out = append(out, numberedLine{line: startLine, text: current.String()})
		current.Reset()
	}
	if current.Len() > 0 {
		out = append(out, numberedLine{line: startLine, text: current.String()})
	}
	return out
}

func parseDockerfileFrom(stmt string) (target, stage string) {
	fields := strings.Fields(stripInlineComment(stmt))
	if len(fields) < 2 || !strings.EqualFold(fields[0], "FROM") {
		return "", ""
	}
	idx := 1
	for idx < len(fields) && strings.HasPrefix(fields[idx], "--") {
		idx++
	}
	if idx >= len(fields) {
		return "", ""
	}
	target = fields[idx]
	for j := idx + 1; j+1 < len(fields) && j < maxLineLiterals; j++ {
		if strings.EqualFold(fields[j], "AS") {
			stage = fields[j+1]
			break
		}
	}
	return target, stage
}

func stripInlineComment(s string) string {
	if idx := strings.Index(s, " #"); idx >= 0 {
		return s[:idx]
	}
	return s
}

func collectYAMLFindings(file, content string) []ImageRefFinding {
	lines := strings.Split(content, "\n")
	workflow := isWorkflowFile(file)
	var findings []ImageRefFinding
	for i := 0; i < len(lines) && i < maxFileLines; i++ {
		ref := extractYAMLImageRef(lines[i], workflow)
		if ref == "" || hasDigestPin(ref) {
			continue
		}
		unresolvable := strings.Contains(ref, "$") || strings.Contains(ref, "{{")
		findings = append(findings, newImageRefFinding(file, i+1, ref, unresolvable))
	}
	return findings
}

func extractYAMLImageRef(line string, workflow bool) string {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "- ") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
	}
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return ""
	}
	key, rest, ok := strings.Cut(trimmed, ":")
	if !ok {
		return ""
	}
	key = strings.TrimSpace(key)
	val := cleanYAMLScalar(rest)
	switch {
	case key == "image":
		return validYAMLScalarRef(val)
	case workflow && key == "uses" && strings.HasPrefix(val, "docker://"):
		return val
	case workflow && key == "container":
		return validYAMLScalarRef(val)
	default:
		return ""
	}
}

func cleanYAMLScalar(raw string) string {
	s := strings.TrimSpace(raw)
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') {
		if end := closingQuoteIndex(s); end > 0 {
			return strings.TrimSpace(s[1:end])
		}
	}
	return strings.Trim(strings.TrimSpace(stripInlineComment(s)), `"'`)
}

func closingQuoteIndex(s string) int {
	quote := s[0]
	for i := 1; i < len(s) && i < maxFileLines; i++ {
		if quote == '"' && s[i] == '\\' {
			i++
			continue
		}
		if s[i] == quote {
			return i
		}
	}
	return -1
}

func validYAMLScalarRef(val string) string {
	if val == "" || val == "|" || val == ">" || val == "{}" || val == "[]" || strings.HasSuffix(val, ":") {
		return ""
	}
	return val
}

func collectDevcontainerFindings(file, content string) []ImageRefFinding {
	lines := strings.Split(content, "\n")
	var findings []ImageRefFinding
	for i := 0; i < len(lines) && i < maxFileLines; i++ {
		m := devcontainerImageField.FindStringSubmatch(lines[i])
		if len(m) < 2 || hasDigestPin(m[1]) {
			continue
		}
		unresolvable := strings.Contains(m[1], "$") || strings.Contains(m[1], "{")
		findings = append(findings, newImageRefFinding(file, i+1, m[1], unresolvable))
	}
	return findings
}

func collectSourceLiteralFindings(file, content string) []ImageRefFinding {
	folded := foldAdjacentStringLines(content)
	var findings []ImageRefFinding
	for i := 0; i < len(folded) && i < maxFileLines; i++ {
		trimmed := strings.TrimSpace(folded[i].text)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			continue
		}
		matches := quotedLiteralPattern.FindAllStringSubmatch(folded[i].text, maxLineLiterals)
		for j := 0; j < len(matches) && j < maxLineLiterals; j++ {
			lit := firstNonEmptySubmatch(matches[j][1:])
			if isUnpinnedQualifiedImageLiteral(lit) {
				findings = append(findings, newImageRefFinding(file, folded[i].line, lit, strings.Contains(lit, "$")))
			}
		}
	}
	return findings
}

func firstNonEmptySubmatch(groups []string) string {
	for i := 0; i < len(groups); i++ {
		if groups[i] != "" {
			return groups[i]
		}
	}
	return ""
}

// foldAdjacentStringLines joins adjacent lines where a double-quoted string literal is split
// across lines via "+" or implicit concatenation, preserving the starting 1-based line number.
func foldAdjacentStringLines(content string) []numberedLine {
	raw := strings.Split(content, "\n")
	out := make([]numberedLine, 0, len(raw))
	for i := 0; i < len(raw) && i < maxFileLines; i++ {
		lineNum := i + 1
		combined := raw[i]
		for i+1 < len(raw) && i+1 < maxFileLines && continuesQuotedLiteral(combined, raw[i+1]) {
			combined = joinContinuedLiteral(combined, raw[i+1])
			i++
		}
		out = append(out, numberedLine{line: lineNum, text: combined})
	}
	return out
}

func continuesQuotedLiteral(curr, next string) bool {
	right := strings.TrimSpace(curr)
	left := strings.TrimSpace(next)
	if !strings.HasPrefix(left, `"`) {
		return false
	}
	if strings.HasSuffix(right, `+`) {
		right = strings.TrimSpace(strings.TrimSuffix(right, `+`))
	}
	return strings.HasSuffix(right, `"`)
}

func joinContinuedLiteral(curr, next string) string {
	right := strings.TrimSpace(curr)
	if strings.HasSuffix(right, `+`) {
		right = strings.TrimSpace(strings.TrimSuffix(right, `+`))
	}
	right = strings.TrimSuffix(right, `"`)
	left := strings.TrimPrefix(strings.TrimSpace(next), `"`)
	return right + left
}

// isUnpinnedQualifiedImageLiteral reports whether lit is a fully qualified container image
// reference ("<registry-host>/<repo>:<tag>") lacking an @sha256: digest pin. Plain host:port
// values without "/<repo>:<tag>" never match.
func isUnpinnedQualifiedImageLiteral(lit string) bool {
	if lit == "" || strings.ContainsAny(lit, " \t\r\n") || strings.Contains(lit, "://") {
		return false
	}
	host, rest, ok := strings.Cut(lit, "/")
	if !ok || !isRegistryHost(host) || hasDigestPin(rest) {
		return false
	}
	repo, tag, hasTag := strings.Cut(rest, ":")
	if !hasTag {
		return false
	}
	return ociRepoPattern.MatchString(repo) && ociTagPattern.MatchString(tag)
}

func isRegistryHost(host string) bool {
	if host == "" {
		return false
	}
	hostname, port, hasPort := strings.Cut(host, ":")
	if hasPort && !isNumericPort(port) {
		return false
	}
	if !isHostnameToken(hostname) {
		return false
	}
	return strings.Contains(hostname, ".") || hasPort || hostname == "localhost"
}

func isNumericPort(port string) bool {
	if len(port) == 0 || len(port) > 5 {
		return false
	}
	for i := 0; i < len(port); i++ {
		if port[i] < '0' || port[i] > '9' {
			return false
		}
	}
	return true
}

func isHostnameToken(h string) bool {
	if len(h) == 0 || h[0] == '.' || h[0] == '-' || h[len(h)-1] == '.' || h[len(h)-1] == '-' {
		return false
	}
	for i := 0; i < len(h) && i < maxLineLiterals; i++ {
		c := h[i]
		isAlphaNum := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
		if !isAlphaNum && c != '.' && c != '-' {
			return false
		}
	}
	return true
}
