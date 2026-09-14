package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDogfoodSuiteCLIPlanAndFlagErrors(t *testing.T) {
	root := t.TempDir()
	config := filepath.Join(root, "suite.json")
	data := `{"version":1,"public_repositories":["https://github.com/spf13/cobra#adbc8813901bba65827259daa8e22ff94ec1f30e"],"transcripts":[]}`
	if err := os.WriteFile(config, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runDogfood([]string{"suite", "--config", config, "--artifacts", filepath.Join(root, "plan")}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"suite"}, {"suite", "--config", config}, {"suite", "--config", config, "--artifacts", filepath.Join(root, "bad"), "extra"}, {"suite", "--config", config, "--artifacts", filepath.Join(root, "bad"), "--stage=promote"}} {
		if err := runDogfood(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
