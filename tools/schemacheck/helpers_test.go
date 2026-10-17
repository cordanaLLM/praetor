package schemacheck

import (
	"testing"

	"github.com/cordanaLLM/praetor/internal/clientschema"
)

func loadManifest(t *testing.T) *clientschema.Manifest {
	t.Helper()
	manifest, err := clientschema.LoadManifest()
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	return manifest
}
