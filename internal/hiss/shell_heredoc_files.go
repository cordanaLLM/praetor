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

// hereDocLiteralWord admits unquoted non-expanding filename bytes and quoted literal spans.
// Bash 5.3 QUOTING: single quotes preserve all bytes except the closing quote;
// double quotes preserve all except expansion markers and special backslash pairs.
var hereDocLiteralWord = regexp.MustCompile(`^[A-Za-z0-9_./+:=-]$`)

// Bash parameter names, special parameters and expansion opening delimiters.
var hereDocExpansionStart = regexp.MustCompile(`^[A-Za-z0-9_@*#?$!({\[-]$`)

// literalHereDocWord decodes a static shell word once for both definitions and invocations.
func literalHereDocWord(word string) string {
	var out strings.Builder
	for i := 0; i < len(word) && i < maxHereDocLineBytes; i++ {
		switch word[i] {
		case '\'':
			end := strings.IndexByte(word[i+1:], '\'')
			if end < 0 {
				return ""
			}
			out.WriteString(word[i+1 : i+1+end])
			i += end + 1
		case '"':
			value, width, ok := hereDocDoubleWord(word[i+1:])
			if !ok {
				return ""
			}
			out.WriteString(value)
			i += width
		default:
			if !hereDocLiteralWord.MatchString(word[i : i+1]) {
				return ""
			}
			out.WriteByte(word[i])
		}
	}
	return out.String()
}

// hereDocDoubleWord decodes non-expanding double-quoted bytes using Bash QUOTING rules.
func hereDocDoubleWord(text string) (string, int, bool) {
	var out strings.Builder
	for i := 0; i < len(text) && i < maxHereDocLineBytes; i++ {
		switch text[i] {
		case '"':
			return out.String(), i + 1, true
		case '$', '`':
			if !hereDocLiteralMark(text[i:]) {
				return "", 0, false
			}
		case '\\':
			if i+1 < len(text) && strings.ContainsRune("$`\"\\\n", rune(text[i+1])) {
				i++
				if text[i] == '\n' {
					continue
				}
			}
		}
		out.WriteByte(text[i])
	}
	return "", 0, false
}

// hereDocLiteralCommand reads an assignment-prefixed command and unwraps privilege wrappers.
func hereDocLiteralCommand(text string) (string, []string) {
	match := hereDocCommand.FindStringSubmatchIndex(text)
	if len(match) < 4 {
		return "", nil
	}
	args, err := ShellCodeFields(text[match[3]:])
	if err != nil {
		return "", nil
	}
	return hereDocUnwrapCommand(literalHereDocWord(text[match[2]:match[3]]), args)
}

// hereDocUnwrapCommand strips privilege wrappers whose non-command options are proven.
func hereDocUnwrapCommand(program string, args []string) (string, []string) {
	for depth := 0; depth < maxShellWrappers && hereDocWrapper(program); depth++ {
		next, rest, ok := hereDocStepWrapper(hereDocBase(program), args)
		if !ok || next == "cd" || next == "pushd" {
			return program, args
		}
		program, args = next, rest
	}
	return program, args
}

// hereDocWrapper names the privilege and environment launchers around an owner command.
func hereDocWrapper(program string) bool {
	_, ok := ShellWrapperOptions[hereDocBase(program)]
	return ok
}

// hereDocStepWrapper consumes one wrapper's non-command options and environment assignments.
func hereDocStepWrapper(wrapper string, args []string) (string, []string, bool) {
	for i := 0; i < len(args) && i < MaxShellFields; i++ {
		word := literalHereDocWord(args[i])
		if word == "--" {
			return hereDocNextProgram(wrapper, args[i+1:])
		}
		if strings.HasPrefix(word, "-") {
			step, ok := hereDocWrapperOption(wrapper, word, args[i+1:])
			if !ok {
				return "", nil, false
			}
			i += step
			continue
		}
		if hereDocWrapperAssignment(wrapper, word) {
			continue
		}
		return word, args[i+1:], word != ""
	}
	return "", nil, false
}

// hereDocNextProgram reads the literal command word after a wrapper's option terminator.
func hereDocNextProgram(wrapper string, args []string) (string, []string, bool) {
	for i := 0; i < len(args) && i < MaxShellFields; i++ {
		word := literalHereDocWord(args[i])
		if hereDocWrapperAssignment(wrapper, word) {
			continue
		}
		return word, args[i+1:], word != "" && !strings.HasPrefix(word, "-")
	}
	return "", nil, false
}

// hereDocWrapperAssignment recognizes wrapper-level environment assignments outside doas.
func hereDocWrapperAssignment(wrapper, word string) bool {
	return wrapper != "doas" && shellAssignment.MatchString(word)
}

// hereDocWrapperOption validates one non-command wrapper flag and its value arity.
func hereDocWrapperOption(wrapper, option string, rest []string) (int, bool) {
	spec, ok := ShellWrapperOptions[wrapper]
	if !ok {
		return 0, false
	}
	if slices.Contains(spec.FlagLong, option) || slices.Contains(spec.OptionalValueLong, option) {
		return 0, true
	}
	if !strings.HasPrefix(option, "--") {
		return hereDocShortOption(option, spec.FlagShort, spec.ValueShort, rest)
	}
	if slices.Contains(spec.ValueLong, option) {
		return hereDocOptionValue(rest)
	}
	name, value, attached := strings.Cut(option, "=")
	return 0, attached && value != "" && (slices.Contains(spec.ValueLong, name) || slices.Contains(spec.OptionalValueLong, name))
}

// hereDocShortOption parses a short option cluster with optional trailing value letter.
func hereDocShortOption(option, flags, values string, rest []string) (int, bool) {
	for i := 1; i < len(option) && i < maxHereDocLineBytes; i++ {
		c := rune(option[i])
		if strings.ContainsRune(flags, c) {
			continue
		}
		if !strings.ContainsRune(values, c) {
			return 0, false
		}
		if i+1 < len(option) {
			return 0, true
		}
		return hereDocOptionValue(rest)
	}
	return 0, len(option) > 1
}

// hereDocOptionValue consumes one literal option argument from rest.
func hereDocOptionValue(rest []string) (int, bool) {
	if len(rest) == 0 || literalHereDocWord(rest[0]) == "" {
		return 0, false
	}
	return 1, true
}

// hereDocFileTargets admits only commands that copy exactly stdin into literal files.
func hereDocFileTargets(shape hereDocShape) []string {
	program, args := hereDocLiteralCommand(shape.command)
	if hereDocBase(program) == "cat" && hereDocForwards(shape.command) {
		return shape.targets
	}
	if hereDocBase(program) != "tee" || !hereDocForwards(shape.command) {
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
	definitions     map[string][]int
	depth           int
	uncertain       string
	dir             string
	output          strings.Builder
	written, growth int
}

// followHereDocFiles exposes literal scripts at later invocations in the same script.
// Unproven file flows receive no execution credit; no file is read or command executed.
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
	flow := hereDocFileFlow{code: normalized, docs: docs, definitions: make(map[string][]int), dir: "."}
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
			if err := f.command(scan.fields[start:i], scan.starts[start], end, hereDocStandalone(scan.fields, start, i)); err != nil {
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

// hereDocStandalone excludes pipeline stages and background commands from parent state changes.
func hereDocStandalone(fields []string, start, end int) bool {
	if start > 0 && fields[start-1] == "|" {
		return false
	}
	return end >= len(fields) || (fields[end] != "|" && fields[end] != "&")
}

// command follows invocation-time definitions and changes only the parent shell's directory.
func (f *hereDocFileFlow) command(words []string, start, end int, standalone bool) error {
	text := strings.Join(words, " ")
	depth := f.depth
	grouped := f.trackGroups(text)
	owned := standalone && depth == 0 && !grouped
	program, args := hereDocLiteralCommand(text)
	f.nonDocumentWrite(text, start, end)
	file := f.invocationFile(program, args, start, depth != 0 || grouped)
	if err := f.invoke(file, start); err != nil {
		return err
	}
	if depth != 0 || grouped {
		f.unprovenDefinitions(start, end)
	}
	f.define(start, end)
	if owned && (program == "cd" || program == "pushd") {
		f.changeDir(args)
	}
	return nil
}

// invocationFile declines grouped and uncertain relative invocations with a named reason.
func (f *hereDocFileFlow) invocationFile(program string, args []string, start int, grouped bool) string {
	operand := hereDocScriptFile(program, args)
	if operand == "" {
		if hereDocUnknownInvocation(program, args) {
			f.unprovenFiles(start, "unproven script invocation: non-literal operand")
		}
		return ""
	}
	if grouped {
		f.unprovenFiles(start, "unproven grouped script invocation")
		return ""
	}
	if f.uncertain != "" {
		f.unprovenFiles(start, f.uncertain)
		return ""
	}
	return hereDocResolvedFile(operand, f.dir)
}

// trackGroups bounds lexical subshell nesting without treating quoted parentheses as groups.
func (f *hereDocFileFlow) trackGroups(text string) bool {
	var quote QuoteStepper
	grouped := false
	for i := 0; i < len(text) && i < maxHereDocLineBytes; i++ {
		if !quote.Open() {
			if text[i] == '(' {
				grouped = true
				f.depth = min(f.depth+1, maxShellNesting)
			}
			if text[i] == ')' {
				grouped = true
				f.depth = max(f.depth-1, 0)
			}
		}
		i = quote.Step(text, i)
	}
	return grouped
}

// changeDir invalidates relative resolution when a parent directory target is not literal.
func (f *hereDocFileFlow) changeDir(args []string) {
	target := ""
	if len(args) == 1 {
		target = literalHereDocWord(args[0])
	}
	f.dir = hereDocResolvedFile(target, f.dir)
	if f.dir == "" {
		f.uncertain = "unproven directory change: non-literal cd/pushd target"
	}
}

// define replaces truncating definitions and extends append definitions by resolved path.
func (f *hereDocFileFlow) define(start, end int) {
	for i := 0; i < len(f.docs) && i < maxShellHeredocs; i++ {
		doc := &f.docs[i]
		if doc.offset < start || doc.offset > end {
			continue
		}
		if doc.Reason != "" {
			f.uncertain = doc.Reason
			continue
		}
		for j := 0; j < len(doc.targets) && j < MaxShellFields; j++ {
			f.defineTarget(i, doc.targets[j])
		}
	}
}

// defineTarget replaces a resolved file or appends only to an already proven definition.
func (f *hereDocFileFlow) defineTarget(index int, target string) {
	doc := &f.docs[index]
	file := hereDocResolvedFile(target, f.dir)
	if doc.pipedWrite {
		f.definitions[file] = []int{index}
		return
	}
	if !doc.appendFile {
		f.definitions[file] = nil
	}
	f.definitions[file] = append(f.definitions[file], index)
}

// invoke emits the live file as one script stream, preserving state across append fragments.
func (f *hereDocFileFlow) invoke(file string, start int) error {
	if file == "" || f.uncertain != "" {
		return nil
	}
	indices := f.definitions[file]
	if len(indices) == 0 || f.unprovenDefinition(indices) {
		return nil
	}
	if len(indices) > maxShellHeredocs {
		return fmt.Errorf("here-document file definition count exceeds %d", maxShellHeredocs)
	}
	var body strings.Builder
	body.WriteString("(\n")
	for j := 0; j < len(indices) && j < maxShellHeredocs; j++ {
		i := indices[j]
		if len(f.code)+f.growth+body.Len()+len(f.docs[i].Text)+3 > 1<<20 {
			return fmt.Errorf("here-document expansion exceeds byte limit")
		}
		body.WriteString(f.docs[i].Text)
		f.docs[i].executes = true
	}
	body.WriteString("\n)\n")
	f.output.WriteString(f.code[f.written:start])
	f.output.WriteString(body.String())
	f.written = start
	f.growth += body.Len()
	return nil
}

// unprovenDefinition declines script execution when the base definition came from a pipe or bare append.
func (f *hereDocFileFlow) unprovenDefinition(indices []int) bool {
	if i := indices[0]; f.docs[i].appendFile && !f.docs[i].pipedWrite {
		f.docs[i].Reason = "unproven append without file definition"
		return true
	}
	for j := 0; j < len(indices) && j < maxShellHeredocs; j++ {
		if i := indices[j]; f.docs[i].pipedWrite {
			f.docs[i].Reason = "unproven piped file definition"
			return true
		}
	}
	return false
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

// unprovenFiles labels earlier candidate bodies when relative invocation state cannot be proved.
func (f *hereDocFileFlow) unprovenFiles(start int, reason string) {
	for i := 0; i < len(f.docs) && i < maxShellHeredocs; i++ {
		if f.docs[i].offset < start && !f.docs[i].executes && len(f.docs[i].targets) > 0 {
			f.docs[i].Reason = reason
		}
	}
}

// hereDocLiteralMark admits a dollar that cannot start Bash parameter or command expansion.
func hereDocLiteralMark(text string) bool {
	if text[0] == '`' {
		return false
	}
	return len(text) == 1 || !hereDocExpansionStart.MatchString(text[1:2])
}

// hereDocUnknownInvocation names source/shell operands outside the literal invocation allow-list.
func hereDocUnknownInvocation(program string, args []string) bool {
	if len(args) == 0 {
		return false
	}
	return program == "." || program == "source" || (shellLanguages[hereDocBase(program)] && !hereDocStdinArgs(args))
}

// nonDocumentWrite invalidates flow when a write has no tracked here-document definition.
func (f *hereDocFileFlow) nonDocumentWrite(text string, start, end int) {
	for i := 0; i < len(f.docs) && i < maxShellHeredocs; i++ {
		if f.docs[i].offset >= start && f.docs[i].offset <= end {
			return
		}
	}
	shape := readHereDocShape(text, nil, 0, len(text))
	if shape.reason != "" {
		f.uncertain = "unproven non-here-document file write"
		return
	}
	for i := 0; i < len(shape.targets) && i < MaxShellFields; i++ {
		file := hereDocResolvedFile(shape.targets[i], f.dir)
		if len(f.definitions[file]) > 0 {
			f.uncertain = "unproven non-here-document file write"
		}
	}
}

// unprovenDefinitions declines file paths defined inside an unmodelled grouped shell context.
func (f *hereDocFileFlow) unprovenDefinitions(start, end int) {
	for i := 0; i < len(f.docs) && i < maxShellHeredocs; i++ {
		if f.docs[i].offset >= start && f.docs[i].offset <= end && len(f.docs[i].targets) > 0 {
			f.docs[i].Reason = "unproven grouped file definition"
		}
	}
}
