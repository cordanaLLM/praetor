// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"strings"
)

// systemd service and socket units.
//
// The scanner reads .service and .socket files that open a unit section ([Unit], [Service],
// [Socket] or [Install]); any other file with those extensions is not a unit and stays unscanned.
// It follows systemd.syntax(7): a line ending in a backslash continues on the next, a line
// starting with # or ; is a comment, and the last assignment of a single-valued setting wins,
// while an empty one resets it. It decides:
//
//   - HISS-02 a Type=oneshot service with no TimeoutStartSec= or TimeoutSec=, whose start
//     timeout systemd disables by default (systemd.service(5)); a start, stop or socket timeout
//     set to infinity or 0, which both disable it; and a restarting service (Restart= other than
//     no) whose StartLimitIntervalSec= is 0, which turns off the rate limit that stops a restart
//     loop (systemd.unit(5)).
//   - HISS-07 an Exec*= command prefixed with "-", whose failure systemd records but otherwise
//     treats as success.
//
// A drop-in (.conf) that overrides a unit is a separate file the scanner does not read, so a
// finding, or its absence, is about the unit file as written.

const (
	// maxUnitLines bounds the physical lines joined into one unit setting (HISS-02).
	maxUnitLines = 256
	// maxUnitSettings bounds the settings one unit file records (HISS-02).
	maxUnitSettings = 4096
)

// unitSections are the sections that make a .service or .socket file a unit.
var unitSections = map[string]bool{"Unit": true, "Service": true, "Socket": true, "Install": true}

// unitTimeouts are the timeout settings whose infinity or 0 disables the timeout, per section.
var unitTimeouts = []string{
	"Service.TimeoutStartSec", "Service.TimeoutStopSec", "Service.TimeoutSec", "Service.TimeoutAbortSec",
	"Socket.TimeoutSec",
}

// systemdLanguage reads systemd service and socket units.
type systemdLanguage struct{}

// handles claims no extension: a .service or .socket file is a unit only when it opens a unit
// section, which claims decides.
func (systemdLanguage) handles(string) bool { return false }

func (systemdLanguage) name() string { return "systemd" }

func (systemdLanguage) candidate(_, ext string) bool { return ext == ".service" || ext == ".socket" }

func (systemdLanguage) claims(src sourceFile) bool {
	for _, line := range src.lfLines() {
		if section, ok := unitSection(strings.TrimSpace(line)); ok {
			return unitSections[section]
		}
	}
	return false
}

func (systemdLanguage) scan(src sourceFile, rep *ScanReport, _ ScanOptions) bool {
	u := readUnit(src.lfLines())
	file := &ScanReport{}
	u.checkTimeouts(file, src.rel)
	u.checkRestart(file, src.rel)
	u.checkExec(file, src.rel)
	recordInLineOrder(rep, file)
	return true
}

// unitSection returns the name of the section a trimmed line opens.
func unitSection(trimmed string) (string, bool) {
	if len(trimmed) < 2 || trimmed[0] != '[' || trimmed[len(trimmed)-1] != ']' {
		return "", false
	}
	return trimmed[1 : len(trimmed)-1], true
}

// unitSetting is one assignment: Section.Key, its value and the line it starts on.
type unitSetting struct {
	key, value string
	line       int
}

// unitFile is a unit's settings in file order.
type unitFile struct {
	settings []unitSetting
}

// readUnit reads a unit's assignments, joining continued lines and skipping comments.
func readUnit(lines []string) unitFile {
	u := unitFile{}
	section := ""
	for i := 0; i < len(lines) && len(u.settings) < maxUnitSettings; i++ {
		start := i
		text, next := joinUnitLine(lines, i)
		i = next
		if name, ok := unitSection(text); ok {
			section = name
			continue
		}
		key, value, ok := strings.Cut(text, "=")
		if !ok || text == "" || text[0] == '#' || text[0] == ';' {
			continue
		}
		u.settings = append(u.settings, unitSetting{
			key: section + "." + strings.TrimSpace(key), value: strings.TrimSpace(value), line: start + 1,
		})
	}
	return u
}

// joinUnitLine returns the trimmed setting that starts at line i, with every continuation line
// a trailing backslash pulls in, and the index of its last line. A comment line inside a
// continuation is skipped, as systemd.syntax(7) says.
func joinUnitLine(lines []string, i int) (string, int) {
	text := strings.TrimSpace(lines[i])
	if text != "" && (text[0] == '#' || text[0] == ';') {
		return text, i
	}
	for n := 0; n < maxUnitLines && strings.HasSuffix(text, `\`) && i+1 < len(lines); n++ {
		i++
		next := strings.TrimSpace(lines[i])
		if next != "" && (next[0] == '#' || next[0] == ';') {
			continue
		}
		text = strings.TrimSuffix(text, `\`) + " " + next
	}
	return strings.TrimSpace(text), i
}

// last returns the setting key ends with: its last assignment, or the zero setting when it is
// unset or its last assignment is empty.
func (u unitFile) last(key string) unitSetting {
	found := unitSetting{}
	for _, s := range u.settings {
		if s.key == key {
			found = s
		}
	}
	if found.value == "" {
		return unitSetting{}
	}
	return found
}

// checkTimeouts reports a oneshot service without a start timeout and a timeout that is
// disabled.
func (u unitFile) checkTimeouts(rep *ScanReport, rel string) {
	if typ := u.last("Service.Type"); typ.value == "oneshot" &&
		u.last("Service.TimeoutStartSec").value == "" && u.last("Service.TimeoutSec").value == "" {
		recordViolation(rep, "HISS-02", rel, typ.line, "",
			"Type=oneshot service sets no TimeoutStartSec=; systemd disables the start timeout for oneshot, so a hung command blocks forever")
	}
	for _, key := range unitTimeouts {
		if s := u.last(key); s.value != "" && disablesTimeout(s.value) {
			name := key[strings.IndexByte(key, '.')+1:]
			recordViolation(rep, "HISS-02", rel, s.line, "", name+"="+s.value+" disables the timeout; set a finite bound")
		}
	}
}

// disablesTimeout reports a time span of infinity or zero, which systemd reads as no timeout.
func disablesTimeout(value string) bool {
	return value == "infinity" || isZeroSpan(value)
}

// isZeroSpan reports a time span whose every number is zero, such as 0, 0s or 0min.
func isZeroSpan(value string) bool {
	digits := false
	for i := 0; i < len(value); i++ {
		switch c := value[i]; {
		case c == '0':
			digits = true
		case c >= '1' && c <= '9':
			return false
		case c == '.', c == ' ', c >= 'a' && c <= 'z':
		default:
			return false
		}
	}
	return digits
}

// checkRestart reports a restarting service whose start rate limit is turned off.
func (u unitFile) checkRestart(rep *ScanReport, rel string) {
	restart := u.last("Service.Restart")
	if restart.value == "" || restart.value == "no" {
		return
	}
	for _, key := range []string{"Unit.StartLimitIntervalSec", "Unit.StartLimitInterval", "Service.StartLimitInterval"} {
		if s := u.last(key); s.value != "" && isZeroSpan(s.value) {
			recordViolation(rep, "HISS-02", rel, s.line, "",
				"Restart="+restart.value+" with "+key[strings.IndexByte(key, '.')+1:]+"=0 restarts without a rate limit, so a failing service restarts forever")
		}
	}
}

// checkExec reports every Exec*= command whose "-" prefix ignores its failure. A setting may
// carry several commands separated by a lone semicolon, each with its own prefix.
func (u unitFile) checkExec(rep *ScanReport, rel string) {
	for _, s := range u.settings {
		section, key, _ := strings.Cut(s.key, ".")
		if (section != "Service" && section != "Socket") || !strings.HasPrefix(key, "Exec") {
			continue
		}
		for _, command := range strings.Split(s.value, " ; ") {
			if strings.Contains(execPrefix(command), "-") {
				recordViolation(rep, "HISS-07", rel, s.line, "",
					key+"=- ignores the command's failure; handle it in the command or let the unit fail")
				break
			}
		}
	}
}

// execPrefix returns the special prefix characters of a command line (systemd.service(5)).
func execPrefix(command string) string {
	command = strings.TrimSpace(command)
	end := 0
	for end < len(command) && strings.IndexByte("@-:+!|", command[end]) >= 0 {
		end++
	}
	return command[:end]
}
