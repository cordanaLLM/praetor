package state

import (
	"os"
	"path/filepath"
	"testing"
)

// One repository reached under two spellings of its directory must bind to one state.
//
// The binding hashes the repository path itself, and on Windows the two sides of a hook
// disagree about how that path is spelled. The runner's TEMP is the 8.3 short name
// C:\Users\RUNNER~1\AppData\Local\Temp, which Go reports verbatim because GetCurrentDirectory
// never expands it, while the hook first moves to the top level git reports and git resolves
// its working directory through GetFinalPathNameByHandleW, which always answers with the long
// form C:\Users\runneradmin\... Two spellings, two bindings, and every commit and push the
// harness self-tests drive was refused as "state synchronization stale" (#135). macOS is
// unaffected because POSIX getcwd(2) is already free of symbolic links.
//
// A symlinked alias is the portable way to put one directory under two spellings, so this
// case exercises the same condition on every platform rather than only where it was found.
func TestStateSyncBindsOneStateAcrossAliasedRootSpellings(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(real, "repo"), 0o700); err != nil {
		t.Fatal(err)
	}
	// The alias is an ancestor rather than the root itself: the state ledger deliberately
	// refuses a symlinked confinement root, and the Windows spelling this stands in for is
	// not a symlink either -- it is a short name for a perfectly ordinary directory.
	if err := os.Symlink(real, filepath.Join(base, "link")); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	root := syncFixtureAt(t, filepath.Join(real, "repo"))
	alias := filepath.Join(base, "link", "repo")
	if err := VerifyStateSync(t.Context(), alias); err != nil {
		t.Fatalf("an aliased spelling rejected a state synced through the real path: %v", err)
	}
	if _, err := SyncState(t.Context(), alias, "aliased spelling"); err != nil {
		t.Fatal(err)
	}
	if err := VerifyStateSync(t.Context(), root); err != nil {
		t.Fatalf("the real path rejected a state synced through an aliased spelling: %v", err)
	}
	// Negative: the binding still refuses a genuinely different repository. Canonicalising
	// the spelling must not turn the path into an input the verification ignores.
	other := syncFixture(t)
	content, err := os.ReadFile(filepath.Join(root, WorkingDirName, "STATE.md"))
	if err != nil {
		t.Fatal(err)
	}
	writeIntegrityFile(t, filepath.Join(other, WorkingDirName, "STATE.md"), string(content))
	if err := VerifyStateSync(t.Context(), other); err == nil {
		t.Fatal("another repository accepted a state bound to this one")
	}
}
