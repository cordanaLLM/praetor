// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

// MakefileLiteralChains is a Makefile whose five rule targets are computed from variables, each
// bound once with ":=" to literal text and references to variables bound the same way above it:
// the shape an adopter repository reported on issue #537, rebuilt with other names. Make expands
// the five names to .tools/bin/python, .tools/bin/ruff, .tools/bin/black, engine/out and
// engine/trace, so the file declares no docs-lint, docs-figures or verify-all rule, and the shared
// Makefile reader (internal/util/makefile_variables.go) reads it the same way. The util, adopt and
// editor tests replay it against GNU Make and through adoption and editor generation.
const MakefileLiteralChains = "TOOLS_DIR := .tools\n" +
	"TOOLS_PY := $(TOOLS_DIR)/bin/python\n" +
	"LINTER := $(TOOLS_DIR)/bin/ruff\n" +
	"FORMATTER := $(TOOLS_DIR)/bin/black\n" +
	"SRC_ROOT := engine\n" +
	"OUT_DIR := $(SRC_ROOT)/out\n" +
	"TRACE_DIR := $(SRC_ROOT)/trace\n\n" +
	".PHONY: all\n" +
	"all: $(OUT_DIR) $(TRACE_DIR)\n\n" +
	"$(OUT_DIR): $(LINTER) $(FORMATTER)\n" +
	"\tmkdir -p $@\n\n" +
	"$(TRACE_DIR): $(LINTER) $(FORMATTER)\n" +
	"\tmkdir -p $@\n\n" +
	"$(TOOLS_PY):\n" +
	"\tpython3 -m venv $(TOOLS_DIR)\n\n" +
	"$(LINTER): $(TOOLS_PY)\n" +
	"\t$(TOOLS_PY) -m pip install ruff\n\n" +
	"$(FORMATTER): $(TOOLS_PY)\n" +
	"\t$(TOOLS_PY) -m pip install black\n"
