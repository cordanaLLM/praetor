package hiss

import (
	"fmt"
	"strings"
)

func scanNativeLines(lines []string, rel string, rep *ScanReport, opts ScanOptions) {
	inFunc := false
	funcStart := 0
	funcName := ""
	braceLevel := 0

	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		scanNativeLineInvariants(line, trimmed, rel, idx+1, rep)

		if !inFunc {
			if isNativeFuncHeader(line, trimmed) {
				inFunc = true
				funcStart = idx + 1
				braceLevel = strings.Count(line, "{") - strings.Count(line, "}")
				funcName = extractNativeFuncName(trimmed, lines, idx)
			}
		} else {
			braceLevel += strings.Count(line, "{") - strings.Count(line, "}")
			if braceLevel <= 0 {
				funcLen := (idx + 1) - funcStart + 1
				if funcLen > opts.MaxFuncLOC {
					recordViolation(rep, "HISS-04", rel, funcStart, funcName,
						fmt.Sprintf("Function '%s' (%d LOC) exceeds HISS-04 / NASA Rule 4 limit of %d LOC", funcName, funcLen, opts.MaxFuncLOC))
				}
				inFunc = false
			}
		}
	}
}

func scanNativeLineInvariants(line, trimmed, rel string, lineNum int, rep *ScanReport) {
	if strings.Contains(line, "while (1)") || strings.Contains(line, "while(1)") ||
		strings.Contains(line, "while (true)") || strings.Contains(line, "while(true)") ||
		strings.Contains(line, "for (;;)") || strings.Contains(line, "for(;;)") {
		recordViolation(rep, "HISS-02", rel, lineNum, "", "Legacy unbounded loop in native code")
	}
	if strings.Contains(line, "gets(") {
		recordViolation(rep, "HISS-09", rel, lineNum, "", "Banned unsafe gets() invocation")
	}
	if strings.Contains(line, "strcpy(") {
		recordViolation(rep, "HISS-09", rel, lineNum, "", "Banned unsafe strcpy() invocation; bounded string copy required")
	}
	if strings.Contains(line, "sprintf(") {
		recordViolation(rep, "HISS-09", rel, lineNum, "", "Banned unsafe sprintf() invocation; snprintf required")
	}
	if strings.HasPrefix(trimmed, "goto ") {
		recordViolation(rep, "HISS-01", rel, lineNum, "", "Legacy non-DAG control flow jump (goto)")
	}
}

func isNativeFuncHeader(line, trimmed string) bool {
	return strings.Contains(line, "{") && !strings.HasPrefix(trimmed, "//") &&
		!strings.HasPrefix(trimmed, "/*") && !strings.HasPrefix(trimmed, "struct ") &&
		!strings.HasPrefix(trimmed, "enum ") && !strings.HasPrefix(trimmed, "union ") &&
		!strings.HasPrefix(trimmed, "typedef ") && !strings.HasPrefix(trimmed, "class ") &&
		!strings.HasPrefix(trimmed, "#")
}

func extractNativeFuncName(trimmed string, lines []string, lineIdx int) string {
	name := trimmed
	if idx := strings.Index(name, "("); idx > 0 {
		parts := strings.Fields(name[:idx])
		if len(parts) > 0 {
			return strings.TrimPrefix(parts[len(parts)-1], "*")
		}
	} else if lineIdx > 0 {
		prev := strings.TrimSpace(lines[lineIdx-1])
		if idx2 := strings.Index(prev, "("); idx2 > 0 {
			parts := strings.Fields(prev[:idx2])
			if len(parts) > 0 {
				return strings.TrimPrefix(parts[len(parts)-1], "*")
			}
		}
	}
	return name
}

func scanPythonLines(lines []string, rel string, rep *ScanReport, opts ScanOptions) {
	inFunc := false
	funcStart := 0
	funcName := ""
	funcIndent := 0

	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		scanPythonLineInvariants(line, trimmed, rel, idx+1, rep)

		if strings.HasPrefix(trimmed, "def ") || strings.HasPrefix(trimmed, "async def ") {
			if inFunc {
				checkPythonFuncLen(funcStart, idx, funcName, rel, rep, opts.MaxFuncLOC)
			}
			inFunc = true
			funcStart = idx + 1
			funcIndent = len(line) - len(strings.TrimLeft(line, " \t"))
			funcName = extractPythonFuncName(trimmed)
		} else if inFunc && trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			currIndent := len(line) - len(strings.TrimLeft(line, " \t"))
			if currIndent <= funcIndent {
				checkPythonFuncLen(funcStart, idx, funcName, rel, rep, opts.MaxFuncLOC)
				inFunc = false
			}
		}
	}
	if inFunc {
		checkPythonFuncLen(funcStart, len(lines), funcName, rel, rep, opts.MaxFuncLOC)
	}
}

func scanPythonLineInvariants(line, trimmed, rel string, lineNum int, rep *ScanReport) {
	if strings.HasPrefix(trimmed, "while True:") {
		recordViolation(rep, "HISS-02", rel, lineNum, "", "Legacy unbounded while True loop in Python")
	}
	if strings.Contains(line, "eval(") || strings.Contains(line, "exec(") {
		recordViolation(rep, "HISS-09", rel, lineNum, "", "Unsafe dynamic eval/exec execution in Python")
	}
	if trimmed == "except:" || strings.HasPrefix(trimmed, "except: ") || strings.HasPrefix(trimmed, "except:#") {
		recordViolation(rep, "HISS-07", rel, lineNum, "", "Bare except catches and suppresses unhandled exceptions")
	}
}

func extractPythonFuncName(trimmed string) string {
	name := strings.TrimPrefix(trimmed, "async ")
	name = strings.TrimPrefix(name, "def ")
	if idx := strings.Index(name, "("); idx > 0 {
		name = strings.TrimSpace(name[:idx])
	}
	return name
}

func checkPythonFuncLen(start, end int, name, rel string, rep *ScanReport, maxLOC int) {
	funcLen := end - start + 1
	if funcLen > maxLOC {
		recordViolation(rep, "HISS-04", rel, start, name,
			fmt.Sprintf("Function '%s' (%d LOC) exceeds HISS-04 / NASA Rule 4 limit of %d LOC", name, funcLen, maxLOC))
	}
}

func scanGoLines(lines []string, rel string, rep *ScanReport, opts ScanOptions) {
	inFunc := false
	funcStart := 0
	funcName := ""
	braceLevel := 0

	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		scanGoLineInvariants(line, trimmed, rel, idx+1, rep)

		if !inFunc {
			if (strings.HasPrefix(trimmed, "func ") || strings.HasPrefix(trimmed, "func (")) && strings.Contains(line, "{") {
				inFunc = true
				funcStart = idx + 1
				braceLevel = strings.Count(line, "{") - strings.Count(line, "}")
				funcName = extractGoFuncName(trimmed)
			}
		} else {
			braceLevel += strings.Count(line, "{") - strings.Count(line, "}")
			if braceLevel <= 0 {
				funcLen := (idx + 1) - funcStart + 1
				if funcLen > opts.MaxFuncLOC {
					recordViolation(rep, "HISS-04", rel, funcStart, funcName,
						fmt.Sprintf("Function '%s' (%d LOC) exceeds HISS-04 / NASA Rule 4 limit of %d LOC", funcName, funcLen, opts.MaxFuncLOC))
				}
				inFunc = false
			}
		}
	}
}

func scanGoLineInvariants(line, trimmed, rel string, lineNum int, rep *ScanReport) {
	if strings.Contains(line, "_ = ") && !strings.Contains(rel, "_test.go") {
		recordViolation(rep, "HISS-07", rel, lineNum, "", "Legacy unchecked error assignment")
	}
	if trimmed == "for {" || strings.HasPrefix(trimmed, "for { ") {
		recordViolation(rep, "HISS-02", rel, lineNum, "", "Legacy unbounded for {} loop without explicit exit condition")
	}
	if strings.Contains(line, "panic(") && !strings.Contains(rel, "_test.go") {
		recordViolation(rep, "HISS-07", rel, lineNum, "", "Legacy panic() invocation in production code path")
	}
	if strings.HasPrefix(trimmed, "goto ") {
		recordViolation(rep, "HISS-01", rel, lineNum, "", "Legacy non-DAG control flow jump (goto)")
	}
}

func extractGoFuncName(trimmed string) string {
	name := strings.TrimPrefix(trimmed, "func ")
	if strings.HasPrefix(name, "(") {
		if closeParen := strings.Index(name, ")"); closeParen > 0 {
			name = strings.TrimSpace(name[closeParen+1:])
		}
	}
	if idx := strings.Index(name, "("); idx > 0 {
		name = strings.TrimSpace(name[:idx])
	}
	return name
}

func scanRustLines(lines []string, rel string, rep *ScanReport, opts ScanOptions) {
	inFunc := false
	funcStart := 0
	funcName := ""
	braceLevel := 0

	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		scanRustLineInvariants(line, trimmed, rel, idx+1, rep)

		if !inFunc {
			if (strings.HasPrefix(trimmed, "fn ") || strings.HasPrefix(trimmed, "pub fn ") ||
				strings.HasPrefix(trimmed, "pub(crate) fn ") || strings.HasPrefix(trimmed, "async fn ") ||
				strings.HasPrefix(trimmed, "pub async fn ")) && strings.Contains(line, "{") {
				inFunc = true
				funcStart = idx + 1
				braceLevel = strings.Count(line, "{") - strings.Count(line, "}")
				funcName = extractRustFuncName(trimmed)
			}
		} else {
			braceLevel += strings.Count(line, "{") - strings.Count(line, "}")
			if braceLevel <= 0 {
				funcLen := (idx + 1) - funcStart + 1
				if funcLen > opts.MaxFuncLOC {
					recordViolation(rep, "HISS-04", rel, funcStart, funcName,
						fmt.Sprintf("Function '%s' (%d LOC) exceeds HISS-04 / NASA Rule 4 limit of %d LOC", funcName, funcLen, opts.MaxFuncLOC))
				}
				inFunc = false
			}
		}
	}
}

func scanRustLineInvariants(line, trimmed, rel string, lineNum int, rep *ScanReport) {
	if trimmed == "loop {" || strings.HasPrefix(trimmed, "loop { ") {
		recordViolation(rep, "HISS-02", rel, lineNum, "", "Legacy unbounded loop {} in Rust without explicit scalar bound")
	}
	if strings.Contains(line, ".unwrap()") && !strings.Contains(rel, "test") {
		recordViolation(rep, "HISS-07", rel, lineNum, "", "Legacy .unwrap() invocation bypassing error propagation")
	}
	if strings.Contains(line, ".expect(") && !strings.Contains(rel, "test") {
		recordViolation(rep, "HISS-07", rel, lineNum, "", "Legacy .expect() invocation in production Rust code")
	}
	if strings.Contains(line, "unsafe {") || strings.HasPrefix(trimmed, "unsafe fn") {
		recordViolation(rep, "HISS-09", rel, lineNum, "", "Unaudited unsafe block in Rust code")
	}
}

func extractRustFuncName(trimmed string) string {
	name := trimmed
	for _, p := range []string{"pub(crate) ", "pub ", "async "} {
		name = strings.TrimPrefix(name, p)
	}
	name = strings.TrimPrefix(name, "fn ")
	if idx := strings.Index(name, "("); idx > 0 {
		name = strings.TrimSpace(name[:idx])
	}
	return name
}
