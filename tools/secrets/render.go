// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package secrets

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/tools/secrets/rules"
)

const (
	// ExceptionsEnv is the environment variable the rendered workflow hands the gate:
	// the JSON list of the manifest's HISS-12 exceptions.
	ExceptionsEnv = "SECRETS_EXCEPTIONS"
)

const (
	gateRunCommand    = "go run -tags=" + BuildTag + " ./tools/secrets/gate"
	runMarker         = "        run: " + gateRunCommand
	exceptionsComment = "          # yamllint disable-line rule:line-length\n"
	exceptionsKey     = "          " + ExceptionsEnv + ": "
	envHeader         = "        env:\n"
)

// exceptionsLines serializes entries as a JSON list in a YAML double-quoted scalar.
func exceptionsLines(entries []rules.Entry) (string, error) {
	var list bytes.Buffer
	encoder := json.NewEncoder(&list)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(entries); err != nil {
		return "", fmt.Errorf("encode secrets exceptions: %w", err)
	}
	quoted, err := json.Marshal(strings.TrimSuffix(list.String(), "\n"))
	if err != nil {
		return "", fmt.Errorf("quote secrets exceptions: %w", err)
	}
	return envHeader + exceptionsComment + exceptionsKey + string(quoted) + "\n", nil
}

// RenderWorkflow returns plain with SECRETS_EXCEPTIONS rendered into the gate step's environment.
// When entries is empty, plain is returned unchanged.
func RenderWorkflow(plain string, entries []rules.Entry) (string, error) {
	if len(entries) == 0 {
		return plain, nil
	}
	if len(entries) > rules.MaxJudgedEntries {
		return "", fmt.Errorf("the workflow carries at most %d exceptions", rules.MaxJudgedEntries)
	}
	if strings.Count(plain, runMarker) != 1 {
		return "", errors.New("the workflow lacks the single secrets gate run step")
	}
	lines, err := exceptionsLines(entries)
	if err != nil {
		return "", err
	}
	rendered := strings.Replace(plain, runMarker, lines+runMarker, 1)
	return rendered, nil
}

// StripSettings returns the plain workflow text behind a rendering, removing SECRETS_EXCEPTIONS.
func StripSettings(text string) (plain string, ok bool) {
	return ghworkflow.StripRendering(text, readSettings, RenderWorkflow)
}

func readSettings(text string) ([]rules.Entry, string, bool) {
	plain := text
	marker := envHeader + exceptionsComment + exceptionsKey
	start := strings.Index(plain, marker)
	if start < 0 {
		return nil, plain, true
	}
	rest := plain[start+len(marker):]
	quoted, after, found := strings.Cut(rest, "\n")
	if !found {
		return nil, "", false
	}
	entries, parsed := parseExceptionsLine(quoted)
	if !parsed {
		return nil, "", false
	}
	plain = plain[:start] + after
	return entries, plain, true
}

func parseExceptionsLine(quoted string) ([]rules.Entry, bool) {
	var inner string
	if err := json.Unmarshal([]byte(quoted), &inner); err != nil {
		return nil, false
	}
	decoder := json.NewDecoder(strings.NewReader(inner))
	decoder.DisallowUnknownFields()
	var entries []rules.Entry
	if err := decoder.Decode(&entries); err != nil || len(entries) == 0 {
		return nil, false
	}
	return entries, true
}
