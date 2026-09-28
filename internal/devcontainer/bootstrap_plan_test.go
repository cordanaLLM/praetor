package devcontainer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Positive: PlanBundle writes nothing; Files lists the config first, each absent, with the bytes
// the write leaves; Publish writes the bundle, which then verifies.
func TestPlanBundle_Positive_PlanThenPublish(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".devcontainer", "devcontainer.json")
	bundle := preparedBootstrap(t)
	plan, err := PlanBundle(t.Context(), path, bundle, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("PlanBundle wrote the DevContainer directory (stat err=%v)", err)
	}
	files := plan.Files()
	if len(files) != len(bundle.Artifacts)+1 || files[0].Path != path {
		t.Fatalf("files = %d, first %q; want the config first and every companion", len(files), files[0].Path)
	}
	for _, file := range files {
		if file.Existed || file.Before != nil || len(file.After) == 0 || file.Placeholder {
			t.Fatalf("absent file planned as %+v", file.Path)
		}
	}
	if err := plan.Publish(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := Verify(t.Context(), path, mustBaseContainer(t)); err != nil {
		t.Fatalf("published bundle does not verify: %v", err)
	}
}

// Negative: a nil context fails both calls; an edited config is refused without force before
// anything is written; and Publish refuses a file that changed after the plan observed it.
func TestPlanBundle_Negative_RefusalsWriteNothing(t *testing.T) {
	var nilCtx context.Context
	if _, err := PlanBundle(nilCtx, "devcontainer.json", preparedBootstrap(t), false); err == nil {
		t.Fatal("PlanBundle accepted a nil context")
	}
	if err := (&BundlePlan{}).Publish(nilCtx); err == nil {
		t.Fatal("Publish accepted a nil context")
	}
	path, placeholder := writeUnavailablePlaceholder(t, []string{"framework"})
	edited := bytes.Replace(placeholder, []byte(`"remoteUser": "vscode"`), []byte(`"remoteUser": "root"`), 1)
	if err := os.WriteFile(path, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanBundle(t.Context(), path, preparedBootstrap(t), false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("edited config admitted without force: %v", err)
	}
	plan, err := PlanBundle(t.Context(), path, preparedBootstrap(t), true)
	if err != nil {
		t.Fatal(err)
	}
	const later = "{}\n"
	if err := os.WriteFile(path, []byte(later), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := plan.Publish(t.Context()); err == nil {
		t.Fatal("Publish overwrote a config edited after the plan")
	}
	assertFileBytes(t, path, []byte(later))
}

// Boundary: Praetor's own unedited placeholder is planned as a placeholder refresh without
// force, with its bytes as Before; an edited config admitted by force is not; and a rerun
// over the written bundle plans every file with Before equal to After.
func TestPlanBundle_Boundary_PlaceholderAndUnchangedFiles(t *testing.T) {
	path, placeholder := writeUnavailablePlaceholder(t, []string{"framework"})
	plan, err := PlanBundle(t.Context(), path, preparedBootstrap(t), false)
	if err != nil {
		t.Fatal(err)
	}
	config := plan.Files()[0]
	if !config.Existed || !config.Placeholder || !bytes.Equal(config.Before, placeholder) {
		t.Fatalf("own placeholder planned as existed=%v placeholder=%v", config.Existed, config.Placeholder)
	}
	edited := bytes.Replace(placeholder, []byte(`"remoteUser": "vscode"`), []byte(`"remoteUser": "root"`), 1)
	if err := os.WriteFile(path, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	forced, err := PlanBundle(t.Context(), path, preparedBootstrap(t), true)
	if err != nil || forced.Files()[0].Placeholder {
		t.Fatalf("edited config planned as a placeholder refresh (err %v)", err)
	}
	if err := forced.Publish(t.Context()); err != nil {
		t.Fatal(err)
	}
	again, err := PlanBundle(t.Context(), path, preparedBootstrap(t), false)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range again.Files() {
		if !file.Existed || !bytes.Equal(file.Before, file.After) {
			t.Fatalf("%s planned as changed on an unchanged rerun", file.Path)
		}
	}
}
