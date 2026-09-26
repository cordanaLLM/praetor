package bump

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/cordanaLLM/praetor/internal/semver"
	"github.com/cordanaLLM/praetor/internal/util"
)

// ApplyUpdate updates a single dependency in repoPath according to its candidate spec.
func ApplyUpdate(ctx context.Context, repoPath string, cand UpgradeCandidate) error {
	return applyUpdateInternal(ctx, repoPath, cand, true)
}

func applyUpdateInternal(ctx context.Context, repoPath string, cand UpgradeCandidate, tidy bool) error {
	if ctx == nil {
		return fmt.Errorf("update requires context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := util.ValidateExecArg(cand.Package); err != nil {
		return fmt.Errorf("invalid package: %w", err)
	}
	if err := util.ValidateExecArg(cand.TargetVersion); err != nil {
		return fmt.Errorf("invalid target version: %w", err)
	}
	targetDir, err := util.ConfinePath(repoPath, cand.ModuleDir)
	if err != nil {
		return fmt.Errorf("invalid module directory: %w", err)
	}

	switch cand.ManifestType {
	case "go.mod":
		return applyGoUpdate(ctx, targetDir, cand, tidy)
	case "package.json":
		return applyNodeUpdate(ctx, targetDir, cand)
	default:
		return fmt.Errorf("unsupported manifest type: %s", cand.ManifestType)
	}
}

// errGoModFallbackEdit marks an update whose `go get` failed and whose requirement was then
// rewritten in go.mod directly. The manifest names the target version, but no go command
// resolved it, so the update is reported as failed with the go get error attached rather
// than as applied.
var errGoModFallbackEdit = errors.New("go get failed; go.mod requirement rewritten directly")

// errFallbackRefused reports a go.mod fallback edit refused before anything was written,
// because the edit could not name exactly one require line and one module version to write.
var errFallbackRefused = errors.New("go.mod fallback edit refused")

func applyGoUpdate(ctx context.Context, targetDir string, cand UpgradeCandidate, tidy bool) error {
	targetSpec := fmt.Sprintf("%s@%s", cand.Package, cand.TargetVersion)
	out, err := util.RunCommand(ctx, targetDir, "go", "get", targetSpec)
	if err == nil {
		return tidyGoModule(ctx, targetDir, tidy)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	getErr := fmt.Errorf("go get %s failed: %w (%s)", targetSpec, err, out)
	if fbErr := fallbackGoModEdit(ctx, targetDir, cand); fbErr != nil {
		return errors.Join(getErr, fbErr)
	}
	// The text edit is a fallback, not a success: the go get error stays in the result so
	// the caller never reports an update that no go command resolved as applied.
	return errors.Join(fmt.Errorf("%w: %w", errGoModFallbackEdit, getErr), tidyGoModule(ctx, targetDir, tidy))
}

// tidyGoModule runs go mod tidy in targetDir when tidy is set.
func tidyGoModule(ctx context.Context, targetDir string, tidy bool) error {
	if !tidy {
		return nil
	}
	if _, err := util.RunCommand(ctx, targetDir, "go", "mod", "tidy"); err != nil {
		return fmt.Errorf("go mod tidy in %s: %w", targetDir, err)
	}
	return nil
}

// fallbackGoModEdit rewrites the version on the one require line that names cand.Package at
// cand.CurrentVersion, and no other byte of go.mod. It is refused, with go.mod untouched,
// when the current version is unknown, when the target is not a module version, and when no
// require line or more than one names the package. Comment, exclude and replace lines never
// match: require lines are identified by gomanifest.RequirementLine, the parser the scanners
// and CurrentGoModVersion share.
func fallbackGoModEdit(ctx context.Context, targetDir string, cand UpgradeCandidate) error {
	if cand.CurrentVersion == "" {
		return fmt.Errorf("%w: current version of %s is unknown", errFallbackRefused, cand.Package)
	}
	if !isModuleVersion(cand.TargetVersion) {
		return fmt.Errorf("%w: target %q is not a module version", errFallbackRefused, cand.TargetVersion)
	}
	data, err := readManifest(ctx, targetDir, "go.mod")
	if err != nil {
		return err
	}
	edited, err := rewriteRequirement(string(data), cand.Package, cand.CurrentVersion, cand.TargetVersion)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Join(targetDir, "go.mod"), err)
	}
	return writeManifest(ctx, targetDir, "go.mod", data, []byte(edited))
}

// isModuleVersion reports whether version can stand as the version token of a go.mod require
// line: a "v"-prefixed SemVer with no surrounding space. A go get query such as "latest" or
// a branch name is valid on the command line but not in the manifest.
func isModuleVersion(version string) bool {
	_, ok := semver.Parse(version)
	return ok && strings.HasPrefix(version, "v") && strings.TrimSpace(version) == version
}

// rewriteRequirement returns manifest with the version token of pkg's single require line
// changed from current to target. Every other line, and the rest of that line (indentation,
// the require keyword, alignment, a trailing comment, the line ending), is kept byte for byte.
func rewriteRequirement(manifest, pkg, current, target string) (string, error) {
	lines := strings.SplitAfter(manifest, "\n")
	count := len(lines)
	if lines[count-1] == "" {
		count--
	}
	if count > MaxManifestLines {
		return "", fmt.Errorf("%w: manifest exceeds %d lines", errFallbackRefused, MaxManifestLines)
	}
	index, err := requirementLineIndex(lines, pkg, current)
	if err != nil {
		return "", err
	}
	line := lines[index]
	start := requirementVersionOffset(line, pkg)
	if start < 0 || !strings.HasPrefix(line[start:], current) {
		return "", fmt.Errorf("%w: cannot locate %s %s on line %d", errFallbackRefused, pkg, current, index+1)
	}
	lines[index] = line[:start] + target + line[start+len(current):]
	return strings.Join(lines, ""), nil
}

// requirementLineIndex returns the index of the only require line naming pkg, and refuses
// when there is none, more than one, or when that line requires a version other than current.
func requirementLineIndex(lines []string, pkg, current string) (int, error) {
	index, version := -1, ""
	inRequire := false
	for i := 0; i < len(lines) && i < MaxManifestLines; i++ {
		found, ok := requiredModuleVersion(lines[i], &inRequire, pkg)
		if !ok {
			continue
		}
		if index >= 0 {
			return -1, fmt.Errorf("%w: %s is required on lines %d and %d", errFallbackRefused, pkg, index+1, i+1)
		}
		index, version = i, found
	}
	if index < 0 {
		return -1, fmt.Errorf("%w: no require line names %s", errFallbackRefused, pkg)
	}
	if version != current {
		return -1, fmt.Errorf("%w: %s is required at %s, not %s", errFallbackRefused, pkg, version, current)
	}
	return index, nil
}

// requirementVersionOffset returns the byte offset of the version token on a line that
// requiredModuleVersion accepted for pkg, or -1. It walks the line the way
// gomanifest.RequirementLine reads it: leading space, an optional "require " keyword, space,
// the module path, space, then the version.
func requirementVersionOffset(line, pkg string) int {
	body := strings.TrimLeftFunc(line, unicode.IsSpace)
	body = strings.TrimPrefix(body, "require ")
	body = strings.TrimLeftFunc(body, unicode.IsSpace)
	rest, found := strings.CutPrefix(body, pkg)
	if !found {
		return -1
	}
	version := strings.TrimLeftFunc(rest, unicode.IsSpace)
	if len(version) == len(rest) {
		return -1
	}
	return len(line) - len(version)
}

// applyNodeUpdate raises a Node dependency. Under a pnpm lockfile the update is pnpm's:
// its failure is returned, never followed by a manual package.json edit, because an edited
// manifest next to an unchanged pnpm-lock.yaml resolves the old version while declaring
// the new one. Without a lockfile the manifest is the whole record and is edited in place.
func applyNodeUpdate(ctx context.Context, targetDir string, cand UpgradeCandidate) error {
	pnpmLock := filepath.Join(targetDir, "pnpm-lock.yaml")
	if !util.FileExists(pnpmLock) && !util.FileExists(filepath.Join(targetDir, "..", "pnpm-lock.yaml")) {
		return updatePackageManifest(ctx, targetDir, cand)
	}
	spec := fmt.Sprintf("%s@%s", cand.Package, cand.TargetVersion)
	if _, err := util.RunCommand(ctx, targetDir, "pnpm", "update", spec); err != nil {
		return fmt.Errorf("pnpm update %s (package.json left unchanged to match pnpm-lock.yaml): %w", spec, err)
	}
	return nil
}

// dependencySections are the package.json sections a bump rewrites.
var dependencySections = map[string]bool{"dependencies": true, "devDependencies": true}

// updatePackageManifest raises cand.Package's range in targetDir's package.json to
// cand.TargetVersion. Only the range strings change: key order, indentation and every other
// byte of the file are kept. Each range keeps its own operator unless the target names one.
func updatePackageManifest(ctx context.Context, targetDir string, cand UpgradeCandidate) error {
	pkgFile := filepath.Join(targetDir, "package.json")
	data, err := readManifest(ctx, targetDir, "package.json")
	if err != nil {
		return err
	}
	ranges, err := dependencyRanges(data, cand.Package)
	if err != nil {
		return fmt.Errorf("parse %s: %w", pkgFile, err)
	}
	if len(ranges) == 0 {
		return fmt.Errorf("package %s not found in %s", cand.Package, pkgFile)
	}
	edited, err := rewriteRanges(data, ranges, cand.TargetVersion)
	if err != nil {
		return fmt.Errorf("update %s in %s: %w", cand.Package, pkgFile, err)
	}
	return writeManifest(ctx, targetDir, "package.json", data, edited)
}

// dependencyRanges returns the range values that name pkg in the dependency sections of
// the package.json document data, in document order.
func dependencyRanges(data []byte, pkg string) ([]objectMember, error) {
	if !json.Valid(data) {
		return nil, errors.New("not valid JSON")
	}
	top, err := objectMembers(data, 0)
	if err != nil {
		return nil, err
	}
	var ranges []objectMember
	for _, section := range top {
		if !dependencySections[section.key] {
			continue
		}
		members, err := objectMembers(section.value, section.start)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", section.key, err)
		}
		for _, member := range members {
			if member.key == pkg {
				ranges = append(ranges, member)
			}
		}
	}
	return ranges, nil
}

// objectMember is one member of a JSON object: its key, the exact bytes of its value, and
// the offset of that value in the enclosing document.
type objectMember struct {
	key   string
	value json.RawMessage
	start int
}

// objectMembers returns the members of the JSON object encoded in object, in document
// order, at most maxManifestDependencies of them; base is object's offset in the document
// member offsets are reported against. A value that is not an object has no members.
func objectMembers(object []byte, base int) ([]objectMember, error) {
	decoder := json.NewDecoder(bytes.NewReader(object))
	open, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("read object: %w", err)
	}
	if open != json.Delim('{') {
		return nil, nil
	}
	var members []objectMember
	for i := 0; decoder.More(); i++ {
		if i == maxManifestDependencies {
			return nil, fmt.Errorf("object exceeds %d members", maxManifestDependencies)
		}
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("read key: %w", err)
		}
		key, isKey := token.(string)
		if !isKey {
			return nil, fmt.Errorf("object key %v is not a string", token)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("read value of %s: %w", key, err)
		}
		end := base + int(decoder.InputOffset())
		members = append(members, objectMember{key: key, value: value, start: end - len(value)})
	}
	return members, nil
}

// rewriteRanges returns data with each range value replaced by target under that range's
// operator. ranges are in document order and never overlap.
func rewriteRanges(data []byte, ranges []objectMember, target string) ([]byte, error) {
	var out bytes.Buffer
	last := 0
	for _, member := range ranges {
		replacement, err := raisedRange(member.value, target)
		if err != nil {
			return nil, err
		}
		out.Write(data[last:member.start])
		out.WriteString(replacement)
		last = member.start + len(member.value)
	}
	out.Write(data[last:])
	return out.Bytes(), nil
}

// raisedRange returns the JSON string literal for current raised to target. A target
// that names its own operator (a fleet catalog pin such as "^5.7.3") sets it; otherwise
// current's operator is kept. A strict comparator is refused: ">2.1.0" or "<2.1.0" would
// exclude the very version it was raised to. A target that is not a SemVer version (a
// dist-tag such as "latest") is refused before any operator is chosen. Operators and SemVer
// versions contain no character JSON escapes, so the literal is the quoted text.
func raisedRange(current json.RawMessage, target string) (string, error) {
	var spec string
	if err := json.Unmarshal(current, &spec); err != nil {
		return "", fmt.Errorf("range %s is not a string: %w", current, err)
	}
	operator, _, ok := splitRangeOperator(spec)
	if !ok {
		return "", fmt.Errorf("range %q is not a single-version range; update it by hand", spec)
	}
	targetOperator, version, ok := splitRangeOperator(target)
	if !ok {
		return "", fmt.Errorf("target %q is not a SemVer version", target)
	}
	if targetOperator != "" {
		operator = targetOperator
	}
	if strictRangeOperators[operator] {
		return "", fmt.Errorf("range %q raised to %s would read %q and exclude %s; update it by hand", spec, target, operator+version, version)
	}
	return `"` + operator + version + `"`, nil
}

// strictRangeOperators are the comparators whose range excludes the version they name.
var strictRangeOperators = map[string]bool{">": true, "<": true}

// rangeOperators are the comparator prefixes a single-version npm range may carry, longest
// first so ">=" is not read as ">".
var rangeOperators = []string{">=", "<=", "^", "~", ">", "<", "="}

// splitRangeOperator splits a single-version npm range such as "^1.2.3", "~1.2.3" or
// ">=1.2.3" into its operator and SemVer version; a bare version has no operator. ok is false
// for anything else: a compound or x-range, a tag such as "latest", a protocol spec such as
// "workspace:^1.0.0" or "file:../x", or surrounding space.
func splitRangeOperator(spec string) (operator, version string, ok bool) {
	for _, candidate := range rangeOperators {
		if strings.HasPrefix(spec, candidate) {
			operator = candidate
			break
		}
	}
	version = strings.TrimPrefix(spec, operator)
	if _, parsed := semver.Parse(version); !parsed || strings.TrimSpace(version) != version {
		return "", "", false
	}
	return operator, version, true
}

// UpdateAll batches updates for all given candidates across repoPath.
func UpdateAll(ctx context.Context, repoPath string, candidates []UpgradeCandidate) (int, error) {
	applied := 0
	var failures []error
	modulesToTidy := make(map[string]bool)

	for _, c := range candidates {
		err := applyUpdateInternal(ctx, repoPath, c, false)
		if err != nil {
			failures = append(failures, fmt.Errorf("update %s: %w", c.Package, err))
		} else {
			applied++
		}
		// A fallback edit is a failed update that still changed go.mod, so the module is
		// tidied like an applied one: tidy either reconciles go.sum or reports why not.
		if c.ManifestType == "go.mod" && (err == nil || errors.Is(err, errGoModFallbackEdit)) {
			targetDir := repoPath
			if c.ModuleDir != "" && c.ModuleDir != "." {
				targetDir = filepath.Join(repoPath, c.ModuleDir)
			}
			modulesToTidy[targetDir] = true
		}
	}

	// Final tidy pass for Go modules
	for dir := range modulesToTidy {
		if tidyErr := tidyGoModule(ctx, dir, true); tidyErr != nil {
			failures = append(failures, tidyErr)
		}
	}

	return applied, errors.Join(failures...)
}
