package flavor

import (
	"strings"

	"github.com/cordanaLLM/praetor/internal/lefthookconfig"
	"gopkg.in/yaml.v3"
)

// lefthookPreCommit is the hook whose jobs a flavor's lefthook.yml claim is about.
const lefthookPreCommit = "pre-commit"

// rustLefthookJobs are the pre-commit jobs rust-systems describes its lefthook.yml setting as
// enforcing, each accepted under any of its spellings: rustfmt through cargo fmt or directly,
// and clippy through cargo clippy or the cargo-clippy binary. The lefthook.yml adoption
// generates for a Cargo repository runs cargo fmt and cargo clippy (internal/adopt/hooks.go).
var rustLefthookJobs = [][]string{
	{"cargo fmt", "rustfmt"},
	{"cargo clippy", "cargo-clippy"},
}

// validRustLefthook reports whether content is a lefthook configuration whose pre-commit hook
// runs every job rust-systems claims (rustLefthookJobs), read from the commands map or the jobs
// list (lefthookconfig.Jobs). Before, the setting passed on any YAML mapping, so a lefthook.yml
// running only gofmt and go vet scored as clippy and rustfmt enforcement (#568).
func validRustLefthook(content []byte) bool {
	return lefthookPreCommitRuns(content, rustLefthookJobs)
}

// lefthookPreCommitRuns reports whether content parses as a non-empty lefthook configuration
// whose pre-commit run lines hold, for every entry of jobs, one of its spellings.
func lefthookPreCommitRuns(content []byte, jobs [][]string) bool {
	var parsed map[string]any
	if err := yaml.Unmarshal(content, &parsed); err != nil || len(parsed) == 0 {
		return false
	}
	runs := lefthookconfig.HookRuns(parsed, lefthookPreCommit)
	for _, spellings := range jobs {
		if !anyRunContains(runs, spellings) {
			return false
		}
	}
	return true
}

// anyRunContains reports whether one of runs holds one of spellings.
func anyRunContains(runs, spellings []string) bool {
	for _, run := range runs {
		for _, spelling := range spellings {
			if strings.Contains(run, spelling) {
				return true
			}
		}
	}
	return false
}
