package harvester

import (
	"context"
	"strings"
	"testing"
)

func TestBundleReportsUnsupportedDevelopmentScope(t *testing.T) {
	bundle := t.TempDir()
	report, err := BundleWorkstation(context.Background(), BundleOptions{HomeDir: t.TempDir(), OutputDir: bundle, DevDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Skipped) != 1 || !strings.Contains(report.Skipped[0], "development-repository capture is not implemented") {
		t.Fatalf("unsupported scope accepted silently: %+v", report.Skipped)
	}
	for _, dryRun := range []bool{true, false} {
		ingested, err := IngestBundle(context.Background(), bundle, t.TempDir(), dryRun)
		if err != nil {
			t.Fatal(err)
		}
		if len(ingested.Warnings) != 1 || ingested.Warnings[0] != report.Skipped[0] {
			t.Fatalf("dryRun=%t lost scope warning: %+v", dryRun, ingested.Warnings)
		}
	}
}
