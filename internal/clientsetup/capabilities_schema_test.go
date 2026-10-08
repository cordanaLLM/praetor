package clientsetup

import (
	"context"
	"testing"

	"github.com/cordanaLLM/praetor/internal/clientschema"
)

// TestCapabilitiesReportThePinnedSchemaPerClient reads the expected versions from the vendored
// manifest, not from literals, and checks the clients without a schema say so with a reason.
func TestCapabilitiesReportThePinnedSchemaPerClient(t *testing.T) {
	manifest, err := clientschema.LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	report, err := Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range report.Clients {
		checkSchemaPin(t, manifest, item)
	}
}

func checkSchemaPin(t *testing.T, manifest *clientschema.Manifest, item Capability) {
	t.Helper()
	key, hasSchema := schemaClients[item.Client]
	if !hasSchema {
		if item.Schema.State != "none" || item.Schema.Reason == "" || item.Schema.Version != "" || len(item.Schema.Sources) != 0 {
			t.Errorf("%s has no schema but reports %+v", item.Client, item.Schema)
		}
		return
	}
	want := manifest.PinnedVersion(key)
	if want == "" || item.Schema.State != "pinned" || item.Schema.Version != want || len(item.Schema.Sources) != len(manifest.ForClient(key)) {
		t.Errorf("%s reports %+v, want version %q from the manifest", item.Client, item.Schema, want)
	}
}

// TestPinnedClientsAreTheOnesTheManifestCovers fixes which adapters carry a schema: the four
// clients the issue names, and no other.
func TestPinnedClientsAreTheOnesTheManifestCovers(t *testing.T) {
	report, err := Capabilities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pinned := map[Client]bool{}
	for _, item := range report.Clients {
		if item.Schema.State == "pinned" {
			pinned[item.Client] = true
		}
	}
	want := map[Client]bool{Codex: true, Claude: true, Gemini: true, OpenCodeV1: true}
	if len(pinned) != len(want) {
		t.Fatalf("pinned clients %v, want %v", pinned, want)
	}
	for client := range want {
		if !pinned[client] {
			t.Errorf("%s is not pinned", client)
		}
	}
}

// TestSchemaPinWithoutAManifestSourceFailsClosed covers the boundary: an adapter mapped to a
// manifest client that has no source reports "none" and never states a version.
func TestSchemaPinWithoutAManifestSourceFailsClosed(t *testing.T) {
	manifest, err := clientschema.LoadManifest()
	if err != nil {
		t.Fatal(err)
	}
	restore := schemaClients
	schemaClients = map[Client]string{Kilo: "kilo-has-no-source"}
	defer func() { schemaClients = restore }()
	if pin := schemaPin(manifest, Kilo); pin.State != "none" || pin.Version != "" {
		t.Fatalf("pin = %+v", pin)
	}
}
