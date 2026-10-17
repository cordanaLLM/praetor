package compiler

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const diagnosticSkillFixtureDir = "testdata/diagnostic-skills"

type cargoSpan struct {
	FileName    string `json:"file_name"`
	LineStart   int    `json:"line_start"`
	ColumnStart int    `json:"column_start"`
	IsPrimary   bool   `json:"is_primary"`
}

type cargoMessage struct {
	Code *struct {
		Code string `json:"code"`
	} `json:"code"`
	Level    string      `json:"level"`
	Spans    []cargoSpan `json:"spans"`
	Rendered string      `json:"rendered"`
}

type cargoLine struct {
	Reason  string       `json:"reason"`
	Message cargoMessage `json:"message"`
}

type cargoRepairDiagnostic struct {
	Code, Level, File, Rendered string
	Line, Column                int
}

func readCargoRepairDiagnostics(path string) ([]cargoRepairDiagnostic, int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, fmt.Errorf("open Cargo fixture: %w", err)
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var diagnostics []cargoRepairDiagnostic
	compilerMessages := 0
	for line := 0; line < 128 && scanner.Scan(); line++ {
		var message cargoLine
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			return nil, 0, fmt.Errorf("decode Cargo fixture line %d: %w", line+1, err)
		}
		if message.Reason != "compiler-message" {
			continue
		}
		compilerMessages++
		diagnostic, found := primaryCargoDiagnostic(message.Message)
		if found {
			diagnostics = append(diagnostics, diagnostic)
		}
	}
	if scanner.Scan() {
		return nil, 0, fmt.Errorf("Cargo fixture exceeds 128 lines")
	}
	if err := scanner.Err(); err != nil {
		return nil, 0, fmt.Errorf("read Cargo fixture: %w", err)
	}
	return diagnostics, compilerMessages, nil
}

func primaryCargoDiagnostic(message cargoMessage) (cargoRepairDiagnostic, bool) {
	for i, span := range message.Spans {
		if i >= 64 {
			break
		}
		if span.IsPrimary {
			code := ""
			if message.Code != nil {
				code = message.Code.Code
			}
			return cargoRepairDiagnostic{Code: code, Level: message.Level, File: span.FileName,
				Line: span.LineStart, Column: span.ColumnStart, Rendered: message.Rendered}, true
		}
	}
	return cargoRepairDiagnostic{}, false
}

func TestCompilerJSONSkillFixtures(t *testing.T) {
	dir := filepath.Join(".", diagnosticSkillFixtureDir)
	got, messages, err := readCargoRepairDiagnostics(filepath.Join(dir, "cargo-error.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if messages != 1 || len(got) != 1 {
		t.Fatalf("error fixture messages=%d diagnostics=%d", messages, len(got))
	}
	if got[0].Code != "E0308" || got[0].File != "src/main.rs" || got[0].Line != 1 || got[0].Column != 30 {
		t.Fatalf("primary Cargo span not retained: %+v", got[0])
	}
	if !strings.Contains(got[0].Rendered, "mismatched types") {
		t.Fatalf("rendered diagnostic missing: %+v", got[0])
	}
	clean, messages, err := readCargoRepairDiagnostics(filepath.Join(dir, "cargo-clean.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if messages != 0 || len(clean) != 0 {
		t.Fatalf("clean fixture messages=%d diagnostics=%d", messages, len(clean))
	}
}
