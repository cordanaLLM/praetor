// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"regexp"
	"strings"
)

// hereDocShape contains only proven redirects and the command left after their removal.
type hereDocShape struct {
	command    string
	stdin      int
	inputs     uint16
	count      int
	output     bool
	targets    []string
	appendFile bool
	reason     string
}

var hereDocRedirect = regexp.MustCompile(`^([0-9]*)(<<-?|>>?|<)`)

// hereDocRedirectStart includes a descriptor only when it forms its own shell word.
func hereDocRedirectStart(text string, at int) int {
	start := at
	for i := at - 1; i >= 0 && i < maxHereDocLineBytes; i-- {
		if text[i] < '0' || text[i] > '9' {
			break
		}
		start = i
	}
	if start > 0 && !strings.ContainsRune(" \t;|&(", rune(text[start-1])) {
		return at
	}
	return start
}

// readHereDocShape orders the bounded redirect stream, failing closed on unsupported forms.
func readHereDocShape(line string, spans [][2]int, start, end int) hereDocShape {
	shape := hereDocShape{stdin: -1}
	var command strings.Builder
	var quote QuoteStepper
	for i := start; i < end && i < maxHereDocLineBytes; i++ {
		if !quote.Open() {
			if next := shape.document(line, spans, i, end); next > i {
				i = next - 1
				continue
			}
			if next := shape.redirect(line, i, end); next > i {
				i = next - 1
				continue
			}
		}
		next := quote.Step(line, i)
		command.WriteString(line[i : next+1])
		i = next
	}
	shape.command = command.String()
	return shape
}

// document records one here-document's descriptor and effective stdin position.
func (s *hereDocShape) document(line string, spans [][2]int, at, end int) int {
	for i := 0; i < len(spans) && i < maxShellHeredocs; i++ {
		span := spans[i]
		if span[0] != at || span[1] > end {
			continue
		}
		match := hereDocRedirect.FindStringSubmatch(line[at:span[1]])
		if len(match) != 3 {
			s.reason = "unproven here-document descriptor"
			return span[1]
		}
		s.count++
		if strings.TrimLeft(match[1], "0") == "" {
			s.stdin = i
			s.inputs |= uint16(1) << i
		}
		return span[1]
	}
	return at
}

// redirect admits literal file redirects; descriptor duplication and dynamic targets stay unproven.
func (s *hereDocShape) redirect(line string, at, end int) int {
	if hereDocEmbeddedDescriptor(line, at) {
		return at
	}
	match := hereDocRedirect.FindStringSubmatch(line[at:end])
	if len(match) != 3 {
		return at
	}
	if strings.HasPrefix(match[2], "<<") {
		s.reason = "unproven redirect chain"
		return end
	}
	rest := line[at+len(match[0]) : end]
	begin := end - len(strings.TrimLeft(rest, " \t"))
	stop := hereDocWordEnd(line, begin, end)
	file := literalHereDocWord(line[begin:stop])
	if file == "" {
		s.reason = "unproven redirect target or descriptor chain"
		return end
	}
	s.recordFileRedirect(match[1], match[2], file)
	return stop
}

// hereDocEmbeddedDescriptor keeps digits inside an operand from becoming a descriptor.
func hereDocEmbeddedDescriptor(line string, at int) bool {
	return at > 0 && line[at] >= '0' && line[at] <= '9' && !strings.ContainsRune(" \t", rune(line[at-1]))
}

// recordFileRedirect updates only the descriptor a proven file redirection replaces.
func (s *hereDocShape) recordFileRedirect(fd, operator, file string) {
	if fd == "" {
		if operator == "<" {
			fd = "0"
		} else {
			fd = "1"
		}
	}
	fd = strings.TrimLeft(fd, "0")
	if fd == "" {
		s.stdin = -1
	}
	if fd == "1" {
		if s.output {
			s.reason = "unproven multiple output redirects"
		}
		s.output = true
		s.targets = []string{file}
		s.appendFile = operator == ">>"
	}
}

// hereDocWordEnd reads one quoted or unquoted word without expanding it.
func hereDocWordEnd(text string, start, end int) int {
	var quote QuoteStepper
	for i := start; i < end && i < maxHereDocLineBytes; i++ {
		if !quote.Open() && strings.ContainsRune(" \t\r\n;&|<>()", rune(text[i])) {
			return i
		}
		i = quote.Step(text, i)
	}
	return end
}

// classifyHereDoc is the ownership allow-list; its default never asserts execution.
func classifyHereDoc(shape hereDocShape, index int, tail string) HereDoc {
	program, args := hereDocLiteralCommand(shape.command)
	doc := HereDoc{Command: program, Reason: shape.reason, data: hereDocDataReader(program)}
	if doc.Reason != "" {
		return doc
	}
	name := hereDocBase(program)
	if !hereDocDataReader(program) && !shellLanguages[name] {
		doc.Reason = "unproven here-document owner: " + program
		return doc
	}
	if shape.stdin != index {
		return hereDocInactiveOwner(shape, doc, index)
	}
	if shellLanguages[name] {
		return hereDocShellOwner(doc, args)
	}
	return hereDocDataOwner(shape, doc, args, tail)
}

// hereDocInactiveOwner distinguishes replaced stdin from a descriptor the command might still read.
func hereDocInactiveOwner(shape hereDocShape, doc HereDoc, index int) HereDoc {
	doc.data = true
	if shape.inputs&(uint16(1)<<index) == 0 {
		doc.Reason = "unproven non-stdin here-document descriptor"
	} else if shape.stdin < 0 {
		doc.Reason = "unproven replaced standard input"
	}
	return doc
}

// hereDocDataOwner proves data-reader operands before following files or a forwarding pipe.
func hereDocDataOwner(shape hereDocShape, doc HereDoc, args []string, tail string) HereDoc {
	if hereDocBase(doc.Command) == "cat" && shape.output && !hereDocForwards(shape.command) {
		doc.Reason = "unproven cat stdin operands"
		return doc
	}
	if hereDocBase(doc.Command) == "tee" && !hereDocTeeArgs(args) {
		doc.Reason = "unproven tee arguments or file operand"
		return doc
	}
	doc.targets, doc.appendFile = hereDocFileTargets(shape), shape.appendFile || hereDocTeeAppend(doc.Command, args)
	doc.executes = hereDocPipeShell(shape, tail)
	if !doc.executes {
		return hereDocPipeData(shape, doc, tail)
	}
	return doc
}

// hereDocShellOwner admits shell source only when its option and operand proof succeeds.
func hereDocShellOwner(doc HereDoc, args []string) HereDoc {
	if hereDocStdinArgs(args) {
		doc.executes = true
	} else {
		doc.Reason = "unproven shell stdin arguments (-c or script operand)"
	}
	return doc
}

// hereDocStdinArgs proves a shell reads stdin, rejecting command strings and script operands.
func hereDocStdinArgs(args []string) bool {
	for i := 0; i < len(args) && i < MaxShellFields; i++ {
		arg := literalHereDocWord(args[i])
		if strings.Contains(arg, "c") || !strings.HasPrefix(arg, "-") {
			return false
		}
		if arg == "--" {
			return i == len(args)-1
		}
		if len(arg) < 2 || strings.Trim(arg[1:], "seuxv") != "" {
			return false
		}
	}
	return true
}

// hereDocPipeShell proves a single forwarding stage supplies a shell's standard input.
func hereDocPipeShell(source hereDocShape, tail string) bool {
	if source.output || !hereDocForwards(source.command) {
		return false
	}
	if !strings.HasPrefix(tail, "|") || strings.HasPrefix(tail, "||") {
		return false
	}
	command, err := normalizeShellCode(tail[1:])
	if err != nil {
		return false
	}
	_, end := hereDocCommandBounds(command, 0)
	if end < len(command) && command[end] == '|' {
		return false
	}
	program, args := hereDocLiteralCommand(command[:end])
	return shellLanguages[hereDocBase(program)] && hereDocStdinArgs(args)
}

// hereDocForwards allows cat stdin and tee copies without a competing input operand.
func hereDocForwards(command string) bool {
	program, args := hereDocLiteralCommand(command)
	if hereDocBase(program) == "cat" {
		return len(args) == 0 || (len(args) == 1 && literalHereDocWord(args[0]) == "-")
	}
	return hereDocBase(program) == "tee" && hereDocTeeArgs(args)
}

// hereDocDataReader enumerates commands whose own stdin is data rather than shell source.
func hereDocDataReader(program string) bool {
	switch hereDocBase(program) {
	case "cat", "tee", "gh":
		return true
	}
	return false
}

// hereDocTeeAppend recognizes tee's two admitted append spellings.
func hereDocTeeAppend(program string, args []string) bool {
	if hereDocBase(program) != "tee" {
		return false
	}
	for i := 0; i < len(args) && i < MaxShellFields; i++ {
		arg := literalHereDocWord(args[i])
		if arg == "-a" || arg == "--append" {
			return true
		}
	}
	return false
}

// hereDocPipeData admits a proven data sink at the end of a single forwarding pipe.
func hereDocPipeData(source hereDocShape, doc HereDoc, tail string) HereDoc {
	if !strings.HasPrefix(tail, "|") || strings.HasPrefix(tail, "||") {
		return doc
	}
	if source.output || !hereDocForwards(source.command) {
		doc.Reason = "unproven pipe receiver, forwarding stage or shell stdin arguments"
		return doc
	}
	command, err := normalizeShellCode(tail[1:])
	if err != nil {
		doc.Reason = "unproven pipe receiver fields"
		return doc
	}
	_, end := hereDocCommandBounds(command, 0)
	sink := readHereDocShape(command, nil, 0, end)
	program, args := hereDocLiteralCommand(sink.command)
	if !hereDocDataReader(program) || end != len(command) {
		doc.Reason = "unproven pipe receiver, forwarding stage or shell stdin arguments"
		return doc
	}
	return hereDocPipeSink(doc, sink, program, args)
}

// hereDocTeeArgs proves literal file operands and the tee options documented to copy stdin.
func hereDocTeeArgs(args []string) bool {
	for i := 0; i < len(args) && i < MaxShellFields; i++ {
		arg := literalHereDocWord(args[i])
		switch arg {
		case "-a", "--append", "-i", "--ignore-interrupts":
			continue
		}
		if arg == "" || strings.HasPrefix(arg, "-") {
			return false
		}
	}
	return true
}

// hereDocPipeSink validates a data sink's redirects and operands and records piped file targets.
func hereDocPipeSink(doc HereDoc, sink hereDocShape, program string, args []string) HereDoc {
	doc.Reason = hereDocPipeDataReason(sink, program, args)
	if doc.Reason != "" {
		return doc
	}
	if targets := hereDocFileTargets(sink); len(targets) > 0 {
		doc.targets = append(doc.targets, targets...)
		doc.pipedWrite = true
	}
	return doc
}

// hereDocPipeDataReason checks a piped data sink's redirects and argument allow-list.
func hereDocPipeDataReason(sink hereDocShape, program string, args []string) string {
	if sink.reason != "" {
		return "unproven piped file definition"
	}
	switch hereDocBase(program) {
	case "tee":
		if sink.output || !hereDocTeeArgs(args) {
			return "unproven tee arguments or file operand"
		}
	case "cat":
		if !hereDocForwards(sink.command) {
			return "unproven cat stdin operands"
		}
	case "gh":
		if sink.output {
			return "unproven piped file definition"
		}
	}
	return ""
}
