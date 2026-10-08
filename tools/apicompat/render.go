// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package apicompat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

const (
	// ExceptionsEnv is the environment variable the rendered workflow hands the gate: the JSON
	// list of the manifest's api-compatibility exceptions, which the standalone gate cannot read
	// from the manifest itself.
	ExceptionsEnv = "APICOMPAT_EXCEPTIONS"
	// InstallStepName names the step that installs api.system_packages.
	InstallStepName = "Install system packages for the Go build"
	// InstallTimeoutMinutes bounds the install step.
	InstallTimeoutMinutes = 15
)

const (
	compareStepMarker = "      - name: Compare the API of every Go module"
	envMarker         = "          BASE: ${{ github.event.pull_request.base.sha }}\n"
	exceptionsComment = "          # yamllint disable-line rule:line-length\n"
	exceptionsKey     = "          " + ExceptionsEnv + ": "
	installPrefix     = "          sudo apt-get install -y --no-install-recommends"
	installIndent     = "            "
	installStepOpen   = "      - name: " + InstallStepName
)

// ModuleException is one api-compatibility exception the gate receives: the go.mod of a module
// whose build failure is excused until the end of the Expires day, and why.
type ModuleException struct {
	Path    string `json:"path"`
	Reason  string `json:"reason"`
	Expires string `json:"expires"`
}

// Settings are the repository-specific parts of the workflow, read from the manifest: the Debian
// packages to install (api.system_packages) and the module exceptions (exceptions, rule
// api-compatibility). The zero value renders Workflow unchanged.
type Settings struct {
	SystemPackages []string
	Exceptions     []ModuleException
}

// Empty reports whether the settings leave the workflow as it is.
func (s Settings) Empty() bool {
	return len(s.SystemPackages) == 0 && len(s.Exceptions) == 0
}

// installStep is the step that installs packages on the Linux runner before the gate builds
// anything, with a timeout. The names follow the Debian package-name grammar
// (config.ValidDebianPackageName), so they need no quoting.
func installStep(packages []string) string {
	return installStepOpen + ghworkflow.HostedGateStepIf + "\n" +
		"        timeout-minutes: " + strconv.Itoa(InstallTimeoutMinutes) + "\n" +
		"        run: |\n" +
		"          sudo apt-get update\n" +
		installPrefix + " \\\n" +
		installIndent + strings.Join(packages, " \\\n"+installIndent) + "\n"
}

// exceptionsLines are the env lines that hand the gate its exceptions: the list as JSON inside a
// YAML double-quoted scalar, which reads JSON's string escapes, on a line the yamllint
// line-length rule skips.
func exceptionsLines(entries []ModuleException) (string, error) {
	var list bytes.Buffer
	encoder := json.NewEncoder(&list)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(entries); err != nil {
		return "", fmt.Errorf("encode the api-compatibility exceptions: %w", err)
	}
	quoted, err := json.Marshal(strings.TrimSuffix(list.String(), "\n"))
	if err != nil {
		return "", fmt.Errorf("quote the api-compatibility exceptions: %w", err)
	}
	return exceptionsComment + exceptionsKey + string(quoted) + "\n", nil
}

// RenderWorkflow returns plain, a rendering of Workflow for any default branch, with the
// settings applied: the install step directly before the gate step, and the exceptions in the
// gate step's environment. Empty settings return plain unchanged, so a repository that declares
// neither keeps today's bytes. A plain that lacks either insertion point is an error.
func RenderWorkflow(plain string, settings Settings) (string, error) {
	if settings.Empty() {
		return plain, nil
	}
	if len(settings.SystemPackages) > config.MaxAPISystemPackages || len(settings.Exceptions) > config.MaxExceptions {
		return "", fmt.Errorf("the workflow carries at most %d packages and %d exceptions", config.MaxAPISystemPackages, config.MaxExceptions)
	}
	if strings.Count(plain, compareStepMarker) != 1 || strings.Count(plain, envMarker) != 1 {
		return "", errors.New("the API compatibility workflow lacks the single gate step the settings are rendered into")
	}
	rendered := plain
	if len(settings.Exceptions) > 0 {
		lines, err := exceptionsLines(settings.Exceptions)
		if err != nil {
			return "", err
		}
		rendered = strings.Replace(rendered, envMarker, envMarker+lines, 1)
	}
	if len(settings.SystemPackages) > 0 {
		rendered = strings.Replace(rendered, compareStepMarker, installStep(settings.SystemPackages)+compareStepMarker, 1)
	}
	return rendered, nil
}

// StripSettings is RenderWorkflow's inverse: it returns the plain workflow text behind a
// rendering, and ok only when text is exactly RenderWorkflow of that plain text with the
// settings it carries. An edited step or line, or any other deviation, returns false, so only
// Praetor's own output is recognised. Text that carries no settings returns itself with ok.
func StripSettings(text string) (plain string, ok bool) {
	settings, plain, ok := readSettings(text)
	if !ok {
		return "", false
	}
	again, err := RenderWorkflow(plain, settings)
	if err != nil || again != text {
		return "", false
	}
	return plain, true
}

// readSettings cuts the install step and the exceptions env lines out of text and parses what
// they carry. ok is false for a step or line that does not parse; the caller verifies the cut
// by rendering it again.
func readSettings(text string) (settings Settings, plain string, ok bool) {
	plain = text
	if start := strings.Index(plain, installStepOpen); start >= 0 {
		end := strings.Index(plain[start:], compareStepMarker)
		if end < 0 {
			return Settings{}, "", false
		}
		packages, found := packagesOfStep(plain[start : start+end])
		if !found {
			return Settings{}, "", false
		}
		settings.SystemPackages = packages
		plain = plain[:start] + plain[start+end:]
	}
	if start := strings.Index(plain, exceptionsComment+exceptionsKey); start >= 0 {
		rest := plain[start+len(exceptionsComment+exceptionsKey):]
		quoted, after, found := strings.Cut(rest, "\n")
		entries, parsed := exceptionsOfLine(quoted)
		if !found || !parsed {
			return Settings{}, "", false
		}
		settings.Exceptions = entries
		plain = plain[:start] + after
	}
	return settings, plain, true
}

// packagesOfStep returns the package names the install command of step lists: one per line
// after installPrefix, each but the last ended by a line continuation, so that no line passes
// yamllint's 80 columns (config.MaxAPISystemPackageBytes).
func packagesOfStep(step string) ([]string, bool) {
	_, rest, found := strings.Cut(step, installPrefix+" \\\n")
	if !found {
		return nil, false
	}
	var packages []string
	for _, line := range strings.Split(strings.TrimSuffix(rest, "\n"), "\n") {
		name, more := strings.CutSuffix(strings.TrimPrefix(line, installIndent), " \\")
		if name == "" || strings.ContainsAny(name, " \t") {
			return nil, false
		}
		packages = append(packages, name)
		if !more {
			break
		}
	}
	return packages, true
}

// exceptionsOfLine reads the quoted JSON list of an exceptions env line.
func exceptionsOfLine(quoted string) ([]ModuleException, bool) {
	var inner string
	if err := json.Unmarshal([]byte(quoted), &inner); err != nil {
		return nil, false
	}
	decoder := json.NewDecoder(strings.NewReader(inner))
	decoder.DisallowUnknownFields()
	var entries []ModuleException
	if err := decoder.Decode(&entries); err != nil || len(entries) == 0 {
		return nil, false
	}
	return entries, true
}
