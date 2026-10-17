// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"fmt"
	"strings"
)

func (s *pythonScanner) addGuard(target string) {
	if target == "" {
		return
	}
	for i := len(s.open) - 1; i >= 0; i-- {
		if !s.open[i].class {
			s.open[i].guards = appendUniqueGuard(s.open[i].guards, target)
			return
		}
	}
	s.moduleGuards = appendUniqueGuard(s.moduleGuards, target)
}

func (s *pythonScanner) isGuarded(target string) bool {
	if target == "" {
		return false
	}
	for i := len(s.open) - 1; i >= 0; i-- {
		if !s.open[i].class {
			return containsGuard(s.open[i].guards, target)
		}
	}
	return containsGuard(s.moduleGuards, target)
}

func containsGuard(guards []string, target string) bool {
	for _, g := range guards {
		if g == target {
			return true
		}
	}
	return false
}

func (s *pythonScanner) observePythonGuards(code string) {
	for _, g := range extractAllGuards(code) {
		s.addGuard(g)
	}
}

func (s *pythonScanner) observePythonSignals(code string, lineNum int) {
	s.checkPythonKillpg(code, lineNum)
	s.checkPythonOsKill(code, lineNum)
}

func (s *pythonScanner) checkPythonKillpg(code string, lineNum int) {
	at := nextIdent(code, "killpg", 0)
	for i := 0; i < len(code) && at >= 0; i++ {
		if opensArgs(code, at+len("killpg")) && !isDefAt(code, at) {
			openParen := strings.IndexByte(code[at:], '(') + at
			args, _ := parseCallArgs(code, openParen)
			if len(args) >= 2 {
				target := stripLeadingMinus(args[0])
				second := strings.TrimSpace(args[1])
				if second != "0" && !s.isGuarded(target) {
					recordViolation(s.rep, "HISS-08", s.rel, lineNum, "",
						fmt.Sprintf("Signal call os.killpg with target %s must be guarded; add guard: if not isinstance(%s, int) or isinstance(%s, bool) or %s <= 1 or %s == os.getpgrp(): ...",
							target, target, target, target, target))
				}
			}
		}
		at = nextIdent(code, "killpg", at+1)
	}
}

func (s *pythonScanner) checkPythonOsKill(code string, lineNum int) {
	at := nextIdent(code, "kill", 0)
	for i := 0; i < len(code) && at >= 0; i++ {
		if isOsKillOrBare(code, at) && !isDefAt(code, at) {
			if target, sig, ok := parseNegativeSignalCall(code, at, "kill"); ok {
				if sig != "0" && !s.isGuarded(target) {
					recordViolation(s.rep, "HISS-08", s.rel, lineNum, "",
						fmt.Sprintf("Signal call os.kill with target %s must be guarded; add guard: if not isinstance(%s, int) or isinstance(%s, bool) or %s <= 1 or %s == os.getpgrp(): ...",
							target, target, target, target, target))
				}
			}
		}
		at = nextIdent(code, "kill", at+1)
	}
}

func isDefAt(code string, at int) bool {
	prefix := strings.TrimSpace(code[:at])
	return strings.HasSuffix(prefix, "def") || strings.HasSuffix(prefix, "def async")
}

func isOsKillOrBare(code string, at int) bool {
	if at == 0 {
		return true
	}
	if code[at-1] == '.' {
		return strings.HasSuffix(strings.TrimSpace(code[:at-1]), "os")
	}
	return !isIdentByte(code[at-1])
}
