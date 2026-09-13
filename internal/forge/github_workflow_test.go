package forge

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"gopkg.in/yaml.v3"
)

// Protection must select actual check-run display names, not YAML job IDs. Read
// the workflow sources so a renamed or removed gate cannot leave a phantom rule.
func TestDefaultRequiredStatusChecksMatchWorkflowJobs(t *testing.T) {
	jobs := []struct{ file, id string }{
		{"ci.yml", "verify"},
		{"compliance.yml", "compliance"},
		{"security.yml", "security"},
	}
	var names []string
	for _, job := range jobs {
		path := filepath.Join("..", "..", ".github", "workflows", job.file)
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var workflow struct {
			Jobs map[string]struct{ Name string } `yaml:"jobs"`
		}
		if err := yaml.Unmarshal(data, &workflow); err != nil {
			t.Fatal(err)
		}
		name := workflow.Jobs[job.id].Name
		if name == "" {
			t.Fatalf("required job %s missing or unnamed in %s", job.id, path)
		}
		names = append(names, name)
	}
	if got := DefaultRequiredStatusChecks(); !slices.Equal(got, names) {
		t.Fatalf("required check contexts %q do not match workflow jobs %q", got, names)
	}
}
