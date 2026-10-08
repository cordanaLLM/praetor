// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	engineMakefile            = ".config/praetor/engine.mk"
	engineMakefileIncludeLine = "-include " + engineMakefile
	engineMakefileContent     = "# Praetor engine resolution; included by Makefiles to align $(PRAETORCTL) with the hook launcher (#906).\n" +
		"PRAETORCTL ?= $(shell sh " + engineLauncherFile + " --print-path)\n\n" +
		".PHONY: praetor-engine-path\n" +
		"praetor-engine-path:\n" +
		"\t@echo $(PRAETORCTL)\n"
)

// priorEngineMakefileDigests records the digests of every Praetor text at engineMakefile.
var priorEngineMakefileDigests = map[string]string{
	"2665828e723edbad12d9195f1997e9b99bbd7754990bbf259d7f0b09503d24f6": "engine resolution and praetor-engine-path target (#906)",
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

func ensureEngineMakefileInclude(data string) (string, bool) {
	normalized, crlf, err := util.NormalizeLineEndingsStrict(data)
	if err != nil {
		normalized, crlf = util.NormalizeLineEndings(data)
	}
	if hasEngineMakefileInclude(normalized) {
		return data, false
	}
	lines := strings.Split(normalized, "\n")
	insertAt := findLeadingCommentEnd(lines)
	lines = slices.Insert(lines, insertAt, engineMakefileIncludeLine)
	result := strings.Join(lines, "\n")
	return util.RestoreLineEndings(result, crlf), true
}

func checkMakefileCLIVariableOverride(s *adoptSession, data string) {
	lines := strings.Split(data, "\n")
	for i, line := range lines {
		if matchCLIVariableOverride(line) {
			s.report.addWarning("%s line %d: %q overrides PRAETORCTL; adopter override is respected",
				makefileName, i+1, strings.TrimSpace(line))
		}
	}
}

func matchCLIVariableOverride(line string) bool {
	if strings.HasPrefix(line, "\t") {
		return false
	}
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "#") {
		return false
	}
	if idx := strings.Index(trimmed, "#"); idx >= 0 {
		trimmed = strings.TrimSpace(trimmed[:idx])
	}
	stripped := stripMakePrefixes(trimmed)
	if !strings.HasPrefix(stripped, "PRAETORCTL") {
		return false
	}
	rest := strings.TrimSpace(strings.TrimPrefix(stripped, "PRAETORCTL"))
	return strings.HasPrefix(rest, ":=") || strings.HasPrefix(rest, "::=") || strings.HasPrefix(rest, "=")
}

func stripMakePrefixes(s string) string {
	for _, prefix := range []string{"override", "export"} {
		if strings.HasPrefix(s, prefix) {
			s = strings.TrimSpace(strings.TrimPrefix(s, prefix))
		}
	}
	return s
}
