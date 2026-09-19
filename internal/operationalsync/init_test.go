package operationalsync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newInitFixture clones the public tip into a fork checkout that still carries the public
// identity, with the remotes an operational fork has: origin is the owner, upstream the source.
func newInitFixture(t *testing.T) (syncFixture, Options) {
	t.Helper()
	f := newSyncFixture(t)
	fork := filepath.Join(filepath.Dir(f.opts.SourcePath), "fork")
	testGit(t, f.git, "", "clone", "--template=", "--no-hardlinks", f.opts.SourcePath, fork)
	testGit(t, f.git, fork, "remote", "set-url", "origin", "https://github.com/private/praetor.git")
	testGit(t, f.git, fork, "remote", "add", "upstream", "https://github.com/public/praetor.git")
	return f, Options{OwnerPath: fork, Owner: "private"}
}

func TestRunInitWritesExactlyTheOverlayThatPlanAccepts(t *testing.T) {
	f, opts := newInitFixture(t)
	r, err := Run(context.Background(), "init", opts)
	if err != nil {
		t.Fatal(err)
	}
	head := testGit(t, f.git, opts.OwnerPath, "rev-parse", "HEAD")
	if r.Status != "initialized" || r.Options.OwnerSHA != head || head != f.opts.SourceSHA || r.Owner != "private/praetor" || r.Source != "public/praetor" {
		t.Fatalf("report: %+v", r)
	}
	if len(r.ChangedPaths) != 4 || r.OwnerOnlyPaths == nil || r.MergePending || r.Candidate != "" {
		t.Fatalf("report paths: %+v", r)
	}
	changed := strings.Split(testGit(t, f.git, opts.OwnerPath, "diff", "--name-only"), "\n")
	if len(changed) != 4 {
		t.Fatalf("init must change exactly four files: %v", changed)
	}
	if staged := testGit(t, f.git, opts.OwnerPath, "diff", "--cached", "--name-only"); staged != "" {
		t.Fatalf("init staged %s", staged)
	}
	if untracked := testGit(t, f.git, opts.OwnerPath, "ls-files", "--others"); untracked != "" {
		t.Fatalf("init created %s", untracked)
	}
	raw, err := os.ReadFile(filepath.Join(opts.OwnerPath, ownerPaths[0]))
	if err != nil || !strings.Contains(string(raw), "owner: private") || !strings.Contains(string(raw), "keep-this-unknown-field") {
		t.Fatalf("manifest overlay: %s %v", raw, err)
	}
	// The operator commits; plan then accepts the engine-produced commit with base = source = tip.
	ownerSHA := fixtureCommit(t, f.git, opts.OwnerPath)
	plan, err := Run(context.Background(), "plan", Options{OwnerPath: opts.OwnerPath, SourcePath: f.opts.SourcePath,
		OwnerSHA: ownerSHA, BaseSHA: f.opts.SourceSHA, SourceSHA: f.opts.SourceSHA})
	if err != nil || plan.Status != "planned" {
		t.Fatalf("plan refused the init commit: %+v %v", plan, err)
	}
}

func TestRunInitRefusesUnsafeCheckouts(t *testing.T) {
	cases := []struct {
		name   string
		change func(*testing.T, *syncFixture, *Options)
		reason string
	}{
		{"already overlaid", func(_ *testing.T, f *syncFixture, o *Options) { o.OwnerPath = f.opts.OwnerPath }, "public source identity"},
		{"same owner as source", func(_ *testing.T, _ *syncFixture, o *Options) { o.Owner = "public" }, "public source identity"},
		{"untracked file", func(t *testing.T, _ *syncFixture, o *Options) { testWrite(t, o.OwnerPath, "note.txt", "x") }, "uncommitted or untracked"},
		{"modified file", func(t *testing.T, _ *syncFixture, o *Options) { testWrite(t, o.OwnerPath, "engine.txt", "x") }, "uncommitted or untracked"},
		{"staged file", func(t *testing.T, f *syncFixture, o *Options) {
			testWrite(t, o.OwnerPath, "engine.txt", "x")
			testGit(t, f.git, o.OwnerPath, "add", "--", "engine.txt")
		}, "uncommitted or untracked"},
		{"origin is another owner", func(_ *testing.T, _ *syncFixture, o *Options) { o.Owner = "elsewhere" }, "disagree with reviewed manifest identities"},
		{"no upstream remote", func(t *testing.T, f *syncFixture, o *Options) {
			testGit(t, f.git, o.OwnerPath, "remote", "remove", "upstream")
		}, "missing upstream remote"},
		{"subdirectory", func(_ *testing.T, _ *syncFixture, o *Options) { o.OwnerPath = filepath.Join(o.OwnerPath, ".paperclip") }, "checkout roots"},
		{"local filter", func(t *testing.T, f *syncFixture, o *Options) {
			testGit(t, f.git, o.OwnerPath, "config", "filter.probe.clean", "cat")
		}, "filters are unsupported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, opts := newInitFixture(t)
			tc.change(t, &f, &opts)
			before := testGit(t, f.git, opts.OwnerPath, "status", "--porcelain")
			r, err := Run(context.Background(), "init", opts)
			if err == nil || r != nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("init on %s: %+v %v", tc.name, r, err)
			}
			if after := testGit(t, f.git, opts.OwnerPath, "status", "--porcelain"); after != before {
				t.Fatalf("refused init wrote to the checkout: %q -> %q", before, after)
			}
		})
	}
}

func TestRunInitRefusesASecondRun(t *testing.T) {
	f, opts := newInitFixture(t)
	if _, err := Run(context.Background(), "init", opts); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), "init", opts); err == nil || !strings.Contains(err.Error(), "uncommitted or untracked") {
		t.Fatalf("init over an uncommitted overlay: %v", err)
	}
	fixtureCommit(t, f.git, opts.OwnerPath)
	if _, err := Run(context.Background(), "init", opts); err == nil || !strings.Contains(err.Error(), "public source identity") {
		t.Fatalf("init over a committed overlay: %v", err)
	}
}

// The positive half of the checkout-root contract whose negative half is the "subdirectory"
// case above: one directory reached through a symlinked ancestor is still its own checkout
// root. git answers rev-parse --show-toplevel through its own real_path(), so it prints the
// resolved spelling while the caller still holds the aliased one. That shape is the portable
// stand-in for the two spellings that failed the matrix -- macOS reaching its TMPDIR through
// /var -> /private/var, and Windows handing out an 8.3 short name where git hands back the
// long one (#135).
//
// What this case is, measured rather than claimed: a pin on the contract, not a reproducer
// for the identity compare at sync.go:258. On Linux filepath.EvalSymlinks resolves an aliased
// ancestor to exactly git's answer, so the EvalSymlinks-as-string compare that stood here
// before accepts this fixture too, and reverting that line leaves this case green. It does
// refuse the plainer spelling compare that has been reintroduced at this line twice already
// (Clean(FromSlash(root)) != Clean(path)), which rejects the aliased spelling outright.
// Inode identity and EvalSymlinks-as-string differ only where the two answers differ as
// strings -- a Windows drive letter's case, and its short names -- which no Linux fixture can
// produce, so the Windows leg of the matrix is what replays that half.
func TestRunInitAcceptsACheckoutRootUnderAnAliasedAncestor(t *testing.T) {
	f, opts := newInitFixture(t)
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(filepath.Dir(opts.OwnerPath), alias); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
	aliased := filepath.Join(alias, filepath.Base(opts.OwnerPath))
	top := filepath.FromSlash(testGit(t, f.git, aliased, "rev-parse", "--show-toplevel"))
	if top == aliased {
		t.Skipf("this host resolves the caller's own spelling to git's: %q", top)
	}
	opts.OwnerPath = aliased
	r, err := Run(context.Background(), "init", opts)
	if err != nil || r.Status != "initialized" {
		t.Fatalf("aliased checkout root refused: %+v %v", r, err)
	}
	// The overlay is written through the aliased spelling and lands in the one directory both
	// spellings name, so the alias is a spelling of the checkout, never a second copy of it.
	raw, err := os.ReadFile(filepath.Join(top, ownerPaths[0]))
	if err != nil || !strings.Contains(string(raw), "owner: private") {
		t.Fatalf("overlay under the resolved spelling: %s %v", raw, err)
	}
}

func TestValidateInitOptionsBounds(t *testing.T) {
	dir := t.TempDir()
	longest := strings.Repeat("a", 100)
	for _, owner := range []string{"a", longest, "Org-1.name_x"} {
		if err := validateInitOptions(&Options{OwnerPath: dir, Owner: owner}); err != nil {
			t.Errorf("owner %q refused: %v", owner, err)
		}
	}
	for _, owner := range []string{"", longest + "a", "bad/owner", "-leading", "sp ace"} {
		if err := validateInitOptions(&Options{OwnerPath: dir, Owner: owner}); err == nil {
			t.Errorf("owner %q accepted", owner)
		}
	}
	sha := strings.Repeat("a", 40)
	for _, opts := range []Options{{Owner: "private"}, {OwnerPath: dir, Owner: "private", SourcePath: dir}, {OwnerPath: dir, Owner: "private", OwnerSHA: sha},
		{OwnerPath: dir, Owner: "private", BaseSHA: sha}, {OwnerPath: dir, Owner: "private", SourceSHA: sha}, {OwnerPath: dir, Owner: "private", Destination: dir}} {
		if err := validateInitOptions(&opts); err == nil {
			t.Errorf("init options accepted: %+v", opts)
		}
	}
	relative := Options{OwnerPath: ".", Owner: "private"}
	if err := validateInitOptions(&relative); err != nil || !filepath.IsAbs(relative.OwnerPath) {
		t.Fatalf("relative owner path: %+v %v", relative, err)
	}
}

func TestLaterStagesRefuseTheInitOwnerOption(t *testing.T) {
	f := newSyncFixture(t)
	f.opts.Owner = "private"
	if _, err := Run(context.Background(), "prepare", f.opts); err == nil || !strings.Contains(err.Error(), "only valid for init") {
		t.Fatalf("owner accepted outside init: %v", err)
	}
	if _, err := os.Stat(f.opts.Destination); !os.IsNotExist(err) {
		t.Fatal("refused options created candidate")
	}
}

func TestCheckInitStatus(t *testing.T) {
	exact := " M .devcontainer/devcontainer.json\x00 M .paperclip/harness.json\x00 M .paperclip/rules.md\x00 M .standards.yaml\x00"
	if err := checkInitStatus(exact); err != nil {
		t.Fatal(err)
	}
	for name, status := range map[string]string{
		"nothing written": "",
		"three of four":   " M .paperclip/harness.json\x00 M .paperclip/rules.md\x00 M .standards.yaml\x00",
		"untracked":       exact + "?? note.txt\x00",
		"engine file":     exact + " M engine.txt\x00",
		"staged overlay":  "M  .devcontainer/devcontainer.json\x00 M .paperclip/harness.json\x00 M .paperclip/rules.md\x00 M .standards.yaml\x00",
		"repeated path":   exact + " M .standards.yaml\x00",
		"short record":    exact + " M\x00",
	} {
		if err := checkInitStatus(status); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
