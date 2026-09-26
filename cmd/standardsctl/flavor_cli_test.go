package main

import (
	"strings"
	"testing"
)

// Positive: flavor apply names the templates it leaves to their producer, so an operator
// is told what is still missing instead of receiving a comment placeholder in its place.
func TestFlavorApply_Positive_ReportsTemplatesLeftToTheirProducer(t *testing.T) {
	out, err := captureStdout(t, func() error {
		return dispatchCommand("flavor", []string{"apply", t.TempDir(), "--flavor=go-library"})
	})
	if err != nil {
		t.Fatalf("flavor apply: %v\n%s", err, out)
	}
	mustContain(t, out, "Left to Producer  (2): .standards.yaml (praetorctl adopt), .standards.lock (praetorctl adopt)")
}

// Negative: a flavor whose every template has a scaffolded body leaves nothing to a producer
// and prints no such line.
func TestFlavorApply_Negative_NoProducerLineWhenNothingIsDeferred(t *testing.T) {
	out, err := captureStdout(t, func() error {
		return dispatchCommand("flavor", []string{"apply", t.TempDir(), "--flavor=go-service"})
	})
	if err != nil {
		t.Fatalf("flavor apply: %v\n%s", err, out)
	}
	if strings.Contains(out, "Left to Producer") {
		t.Fatalf("go-service defers nothing, got:\n%s", out)
	}
}

// Boundary: inspect names the producer of each producer-owned template and nothing for a
// scaffolded one.
func TestFlavorInspect_Boundary_NamesTheProducer(t *testing.T) {
	out, err := captureStdout(t, func() error {
		return dispatchCommand("flavor", []string{"inspect", "agentic-autonomous"})
	})
	if err != nil {
		t.Fatalf("flavor inspect: %v\n%s", err, out)
	}
	mustContain(t, out, "written by: praetorctl adopt", "written by: praetorctl compile-context")
	scaffolded, err := captureStdout(t, func() error {
		return dispatchCommand("flavor", []string{"inspect", "go-service"})
	})
	if err != nil || strings.Contains(scaffolded, "written by:") {
		t.Fatalf("go-service templates are scaffolded, not produced elsewhere: %v\n%s", err, scaffolded)
	}
}
