// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

// Literal file tracking accepts only static shell words, never expansion or glob syntax.
var hereDocLiteralWord = regexp.MustCompile(`^(?:[A-Za-z0-9_./-]+|'[A-Za-z0-9_./ -]*'|"[A-Za-z0-9_./ -]*")+$`)
var hereDocCatOutput = regexp.MustCompile(`^\s*(?:1)?>\s*(.*?)\s*$`)

// literalHereDocWord removes quotes only from allowlisted literal file names.
func literalHereDocWord(word string) string {
	if !hereDocLiteralWord.MatchString(word) {
		return ""
	}
	return strings.NewReplacer("'", "", `"`, "").Replace(word)
}

// hereDocLiteralCommand reads an assignment-prefixed command with the shared tokenizer.
func hereDocLiteralCommand(text string) (string, []string) {
	match := hereDocCommand.FindStringSubmatchIndex(text)
	if len(match) < 4 {
		return "", nil
	}
	args, err := ShellCodeFields(text[match[3]:])
	if err != nil {
		return "", nil
	}
	return literalHereDocWord(text[match[2]:match[3]]), args
}

// hereDocFileTargets recognizes literal cat output and tee copies without input overrides.
func hereDocFileTargets(command string) []string {
	program, args := hereDocLiteralCommand(command)
	if hereDocBase(program) == "cat" {
		match := hereDocCatOutput.FindStringSubmatch(strings.Join(args, " "))
		if len(match) == 2 && literalHereDocWord(match[1]) != "" {
			return []string{literalHereDocWord(match[1])}
		}
		return nil
	}
	if hereDocBase(program) != "tee" || !hereDocTeeArgs.MatchString(strings.Join(args, " ")) {
		return nil
	}
	var targets []string
	for i := 0; i < len(args) && i < MaxShellFields; i++ {
		if target := literalHereDocWord(args[i]); target != "" && !strings.HasPrefix(target, "-") {
			targets = append(targets, target)
		}
	}
	return targets
}

// hereDocScriptFile recognizes literal shell operands, source commands and direct script paths.
func hereDocScriptFile(program string, args []string) string {
	if program == "." || program == "source" {
		if len(args) > 0 {
			return literalHereDocWord(args[0])
		}
		return ""
	}
	if !shellLanguages[hereDocBase(program)] {
		return program
	}
	for i := 0; i < len(args) && i < MaxShellFields; i++ {
		arg := literalHereDocWord(args[i])
		if arg == "--" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if strings.Trim(arg[1:], "euxv") != "" {
				return ""
			}
			continue
		}
		return arg
	}
	return ""
}

// hereDocFileFlow holds bounded lexical directories and inserts bodies at their invocation.
type hereDocFileFlow struct {
	code            string
	docs            []HereDoc
	targets         [][]string
	dir             string
	output          strings.Builder
	written, growth int
}

// followHereDocFiles exposes literal scripts at later invocations in the same script.
// Dynamic file names remain data; no file is read or command executed.
func followHereDocFiles(code string, docs []HereDoc) (string, error) {
	if !slices.ContainsFunc(docs, func(doc HereDoc) bool { return len(doc.targets) > 0 }) {
		return code, nil
	}
	normalized, scan, err := readShellCodeFields(code)
	if err != nil {
		return "", err
	}
	if err := normalizeHereDocOffsets(code, docs); err != nil {
		return "", err
	}
	flow := hereDocFileFlow{code: normalized, docs: docs, targets: make([][]string, len(docs)), dir: "."}
	if err := flow.walk(scan); err != nil {
		return "", err
	}
	flow.output.WriteString(normalized[flow.written:])
	return flow.output.String(), nil
}

// normalizeHereDocOffsets maps definition boundaries into the shared normalized code.
func normalizeHereDocOffsets(code string, docs []HereDoc) error {
	for i := 0; i < len(docs) && i < maxShellHeredocs; i++ {
		prefix, err := normalizeShellCode(code[:docs[i].offset])
		if err != nil {
			return err
		}
		docs[i].offset = len(prefix)
	}
	return nil
}

// walk visits every bounded command segment, preserving quoted operator words.
func (f *hereDocFileFlow) walk(scan shellCodeReader) error {
	start := 0
	for i := 0; i <= len(scan.fields) && i <= MaxShellFields; i++ {
		if i < len(scan.fields) && !shellFieldSeparator(scan.fields[i]) {
			continue
		}
		end := len(f.code)
		if i < len(scan.fields) {
			end = scan.starts[i]
		}
		if start < i {
			if err := f.command(scan.fields[start:i], scan.starts[start], end); err != nil {
				return err
			}
		}
		start = i + 1
	}
	return nil
}

// shellFieldSeparator recognizes only whole control operators emitted by the tokenizer.
func shellFieldSeparator(word string) bool {
	switch word {
	case ";", "&", "&&", "|", "||":
		return true
	}
	return false
}

// command records file definitions, follows invocations and tracks literal cd/pushd operands.
func (f *hereDocFileFlow) command(words []string, start, end int) error {
	program, args := hereDocLiteralCommand(strings.Join(words, " "))
	file := hereDocResolvedFile(hereDocScriptFile(program, args), f.dir)
	if err := f.invoke(file, start); err != nil {
		return err
	}
	f.define(start, end)
	if (program == "cd" || program == "pushd") && len(args) > 0 {
		f.dir = hereDocResolvedFile(literalHereDocWord(args[0]), f.dir)
	}
	return nil
}

// define resolves literal output targets at the owning command's directory.
func (f *hereDocFileFlow) define(start, end int) {
	for i := 0; i < len(f.docs) && i < maxShellHeredocs; i++ {
		if f.docs[i].offset < start || f.docs[i].offset > end {
			continue
		}
		for j := 0; j < len(f.docs[i].targets) && j < MaxShellFields; j++ {
			f.targets[i] = append(f.targets[i], hereDocResolvedFile(f.docs[i].targets[j], f.dir))
		}
	}
}

// invoke inserts each matching body before its later command, preserving the command's directory.
func (f *hereDocFileFlow) invoke(file string, start int) error {
	if file == "" {
		return nil
	}
	for i := 0; i < len(f.docs) && i < maxShellHeredocs; i++ {
		if f.docs[i].offset >= start || !slices.Contains(f.targets[i], file) {
			continue
		}
		body := "(\n" + f.docs[i].Text + "\n)\n"
		if len(f.code)+f.growth+len(body) > 1<<20 {
			return fmt.Errorf("here-document expansion exceeds byte limit")
		}
		f.output.WriteString(f.code[f.written:start])
		f.output.WriteString(body)
		f.written = start
		f.growth += len(body)
		f.docs[i].executes = true
	}
	return nil
}

// hereDocResolvedFile resolves only literal paths against a known lexical directory.
func hereDocResolvedFile(file, dir string) string {
	if file == "" {
		return ""
	}
	if path.IsAbs(file) {
		return path.Clean(file)
	}
	if dir == "" {
		return ""
	}
	return path.Join(dir, file)
}
