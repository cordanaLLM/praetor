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

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	engineMakefile            = ".config/praetor/engine.mk"
	engineMakefileIncludeLine = "-include " + engineMakefile
	engineMakefileContent     = "# Praetor engine resolution; included by Makefiles to align $(PRAETORCTL) with the hook launcher (#906).\n" +
		"ifneq ($(wildcard " + engineLauncherFile + "),)\n" +
		"PRAETOR_ENGINE = $(eval PRAETOR_ENGINE := $(or $(shell sh " + engineLauncherFile + " --print-path),$(error praetor hooks: engine launcher failed to resolve praetorctl)))$(PRAETOR_ENGINE)\n" +
		"PRAETORCTL ?= $(PRAETOR_ENGINE)\n" +
		"endif\n\n" +
		"praetor_engine_goal := $(.DEFAULT_GOAL)\n" +
		".PHONY: praetor-engine-path\n" +
		"praetor-engine-path:\n" +
		"\t@echo $(PRAETORCTL)\n" +
		".DEFAULT_GOAL := $(praetor_engine_goal)\n"
)

// priorEngineMakefileDigests records the digests of every Praetor text at engineMakefile.
var priorEngineMakefileDigests = map[string]string{
	"2665828e723edbad12d9195f1997e9b99bbd7754990bbf259d7f0b09503d24f6": "engine resolution and praetor-engine-path target (#906)",
	"f8274873d3db269e3308ce3425cc049f08f9dfee5d91f29adf42e6e194b8b1d1": "fail-closed engine resolution guarding on launcher existence (#906)",
	"0599c6a779e0f605cc29e5f331511f87262db68b0177c55db7898937d5b5be94": "default-goal preservation around praetor-engine-path target (#906)",
}

// reconcileEngineMakefile scaffolds the managed engine.mk file.
func reconcileEngineMakefile(ctx context.Context, s *adoptSession) error {
	_, err := s.scaffoldFile(ctx, scaffold{
		rel:       engineMakefile,
		perm:      filePerm,
		content:   []byte(engineMakefileContent),
		created:   "Installed Praetor engine Makefile: Make targets resolve the engine the repository pins",
		verified:  "Existing Praetor engine Makefile verified present",
		refreshed: "Refreshed an unedited earlier Praetor engine Makefile to the current text",
		prior:     priorEngineMakefileDigests,
	})
	return err
}

func launcherInstalledOrPlanned(ctx context.Context, s *adoptSession) (bool, error) {
	declined, err := ArtifactDeclined(s.declined, "git-hooks")
	if err != nil {
		return false, err
	}
	if declined {
		return false, nil
	}
	if _, err := os.Stat(filepath.Join(s.repoPath, filepath.FromSlash(engineLauncherFile))); err == nil {
		return true, nil
	}
	existing, exists, err := s.readExistingLefthook()
	if err != nil {
		return false, err
	}
	if exists {
		shape, err := s.lefthookShape(ctx)
		if err != nil {
			return false, err
		}
		identity := classifyLefthookConfig(existing, shape)
		if identity.reason != "" {
			return false, nil
		}
	}
	return true, nil
}

func hasEngineMakefileInclude(normalized string) bool {
	for _, line := range strings.Split(normalized, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if idx := strings.Index(trimmed, "#"); idx >= 0 {
			trimmed = strings.TrimSpace(trimmed[:idx])
		}
		fields := strings.Fields(trimmed)
		if len(fields) == 2 && (fields[0] == "-include" || fields[0] == "include" || fields[0] == "sinclude") &&
			(fields[1] == engineMakefile || fields[1] == ".config/praetor/engine.mk") {
			return true
		}
	}
	return false
}

func withoutEngineMakefileInclude(data string) string {
	lines := strings.Split(data, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if idx := strings.Index(trimmed, "#"); idx >= 0 {
			trimmed = strings.TrimSpace(trimmed[:idx])
		}
		fields := strings.Fields(trimmed)
		if len(fields) == 2 && (fields[0] == "-include" || fields[0] == "include" || fields[0] == "sinclude") &&
			(fields[1] == engineMakefile || fields[1] == ".config/praetor/engine.mk") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

func findLeadingCommentEnd(lines []string) int {
	lastComment := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			lastComment = i
		} else if trimmed != "" {
			break
		}
	}
	return lastComment + 1
}

func ensureEngineMakefileInclude(data string) (string, bool, error) {
	normalized, crlf, err := util.NormalizeLineEndingsStrict(data)
	if err != nil {
		return "", false, fmt.Errorf("makefile line endings are inconsistent: %w", err)
	}
	if hasEngineMakefileInclude(normalized) {
		return data, false, nil
	}
	lines := strings.Split(normalized, "\n")
	insertAt := findLeadingCommentEnd(lines)
	lines = slices.Insert(lines, insertAt, engineMakefileIncludeLine)
	result := strings.Join(lines, "\n")
	return util.RestoreLineEndings(result, crlf), true, nil
}

func checkMakefileCLIVariableOverride(s *adoptSession, data string) {
	lines := strings.Split(data, "\n")
	for i, line := range lines {
		matched, op := matchCLIVariableOverride(line)
		if !matched {
			continue
		}
		if op == "?=" {
			s.report.addWarning("%s line %d: %q defines PRAETORCTL with ?=; shadowed by %s (use := or = to override)",
				makefileName, i+1, strings.TrimSpace(line), engineMakefile)
		} else {
			s.report.addWarning("%s line %d: %q overrides PRAETORCTL; adopter override is respected",
				makefileName, i+1, strings.TrimSpace(line))
		}
	}
}

func matchCLIVariableOverride(line string) (bool, string) {
	if strings.HasPrefix(line, "\t") || util.MakefileIsCLIVariableLine(line) {
		return false, ""
	}
	if idx := strings.Index(line, "#"); idx >= 0 {
		line = line[:idx]
	}
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false, ""
	}
	stripped := stripMakePrefixes(trimmed)
	if ok, op := matchDefineCLIVariable(stripped); ok {
		return true, op
	}
	return matchAssignCLIVariable(stripped)
}

func matchDefineCLIVariable(stripped string) (bool, string) {
	if !strings.HasPrefix(stripped, "define") {
		return false, ""
	}
	if len(stripped) > 6 && stripped[6] != ' ' && stripped[6] != '\t' {
		return false, ""
	}
	fields := strings.Fields(strings.TrimSpace(stripped[6:]))
	if len(fields) > 0 && fields[0] == "PRAETORCTL" {
		return true, "define"
	}
	return false, ""
}

func matchAssignCLIVariable(stripped string) (bool, string) {
	if !strings.HasPrefix(stripped, "PRAETORCTL") {
		return false, ""
	}
	rest := strings.TrimSpace(strings.TrimPrefix(stripped, "PRAETORCTL"))
	for _, op := range []string{":=", "::=", "?=", "+=", "!=", "="} {
		if strings.HasPrefix(rest, op) {
			return true, op
		}
	}
	return false, ""
}

func stripMakePrefixes(s string) string {
	for i := 0; i < 4; i++ {
		stripped := false
		for _, prefix := range []string{"override", "export", "private"} {
			if strings.HasPrefix(s, prefix) && (len(s) == len(prefix) || s[len(prefix)] == ' ' || s[len(prefix)] == '\t') {
				s = strings.TrimSpace(s[len(prefix):])
				stripped = true
			}
		}
		if !stripped {
			break
		}
	}
	return s
}
