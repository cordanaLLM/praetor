package compiler

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

const dotnetDiagnosticSkillFixtureDir = "testdata/diagnostic-skills"

type sarifResult struct {
	RuleID    string `json:"ruleId"`
	Level     string `json:"level"`
	Locations []struct {
		PhysicalLocation struct {
			ArtifactLocation struct {
				URI string `json:"uri"`
			} `json:"artifactLocation"`
			Region struct {
				StartLine int `json:"startLine"`
			} `json:"region"`
		} `json:"physicalLocation"`
	} `json:"locations"`
}

func readSARIFRepairResults(path string) ([]sarifResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read SARIF fixture: %w", err)
	}
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("SARIF fixture exceeds 1 MiB")
	}
	var report struct {
		Version string `json:"version"`
		Runs    []struct {
			Results []sarifResult `json:"results"`
		} `json:"runs"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("decode SARIF fixture: %w", err)
	}
	if report.Version != "2.1.0" {
		return nil, fmt.Errorf("unsupported SARIF version %q", report.Version)
	}
	var results []sarifResult
	for i, run := range report.Runs {
		if i >= 16 {
			return nil, fmt.Errorf("SARIF fixture exceeds 16 runs")
		}
		if len(run.Results) > 256 {
			return nil, fmt.Errorf("SARIF fixture run exceeds 256 results")
		}
		results = append(results, run.Results...)
	}
	return results, nil
}

func TestDotnetSARIFSkillFixtures(t *testing.T) {
	dir := filepath.Join(".", dotnetDiagnosticSkillFixtureDir)
	results, err := readSARIFRepairResults(filepath.Join(dir, "dotnet-error.sarif"))
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].RuleID != "CS0219" || results[0].Level != "warning" {
		t.Fatalf("planted SARIF result not retained: %+v", results)
	}
	location := results[0].Locations[0].PhysicalLocation
	if location.ArtifactLocation.URI != "src/Program.cs" || location.Region.StartLine != 1 {
		t.Fatalf("SARIF repair location not retained: %+v", location)
	}
	empty, err := readSARIFRepairResults(filepath.Join(dir, "dotnet-clean.sarif"))
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("clean SARIF fixture has %d results", len(empty))
	}
}
