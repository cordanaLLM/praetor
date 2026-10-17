package forge

import "testing"

func TestIsRefactorCommit_Positive(t *testing.T) {
	for _, message := range []string{
		"refactor: split function", "refactor(core): split", "refactor!: migrate", "refactor(core)!: migrate",
	} {
		if !IsRefactorCommit(message) {
			t.Errorf("IsRefactorCommit(%q) = false", message)
		}
	}
}

func TestIsRefactorCommit_Negative(t *testing.T) {
	for _, message := range []string{"feat: refactor code", "refactorish: split", "bad subject", "refactor(): split"} {
		if IsRefactorCommit(message) {
			t.Errorf("IsRefactorCommit(%q) = true", message)
		}
	}
}

func TestIsRefactorCommit_Boundary(t *testing.T) {
	if !IsRefactorCommit("refactor(core): split\n\nRefs #1423\n") || IsRefactorCommit("") {
		t.Fatal("message body and final newline must not change subject classification")
	}
}
