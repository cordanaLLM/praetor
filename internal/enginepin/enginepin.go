// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

// Package enginepin resolves the engine a repository pins with PRAETOR_REF, for the callers
// that run on every agent tool call and cannot start a shell for it (#1173).
//
// The rule has a second text: .config/lefthook/engine.sh, the launcher adoption writes, which
// resolves the pin before any engine exists (internal/adopt engineLauncherScript). The two are
// held equal by TestEnginePin_Differential_ResolverAgreesWithTheLauncher, which runs both over
// the same workflow fixtures. A change to one is a change to the other.
package enginepin

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
)

// Module and Package name what the launcher installs per pin: go install Module/Package@pin.
const (
	Module  = "github.com/cordanaLLM/praetor"
	Package = "cmd/standardsctl"
	binary  = "standardsctl"
)

// Bounds of one resolution (HISS-02).
const (
	maxWorkflowFiles = 512
	maxWorkflowBytes = 1 << 20
	maxDeclarations  = 4096
)

// Resolution is the pin the workflows of a repository declare. Pin is empty when none is
// declared. Fault is set when the declaration cannot be used, as the launcher refuses it: a
// PRAETOR_REF value that is no pin, or several values. Source is the first file declaring it.
type Resolution struct {
	Pin    string
	Source string
	Fault  string
}

// declaration is one "file<TAB>value" line of the launcher's declared_pins and uses_pins.
type declaration struct {
	file  string
	value string
}

var (
	declaredLine = regexp.MustCompile(`^[ \t\v\f]*PRAETOR_REF[ \t\v\f]*[:=][ \t\v\f]*(.*)$`)
	usesLine     = regexp.MustCompile(`^[ \t\v\f]*(?:-[ \t\v\f]*)?uses:[ \t\v\f]*(.*)$`)
	adoptUses    = regexp.MustCompile(`^(?i:cordanallm/praetor)/\.github/actions/praetor-adopt@(.+)$`)
	reusableUses = regexp.MustCompile(`^(?i:cordanallm/praetor)/\.github/workflows/[^@ \t\v\f\r\n]*@(.+)$`)
	trailingNote = regexp.MustCompile(`[ \t\v\f]*#.*$`)
	closingQuote = regexp.MustCompile(`["'][ \t\v\f]*$`)
)

// Resolve reads the pin the workflows under root/.github/workflows declare, as the launcher
// does: PRAETOR_REF declarations first, and only when there is none the ref of a praetor-adopt
// action or reusable workflow that is itself a pin. An unreadable workflow file is an error.
func Resolve(root string) (Resolution, error) {
	files, err := workflowFiles(root)
	if err != nil {
		return Resolution{}, err
	}
	declared, uses, err := declarations(root, files)
	if err != nil {
		return Resolution{}, err
	}
	pins := declared
	if len(pins) == 0 {
		pins = uses
	}
	if len(pins) == 0 {
		return Resolution{}, nil
	}
	values := make([]string, 0, len(pins))
	for _, pin := range pins {
		if !slices.Contains(values, pin.value) {
			values = append(values, pin.value)
		}
	}
	source := pins[0].file
	if len(values) != 1 {
		return Resolution{Source: source, Fault: fmt.Sprintf("the repository declares several PRAETOR_REF values, so no engine is pinned: %s",
			describe(pins))}, nil
	}
	if fault := PinFault(values[0]); fault != "" {
		return Resolution{Pin: values[0], Source: source, Fault: fmt.Sprintf("PRAETOR_REF='%s' in %s is no usable pin: %s", values[0], source, fault)}, nil
	}
	return Resolution{Pin: values[0], Source: source}, nil
}

// workflowFiles lists .github/workflows/*.yml and then *.yaml, each sorted, as the shell's two
// globs expand them.
func workflowFiles(root string) ([]string, error) {
	var files []string
	for _, pattern := range []string{"*.yml", "*.yaml"} {
		matches, err := filepath.Glob(filepath.Join(root, ".github", "workflows", pattern))
		if err != nil {
			return nil, err
		}
		slices.Sort(matches)
		files = append(files, matches...)
	}
	if len(files) > maxWorkflowFiles {
		return nil, fmt.Errorf("more than %d workflow files under %s", maxWorkflowFiles, filepath.Join(root, ".github", "workflows"))
	}
	return files, nil
}

// declarations reads every declared PRAETOR_REF value and every usable uses ref of files.
func declarations(root string, files []string) (declared, uses []declaration, err error) {
	for _, path := range files {
		rel := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
		data, err := readWorkflow(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("workflow file %s is not readable: %w", rel, err)
		}
		for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r", ""), "\n") {
			if len(declared)+len(uses) > maxDeclarations {
				return nil, nil, fmt.Errorf("more than %d engine declarations under .github/workflows", maxDeclarations)
			}
			if match := declaredLine.FindStringSubmatch(line); match != nil {
				declared = append(declared, declaration{rel, cleanValue(match[1])})
				continue
			}
			if ref, ok := usesRef(line); ok && PinFault(ref) == "" {
				uses = append(uses, declaration{rel, ref})
			}
		}
	}
	return declared, uses, nil
}

func readWorkflow(path string) ([]byte, error) {
	file, err := os.Open(path) // #nosec G304 -- a workflow file of the repository being judged.
	if err != nil {
		return nil, err
	}
	defer file.Close() //nolint:errcheck // read-only
	data := make([]byte, maxWorkflowBytes+1)
	n, err := file.Read(data)
	if err != nil && n == 0 {
		return nil, err
	}
	return data[:n], nil
}

// usesRef returns the ref of a praetor-adopt action or a reusable Praetor workflow on line.
func usesRef(line string) (string, bool) {
	match := usesLine.FindStringSubmatch(line)
	if match == nil {
		return "", false
	}
	value := cleanValue(match[1])
	for _, pattern := range []*regexp.Regexp{adoptUses, reusableUses} {
		if ref := pattern.FindStringSubmatch(value); ref != nil {
			return strings.TrimSpace(ref[1]), true
		}
	}
	return "", false
}

// cleanValue drops a trailing comment, one opening quote, a closing quote and surrounding
// blanks, as the launcher's sed and read do.
func cleanValue(value string) string {
	value = trailingNote.ReplaceAllString(value, "")
	value = strings.TrimPrefix(strings.TrimPrefix(value, `"`), `'`)
	if !strings.HasPrefix(value, `"`) && !strings.HasPrefix(value, `'`) {
		value = closingQuote.ReplaceAllString(value, "")
	}
	return strings.Trim(value, " \t\v\f")
}

func describe(pins []declaration) string {
	parts := make([]string, 0, len(pins))
	for _, pin := range pins {
		parts = append(parts, pin.file+"="+pin.value)
	}
	return strings.Join(parts, " ")
}

// PinFault returns why ref is no pin, or "" when it is one: a commit id of 7 to 40 hexadecimal
// digits, or a release tag vMAJOR.MINOR.PATCH with an optional -prerelease or +build suffix.
func PinFault(ref string) string {
	if isHexID(ref) || isReleaseTag(ref) {
		return ""
	}
	return "it is neither a commit id of 7 to 40 hexadecimal digits nor a release tag such as v1.2.3"
}

func isHexID(ref string) bool {
	if len(ref) < 7 || len(ref) > 40 {
		return false
	}
	return strings.Trim(ref, "0123456789abcdefABCDEF") == ""
}

func isReleaseTag(ref string) bool {
	rest, ok := strings.CutPrefix(ref, "v")
	if !ok {
		return false
	}
	major, rest, ok := strings.Cut(rest, ".")
	if !ok || !allDigits(major) {
		return false
	}
	minor, rest, ok := strings.Cut(rest, ".")
	if !ok || !allDigits(minor) {
		return false
	}
	patch := strings.TrimLeft(rest, "0123456789")
	if len(patch) == len(rest) {
		return false
	}
	if patch == "" {
		return true
	}
	return (patch[0] == '-' || patch[0] == '+') && strings.Trim(patch, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._+-") == ""
}

func allDigits(text string) bool {
	return text != "" && strings.Trim(text, "0123456789") == ""
}

// CacheDirectory is the directory the launcher installs the engine of pin into:
// ${XDG_CACHE_HOME:-$HOME/.cache}/praetor/engine/<pin>. On Windows a process started outside
// Git's shell may have no HOME; USERPROFILE is what that shell sets HOME to.
func CacheDirectory(pin string, getenv func(string) string) (string, error) {
	base := getenv("XDG_CACHE_HOME")
	if base == "" {
		home := getenv("HOME")
		if home == "" && runtime.GOOS == "windows" {
			home = getenv("USERPROFILE")
		}
		if home == "" {
			return "", errors.New("neither XDG_CACHE_HOME nor HOME is set")
		}
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "praetor", "engine", pin), nil
}

// CachedEngine returns the installed engine in directory, the plain name first and then the
// .exe one, or "" when neither is there.
func CachedEngine(directory string) string {
	for _, name := range []string{binary, binary + ".exe"} {
		path := filepath.Join(directory, name)
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path
		}
	}
	return ""
}

// InstallCommand is the command that installs the engine of pin where the launcher looks.
func InstallCommand(pin, directory string) string {
	return "GOBIN=" + directory + " go install " + Module + "/" + Package + "@" + pin
}
