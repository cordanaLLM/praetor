package util

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileAndDirExists(t *testing.T) {
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "sample.txt")
	dirPath := filepath.Join(tmp, "subdir")

	if err := os.WriteFile(filePath, []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	if err := os.Mkdir(dirPath, 0755); err != nil {
		t.Fatalf("failed to mkdir: %v", err)
	}

	// Positive
	if !FileExists(filePath) {
		t.Errorf("expected FileExists(%s) == true", filePath)
	}
	if !DirExists(dirPath) {
		t.Errorf("expected DirExists(%s) == true", dirPath)
	}

	// Negative
	if FileExists(dirPath) {
		t.Errorf("expected FileExists(dirPath) == false")
	}
	if DirExists(filePath) {
		t.Errorf("expected DirExists(filePath) == false")
	}

	// Boundary (non-existent)
	nonExistent := filepath.Join(tmp, "does_not_exist")
	if FileExists(nonExistent) {
		t.Errorf("expected FileExists(nonExistent) == false")
	}
	if DirExists(nonExistent) {
		t.Errorf("expected DirExists(nonExistent) == false")
	}

	// PathExists
	if !PathExists(filePath) {
		t.Errorf("expected PathExists(filePath) == true")
	}
	if !PathExists(dirPath) {
		t.Errorf("expected PathExists(dirPath) == true")
	}
	if PathExists(nonExistent) {
		t.Errorf("expected PathExists(nonExistent) == false")
	}
}

func TestCleanGitURLAndExtract(t *testing.T) {
	tests := []struct {
		url       string
		wantClean string
		wantOwner string
		wantRepo  string
	}{
		{
			url:       "git@github.com:cordanaLLM/praetor.git",
			wantClean: "git@github.com:cordanaLLM/praetor",
			wantOwner: "cordanaLLM",
			wantRepo:  "praetor",
		},
		{
			url:       "https://github.com/golusoris/sveltesentio.git/",
			wantClean: "https://github.com/golusoris/sveltesentio",
			wantOwner: "golusoris",
			wantRepo:  "sveltesentio",
		},
		{
			url:       "vmafx",
			wantClean: "vmafx",
			wantOwner: "",
			wantRepo:  "vmafx",
		},
		{
			url:       "",
			wantClean: "",
			wantOwner: "",
			wantRepo:  "",
		},
	}

	for _, tt := range tests {
		gotClean := CleanGitURL(tt.url)
		if gotClean != tt.wantClean {
			t.Errorf("CleanGitURL(%q) = %q, want %q", tt.url, gotClean, tt.wantClean)
		}
		gotOwner, gotRepo := ExtractOwnerAndRepo(tt.url)
		if gotOwner != tt.wantOwner || gotRepo != tt.wantRepo {
			t.Errorf("ExtractOwnerAndRepo(%q) = (%q, %q), want (%q, %q)", tt.url, gotOwner, gotRepo, tt.wantOwner, tt.wantRepo)
		}
	}
}

func TestRunCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	out, err := RunCommand(ctx, ".", "echo", "hello-world")
	if err != nil {
		t.Fatalf("RunCommand failed: %v", err)
	}
	if out != "hello-world" {
		t.Errorf("got %q, want %q", out, "hello-world")
	}
}
