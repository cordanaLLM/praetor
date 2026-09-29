package flavor_test

import (
	"testing"
)

// goOnlyLefthook is the pre-commit shape adoption generated for every repository before #568:
// gofmt and go vet on Go files, then the governance jobs.
const goOnlyLefthook = "pre-commit:\n  commands:\n" +
	"    gofmt:\n      glob: \"*.go\"\n      run: gofmt -w {staged_files}\n" +
	"    govet:\n      glob: \"*.go\"\n      run: go vet ./...\n" +
	"    hiss-audit:\n      run: praetorctl audit\n"

// rustLefthookValid reports what rust-systems' lefthook.yml setting decides for body.
func rustLefthookValid(t *testing.T, body string) bool {
	t.Helper()
	return settingsFor(t, "rust-systems", "lefthook.yml")[0].Validator([]byte(body))
}

// Positive: a lefthook.yml whose pre-commit hook runs cargo fmt and cargo clippy satisfies the
// setting rust-systems describes as clippy and rustfmt enforcement, in the commands-map form
// adoption writes, in lefthook's jobs-list form, and with rustfmt and cargo-clippy spelled
// directly.
func TestRustLefthookSetting_Positive_CargoJobsSatisfy(t *testing.T) {
	for _, body := range []string{
		"pre-commit:\n  commands:\n    rustfmt:\n      glob: \"*.rs\"\n      run: cargo fmt --all --check\n" +
			"    clippy:\n      glob: \"*.rs\"\n      run: cargo clippy --workspace --all-targets -- -D warnings\n",
		"pre-commit:\n  jobs:\n    - name: fmt\n      run: cargo fmt --all --check\n    - run: cargo clippy -- -D warnings\n",
		"pre-commit:\n  commands:\n    fmt:\n      run: rustfmt --check src/main.rs\n    lint:\n      run: cargo-clippy -- -D warnings\n",
	} {
		if !rustLefthookValid(t, body) {
			t.Errorf("a pre-commit hook running cargo fmt and cargo clippy was rejected:\n%s", body)
		}
	}
}

// Negative (#568): the Go-only configuration no longer scores as clippy and rustfmt enforcement,
// and neither does one that runs only one of the two jobs or runs them before a push instead of
// a commit.
func TestRustLefthookSetting_Negative_GoOnlyAndPartialRejected(t *testing.T) {
	for _, body := range []string{
		goOnlyLefthook,
		"pre-commit:\n  commands:\n    rustfmt:\n      run: cargo fmt --all --check\n",
		"pre-commit:\n  commands:\n    clippy:\n      run: cargo clippy\n",
		"pre-push:\n  commands:\n    rustfmt:\n      run: cargo fmt --all --check\n    clippy:\n      run: cargo clippy\n",
	} {
		if rustLefthookValid(t, body) {
			t.Errorf("accepted a lefthook.yml without both pre-commit jobs:\n%s", body)
		}
	}
	if !settingsFor(t, "go-library", "lefthook.yml")[0].Validator([]byte(goOnlyLefthook)) {
		t.Error("go-library claims no Rust job and must keep accepting the Go-only configuration")
	}
}

// Boundary: a job name is not its command, a script entry carries no run line, and a group's
// own jobs are not read, so none of them counts; an empty mapping or a document that does not
// parse is rejected before any job is read.
func TestRustLefthookSetting_Boundary_NamesScriptsAndGroupsDoNotCount(t *testing.T) {
	for _, body := range []string{
		"pre-commit:\n  commands:\n    cargo fmt:\n      run: 'true'\n    cargo clippy:\n      run: 'true'\n",
		"pre-commit:\n  scripts:\n    cargo fmt:\n      runner: bash\n    cargo clippy:\n      runner: bash\n",
		"pre-commit:\n  jobs:\n    - name: rust\n      group:\n        jobs:\n          - run: cargo fmt --all --check\n          - run: cargo clippy\n",
		"{}\n",
		"pre-commit: [unterminated\n",
	} {
		if rustLefthookValid(t, body) {
			t.Errorf("accepted a lefthook.yml whose pre-commit run lines hold neither job:\n%s", body)
		}
	}
}
