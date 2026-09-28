package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"gopkg.in/yaml.v3"
)

func TestInitCmd_FacetsDefault(t *testing.T) {
	tempDir := t.TempDir()
	manifest := filepath.Join(tempDir, ".standards.yaml")

	_, err := runInitCmd(t, "--output="+manifest)
	if err != nil {
		t.Fatalf("runInitCmd failed: %v", err)
	}

	b, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}

	var m struct {
		Facets []string `yaml:"facets"`
	}
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}

	want := config.DefaultFacets()
	if !reflect.DeepEqual(m.Facets, want) {
		t.Errorf("init default facets = %v, want %v", m.Facets, want)
	}
}
