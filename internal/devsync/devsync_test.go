package devsync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestInitCreatesCryptRemote(t *testing.T) {
	ctx, rclone, store := newFakeRclone(t, "")
	if err := Init(ctx, InitOptions{Base: "remote:folder", Rclone: rclone}); err != nil {
		t.Fatal(err)
	}
	var args []string
	if err := json.Unmarshal([]byte(readTestFile(t, filepath.Join(store, "config-create.json"))), &args); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(args[:5], []string{"config", "create", DefaultRemoteName, "crypt", "remote=remote:folder"}) ||
		!slices.Contains(args, "--obscure") || !slices.Contains(args, "--non-interactive") {
		t.Fatalf("config create argv = %v", args)
	}
	if args[5] == args[6] || !strings.HasPrefix(args[5], "password=") || !strings.HasPrefix(args[6], "password2=") ||
		len(args[5]) < len("password=")+40 {
		t.Fatalf("keys are not two distinct generated values: %q %q", args[5], args[6])
	}
}

func TestInitRefusesExistingRemote(t *testing.T) {
	ctx, rclone, _ := newFakeRclone(t, "")
	if err := Init(ctx, InitOptions{RemoteName: "mine", Base: "remote:folder", Rclone: rclone}); err != nil {
		t.Fatal(err)
	}
	if err := Init(ctx, InitOptions{RemoteName: "mine", Base: "remote:folder", Rclone: rclone}); !errors.Is(err, ErrRemoteExists) {
		t.Fatalf("second init = %v, want ErrRemoteExists", err)
	}
	for _, name := range []string{"has:colon", "-flag", "a/b", "semi;colon"} {
		if err := Init(ctx, InitOptions{RemoteName: name, Base: "remote:folder", Rclone: rclone}); err == nil {
			t.Errorf("remote name %q accepted", name)
		}
	}
	if err := Init(ctx, InitOptions{RemoteName: "other", Base: "-oops", Rclone: rclone}); err == nil {
		t.Error("base starting with '-' accepted")
	}
	failing, broken, _ := newFakeRclone(t, "config")
	if err := Init(failing, InitOptions{Base: "remote:folder", Rclone: broken}); err == nil || !strings.Contains(err.Error(), "fake failure") {
		t.Fatalf("rclone failure not surfaced: %v", err)
	}
}

func TestInitBaseRequirement_PNB(t *testing.T) {
	ctx, rclone, _ := newFakeRclone(t, "")

	// Negative: without --base -> error (unset = neutral refusal)
	err := Init(ctx, InitOptions{Rclone: rclone})
	if err == nil || err.Error() != "devsync init: --base required: name your rclone remote folder" {
		t.Fatalf("Init without Base = %v, want 'devsync init: --base required: name your rclone remote folder'", err)
	}

	// Boundary: whitespace base -> error
	err = Init(ctx, InitOptions{Base: "   ", Rclone: rclone})
	if err == nil || err.Error() != "devsync init: --base required: name your rclone remote folder" {
		t.Fatalf("Init with whitespace Base = %v, want 'devsync init: --base required: name your rclone remote folder'", err)
	}

	// Positive: with --base -> ok
	err = Init(ctx, InitOptions{Base: "remote:folder", Rclone: rclone})
	if err != nil {
		t.Fatalf("Init with valid Base = %v, want nil", err)
	}
}

func TestConfigCreateResultBoundary(t *testing.T) {
	if err := configCreateResult([]byte(`{"State":"","Error":""}`)); err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{`{"State":"*oauth","Error":""}`, `{"State":"","Error":"bad"}`, `not json`} {
		if err := configCreateResult([]byte(out)); err == nil {
			t.Errorf("result %s accepted", out)
		}
	}
}

func pushOptions(t *testing.T, rclone Rclone, dev string, out *bytes.Buffer) PushOptions {
	t.Helper()
	opts := PushOptions{
		DevDir: dev, HomeDir: t.TempDir(), Remote: DefaultRemote, Host: "ws1",
		StatePath: filepath.Join(t.TempDir(), "state.json"), Rclone: rclone,
	}
	if out != nil {
		opts.Out = out
	}
	return opts
}

func statuses(outcomes []Outcome) map[string]string {
	result := map[string]string{}
	for _, o := range outcomes {
		result[o.Archive] = o.Status
	}
	return result
}

func TestPushUploadsThenSkipsUnchanged(t *testing.T) {
	ctx, rclone, store := newFakeRclone(t, "")
	dev := makeDevTree(t)
	var out bytes.Buffer
	opts := pushOptions(t, rclone, dev, &out)
	outcomes, err := Push(ctx, opts)
	if err != nil {
		t.Fatal(err, out.String())
	}
	want := map[string]string{
		"ws1/dev/empty-org.tar.gz": StatusSkipped, "ws1/dev/empty-org/x.tar.gz": StatusUploaded,
		"ws1/dev/org.tar.gz": StatusUploaded, "ws1/dev/org/app.tar.gz": StatusUploaded,
		"ws1/dev/scratch.tar.gz": StatusUploaded, "ws1/dev/solo.tar.gz": StatusUploaded,
		"ws1/agent-state.tar.gz": StatusUploaded,
	}
	if got := statuses(outcomes); !mapsEqual(got, want) {
		t.Fatalf("first push = %v\n%s", got, out.String())
	}
	if _, err := os.Stat(filepath.Join(store, "data", "ws1", "dev", "org", "app.tar.gz.partial")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("partial upload left behind")
	}
	writeTestFile(t, filepath.Join(dev, "solo", "new.go"), "package main\n")
	outcomes, err = Push(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	got := statuses(outcomes)
	if got["ws1/dev/solo.tar.gz"] != StatusUploaded || got["ws1/dev/org/app.tar.gz"] != StatusSkipped {
		t.Fatalf("second push = %v", got)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if b[key] != value {
			return false
		}
	}
	return true
}

func TestPushDryRunChangesNothing(t *testing.T) {
	ctx, rclone, store := newFakeRclone(t, "")
	opts := pushOptions(t, rclone, makeDevTree(t), nil)
	opts.DryRun = true
	outcomes, err := Push(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := statuses(outcomes); got["ws1/dev/solo.tar.gz"] != StatusWouldUpload || got["ws1/agent-state.tar.gz"] != StatusWouldUpload {
		t.Fatalf("dry run = %v", got)
	}
	if _, err := os.Stat(filepath.Join(store, "data")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("dry run uploaded")
	}
	if _, err := os.Stat(opts.StatePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("dry run recorded state")
	}
}

func TestPushFailureKeepsLastArchive(t *testing.T) {
	ctx, rclone, store := newFakeRclone(t, "")
	dev := makeDevTree(t)
	opts := pushOptions(t, rclone, dev, nil)
	if _, err := Push(ctx, opts); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(store, "data", "ws1", "dev", "solo.tar.gz")
	before := readTestFile(t, archive)
	writeTestFile(t, filepath.Join(dev, "solo", "changed.go"), "package main\n")
	for _, verb := range []string{"rcat", "moveto"} {
		failing, broken := fakeRcloneAt(t, store, verb)
		failingOpts := opts
		failingOpts.Rclone = broken
		outcomes, err := Push(failing, failingOpts)
		if err == nil || statuses(outcomes)["ws1/dev/solo.tar.gz"] != StatusFailed {
			t.Fatalf("%s failure reported as %v, %v", verb, statuses(outcomes), err)
		}
		if readTestFile(t, archive) != before {
			t.Fatalf("%s failure replaced the last good archive", verb)
		}
		if _, err := os.Stat(archive + partialSuffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("partial upload not removed after %s failure", verb)
		}
	}
	outcomes, err := Push(ctx, opts)
	if err != nil || statuses(outcomes)["ws1/dev/solo.tar.gz"] != StatusUploaded {
		t.Fatalf("retry after failure = %v, %v", statuses(outcomes), err)
	}
}

func TestPushValidation(t *testing.T) {
	ctx, rclone, _ := newFakeRclone(t, "")
	dev := makeDevTree(t)
	for name, mutate := range map[string]func(*PushOptions){
		"empty host":     func(o *PushOptions) { o.Host = "" },
		"host with path": func(o *PushOptions) { o.Host = "a/b" },
		"dot host":       func(o *PushOptions) { o.Host = ".." },
		"missing dev":    func(o *PushOptions) { o.DevDir = filepath.Join(dev, "absent") },
		"dev is a file":  func(o *PushOptions) { o.DevDir = filepath.Join(dev, "loose.txt") },
		"no remote":      func(o *PushOptions) { o.Remote = "" },
		"no state":       func(o *PushOptions) { o.StatePath = "" },
	} {
		opts := pushOptions(t, rclone, dev, nil)
		mutate(&opts)
		if _, err := Push(ctx, opts); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// bigArchiveBytes discovers the measured source size Push reports for archive, through an
// uncapped dry run that neither uploads nor records anything.
func bigArchiveBytes(t *testing.T, ctx context.Context, rclone Rclone, dev, archive string) int64 {
	t.Helper()
	opts := pushOptions(t, rclone, dev, nil)
	opts.DryRun = true
	outcomes, err := Push(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	return outcomeFor(t, outcomes, archive).Bytes
}

// outcomeFor returns the outcome Push reported for archive.
func outcomeFor(t *testing.T, outcomes []Outcome, archive string) Outcome {
	t.Helper()
	for _, o := range outcomes {
		if o.Archive == archive {
			return o
		}
	}
	t.Fatalf("archive %s not found in %v", archive, outcomes)
	return Outcome{}
}

// isolateTempDir points the process temporary folder at a fresh empty folder and returns it,
// so a test can check that push leaves no private bundle copy behind.
func isolateTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, dir)
	}
	return dir
}

func TestPushCapAllowsArchiveUnderIt(t *testing.T) {
	// positive: a cap well above both fixtures uploads them both and reports zero skipped.
	ctx, rclone, _ := newFakeRclone(t, "")
	dev := makeSizeCapTree(t)
	var out bytes.Buffer
	opts := pushOptions(t, rclone, dev, &out)
	opts.MaxArchiveSize = 1 << 20
	outcomes, err := Push(ctx, opts)
	if err != nil {
		t.Fatal(err, out.String())
	}
	got := statuses(outcomes)
	if got["ws1/dev/small.tar.gz"] != StatusUploaded || got["ws1/dev/big.tar.gz"] != StatusUploaded ||
		got["ws1/agent-state.tar.gz"] != StatusUploaded {
		t.Fatalf("under-cap push = %v\n%s", got, out.String())
	}
	if strings.Contains(out.String(), StatusTooLarge) {
		t.Fatalf("unexpected %s line:\n%s", StatusTooLarge, out.String())
	}
	if !strings.Contains(out.String(), "skipped for size: 0 archive(s)") {
		t.Fatalf("size summary missing or nonzero:\n%s", out.String())
	}
}

func TestPushCapSkipsArchiveOverIt(t *testing.T) {
	// negative: a cap below big's measured size skips only big, prints a too-large line for it,
	// and the summary counts it; skipping for size is not a failure.
	ctx, rclone, _ := newFakeRclone(t, "")
	dev := makeSizeCapTree(t)
	bigBytes := bigArchiveBytes(t, ctx, rclone, dev, "ws1/dev/big.tar.gz")
	var out bytes.Buffer
	opts := pushOptions(t, rclone, dev, &out)
	opts.MaxArchiveSize = bigBytes - 1
	outcomes, err := Push(ctx, opts)
	if err != nil {
		t.Fatal(err, out.String())
	}
	got := statuses(outcomes)
	if got["ws1/dev/big.tar.gz"] != StatusTooLarge || got["ws1/dev/small.tar.gz"] != StatusUploaded {
		t.Fatalf("over-cap push = %v\n%s", got, out.String())
	}
	if !strings.Contains(out.String(), StatusTooLarge) || !strings.Contains(out.String(), "ws1/dev/big.tar.gz") ||
		!strings.Contains(out.String(), FormatBytes(bigBytes)) || !strings.Contains(out.String(), FormatBytes(bigBytes-1)) {
		t.Fatalf("too-large line lacks archive, measured size or cap:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "skipped for size: 1 archive(s)") || !strings.Contains(out.String(), FormatBytes(bigBytes)) {
		t.Fatalf("size summary missing or wrong count:\n%s", out.String())
	}
	// --dry-run reports the same skip without uploading anything.
	dryOut := &bytes.Buffer{}
	dryOpts := opts
	dryOpts.Out, dryOpts.DryRun = dryOut, true
	dryOutcomes, err := Push(ctx, dryOpts)
	if err != nil {
		t.Fatal(err, dryOut.String())
	}
	if statuses(dryOutcomes)["ws1/dev/big.tar.gz"] != StatusTooLarge {
		t.Fatalf("dry run over-cap = %v", statuses(dryOutcomes))
	}
}

func TestPushCapBoundaryAndDisabled(t *testing.T) {
	// boundary: exactly at the cap uploads rather than skipping; 0 and "none" both disable it.
	ctx, rclone, _ := newFakeRclone(t, "")
	dev := makeSizeCapTree(t)
	bigBytes := bigArchiveBytes(t, ctx, rclone, dev, "ws1/dev/big.tar.gz")
	exact := pushOptions(t, rclone, dev, nil)
	exact.MaxArchiveSize = bigBytes
	outcomes, err := Push(ctx, exact)
	if err != nil {
		t.Fatal(err)
	}
	if statuses(outcomes)["ws1/dev/big.tar.gz"] == StatusTooLarge {
		t.Fatalf("archive exactly at the cap was skipped: %v", statuses(outcomes))
	}
	disabledOpts := pushOptions(t, rclone, makeSizeCapTree(t), nil)
	disabledOpts.MaxArchiveSize = 0
	outcomes, err = Push(ctx, disabledOpts)
	if err != nil {
		t.Fatal(err)
	}
	if statuses(outcomes)["ws1/dev/big.tar.gz"] == StatusTooLarge {
		t.Fatalf("MaxArchiveSize=0 did not disable the cap: %v", statuses(outcomes))
	}
}

func TestPushMeasuresAgentState(t *testing.T) {
	// positive (#385): a dry run measures the agent state bundle instead of reporting 0 B, a
	// cap above it uploads it, and the private bundle copy never outlives either push.
	temp := isolateTempDir(t)
	ctx, rclone, store := newFakeRclone(t, "")
	opts := pushOptions(t, rclone, makeSizeCapTree(t), nil)
	opts.DryRun = true
	outcomes, err := Push(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	dry := outcomeFor(t, outcomes, "ws1/agent-state.tar.gz")
	if dry.Status != StatusWouldUpload || dry.Bytes <= 0 || dry.Note != "before compression" {
		t.Fatalf("dry run did not measure the agent state: %+v", dry)
	}
	opts.DryRun, opts.MaxArchiveSize = false, dry.Bytes*2
	if outcomes, err = Push(ctx, opts); err != nil {
		t.Fatal(err)
	}
	if got := outcomeFor(t, outcomes, "ws1/agent-state.tar.gz"); got.Status != StatusUploaded {
		t.Fatalf("agent state under the cap = %+v", got)
	}
	if _, err := os.Stat(filepath.Join(store, "data", "ws1", agentStateArchive)); err != nil {
		t.Fatalf("agent state not on the remote: %v", err)
	}
	if left, err := os.ReadDir(temp); err != nil || len(left) != 0 {
		t.Fatalf("private bundle copy left behind: %v, %v", left, err)
	}
}

func TestPushCapHoldsAgentState(t *testing.T) {
	// negative (#385): over the cap, the agent state bundle is skipped as too-large like every
	// project archive: nothing reaches the remote, the summary counts it, and a dry run reports
	// the same decision.
	ctx, rclone, store := newFakeRclone(t, "")
	var out bytes.Buffer
	opts := pushOptions(t, rclone, makeSizeCapTree(t), &out)
	opts.MaxArchiveSize = 1
	outcomes, err := Push(ctx, opts)
	if err != nil {
		t.Fatal(err, out.String())
	}
	agent := outcomeFor(t, outcomes, "ws1/agent-state.tar.gz")
	if agent.Status != StatusTooLarge || agent.Bytes <= 1 || agent.Note != FormatBytes(agent.Bytes)+" exceeds cap 1 B" {
		t.Fatalf("agent state over the cap = %+v\n%s", agent, out.String())
	}
	if _, err := os.Stat(filepath.Join(store, "data", "ws1", agentStateArchive)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("agent state uploaded over the cap: %v", err)
	}
	if !strings.Contains(out.String(), "too-large    ws1/agent-state.tar.gz") ||
		!strings.Contains(out.String(), "skipped for size: 3 archive(s)") {
		t.Fatalf("agent state skip not reported or not counted:\n%s", out.String())
	}
	opts.DryRun, opts.Out = true, nil
	if outcomes, err = Push(ctx, opts); err != nil {
		t.Fatal(err)
	}
	// The bundle manifest records its capture time, so its size may differ by a few bytes.
	if got := outcomeFor(t, outcomes, "ws1/agent-state.tar.gz"); got.Status != StatusTooLarge || got.Bytes <= 1 {
		t.Fatalf("dry run over the cap = %+v", got)
	}
}

func TestPushBundleCapBoundary(t *testing.T) {
	// boundary (#385): a bundle one byte over the cap is skipped, one exactly at it is measured
	// for a dry run and uploaded otherwise, and a zero cap never applies.
	ctx, rclone, store := newFakeRclone(t, "")
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "manifest.json"), strings.Repeat("x", 100))
	u := unit{rel: "agent-state", dir: dir, keepCaches: true}
	remote := filepath.Join(store, "data", "ws1", agentStateArchive)
	for _, tc := range []struct {
		limit        int64
		dryRun       bool
		status, note string
		bytes        int64
		onRemote     bool
	}{
		{limit: 99, status: StatusTooLarge, note: "100 B exceeds cap 99 B", bytes: 100},
		{limit: 100, dryRun: true, status: StatusWouldUpload, note: "before compression", bytes: 100},
		{limit: 100, status: StatusUploaded, onRemote: true},
		{limit: 0, status: StatusUploaded, onRemote: true},
	} {
		opts := pushOptions(t, rclone, t.TempDir(), nil)
		opts.MaxArchiveSize, opts.DryRun = tc.limit, tc.dryRun
		got := pushBundle(ctx, opts, Outcome{Archive: "ws1/" + agentStateArchive}, u)
		_, statErr := os.Stat(remote)
		if got.Status != tc.status || got.Err != nil || (tc.bytes != 0 && (got.Bytes != tc.bytes || got.Note != tc.note)) ||
			(statErr == nil) != tc.onRemote {
			t.Fatalf("cap %d, dry run %v = %+v, on remote %v", tc.limit, tc.dryRun, got, statErr == nil)
		}
	}
}

func TestPushBundleMeasureFailure(t *testing.T) {
	// negative: a bundle that cannot be measured fails the archive with the cause attached.
	ctx, rclone, _ := newFakeRclone(t, "")
	u := unit{rel: "agent-state", dir: filepath.Join(t.TempDir(), "absent"), keepCaches: true}
	got := pushBundle(ctx, pushOptions(t, rclone, t.TempDir(), nil), Outcome{Archive: "ws1/" + agentStateArchive}, u)
	if got.Status != StatusFailed || got.Err == nil || !strings.Contains(got.Err.Error(), "measure agent state") {
		t.Fatalf("unmeasurable bundle = %+v", got)
	}
}

func TestPullRestoresPushedTree(t *testing.T) {
	ctx, rclone, _ := newFakeRclone(t, "")
	dev := makeDevTree(t)
	if _, err := Push(ctx, pushOptions(t, rclone, dev, nil)); err != nil {
		t.Fatal(err)
	}
	into := t.TempDir()
	var out bytes.Buffer
	outcomes, err := Pull(ctx, PullOptions{Remote: DefaultRemote, Host: "ws1", Into: into, DevDir: dev, Rclone: rclone, Out: &out})
	if err != nil {
		t.Fatal(err, out.String())
	}
	if len(outcomes) != 6 || !strings.Contains(out.String(), StatusRestored) {
		t.Fatalf("pull outcomes = %v\n%s", statuses(outcomes), out.String())
	}
	restored := filepath.Join(into, "ws1", "dev")
	if readTestFile(t, filepath.Join(restored, "org", "app", "main.go")) != "package main\n" ||
		readTestFile(t, filepath.Join(restored, "org", "notes.md")) != "notes\n" ||
		readTestFile(t, filepath.Join(restored, "solo", ".git", "HEAD")) != "ref: refs/heads/main\n" {
		t.Fatal("restored tree differs")
	}
	if _, err := os.Stat(filepath.Join(into, "ws1", "agent-state", "manifest.json")); err != nil {
		t.Fatalf("agent state not restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(restored, "scratch", "build")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("build cache restored")
	}
}

func TestPullRefusals(t *testing.T) {
	ctx, rclone, _ := newFakeRclone(t, "")
	dev := makeDevTree(t)
	if _, err := Push(ctx, pushOptions(t, rclone, dev, nil)); err != nil {
		t.Fatal(err)
	}
	base := PullOptions{Remote: DefaultRemote, Host: "ws1", DevDir: dev, Rclone: rclone}
	inside := base
	inside.Into = dev
	if _, err := Pull(ctx, inside); err == nil || !strings.Contains(err.Error(), "inside the dev folder") {
		t.Fatalf("pull into the dev folder = %v", err)
	}
	full := base
	full.Into = t.TempDir()
	writeTestFile(t, filepath.Join(full.Into, "ws1", "existing.txt"), "keep")
	if _, err := Pull(ctx, full); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("pull into a non-empty target = %v", err)
	}
	unknown := base
	unknown.Into, unknown.Host = t.TempDir(), "nobody"
	if _, err := Pull(ctx, unknown); err == nil || !strings.Contains(err.Error(), "no archives") {
		t.Fatalf("pull of an unknown host = %v", err)
	}
	invalid := base
	invalid.Into, invalid.Host = t.TempDir(), ""
	if _, err := Pull(ctx, invalid); err == nil {
		t.Fatal("empty host accepted")
	}
}

func TestListGroupsByHost(t *testing.T) {
	ctx, rclone, _ := newFakeRclone(t, "")
	archives, err := List(ctx, ListOptions{Remote: DefaultRemote, Rclone: rclone})
	if err != nil || len(archives) != 0 {
		t.Fatalf("empty remote listed %v, %v", archives, err)
	}
	if _, err := Push(ctx, pushOptions(t, rclone, makeDevTree(t), nil)); err != nil {
		t.Fatal(err)
	}
	archives, err = List(ctx, ListOptions{Remote: DefaultRemote, Rclone: rclone})
	if err != nil || len(archives) != 6 {
		t.Fatalf("listed %v, %v", archives, err)
	}
	for _, archive := range archives {
		if archive.Host != "ws1" || archive.Size == 0 || archive.ModTime.IsZero() || time.Since(archive.ModTime) > time.Hour {
			t.Fatalf("archive %+v lacks host, size or time", archive)
		}
	}
	failing, broken, _ := newFakeRclone(t, "lsjson")
	if _, err := List(failing, ListOptions{Remote: DefaultRemote, Rclone: broken}); err == nil {
		t.Fatal("listing failure hidden")
	}
	if _, err := List(ctx, ListOptions{Remote: "-flag", Rclone: rclone}); err == nil {
		t.Fatal("remote starting with '-' accepted")
	}
}

func TestFormatBytes(t *testing.T) {
	cases := map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 1536 * 1024: "1.5 MiB", 5 << 40: "5.0 TiB", 3 << 50: "3072.0 TiB"}
	for n, want := range cases {
		if got := FormatBytes(n); got != want {
			t.Errorf("FormatBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestParseSizeAccepts(t *testing.T) {
	// positive: plain bytes and every unit ParseSize documents, case-insensitively.
	cases := map[string]int64{
		"1024": 1024, "0": 0, "2GiB": 2 << 30, "2GIB": 2 << 30, "2gib": 2 << 30,
		"500MiB": 500 * (1 << 20), "1KiB": 1 << 10, "1TiB": 1 << 40, "1 GiB": 1 << 30,
		"1.5GiB": int64(1.5 * float64(int64(1)<<30)), "10B": 10,
		"none": 0, "None": 0, "NONE": 0, "  2GiB  ": 2 << 30,
	}
	for in, want := range cases {
		got, err := ParseSize(in)
		if err != nil {
			t.Errorf("ParseSize(%q) = %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseSize(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestParseSizeRejects(t *testing.T) {
	// negative: not a number, an unknown suffix, and a negative number.
	for _, in := range []string{"", "abc", "2GB", "2XiB", "-1", "-1GiB", "GiB", "2 2GiB"} {
		if _, err := ParseSize(in); err == nil {
			t.Errorf("ParseSize(%q) accepted", in)
		}
	}
}

func TestParseSizeRejectsNonFinite(t *testing.T) {
	// negative (#386): NaN and infinity, in any case, sign or unit, are not non-negative numbers;
	// none of them may reach the integer conversion and silently disable the cap.
	for _, in := range []string{"NaN B", "nan KiB", "NaN MiB", "Inf B", "+Inf MiB", "inf GiB", "Infinity B", "+infinity TiB", "-Inf B"} {
		if got, err := ParseSize(in); err == nil || !strings.Contains(err.Error(), "not a non-negative number") {
			t.Errorf("ParseSize(%q) = %d, %v, want the not-a-non-negative-number error", in, got, err)
		}
	}
}

func TestParseSizeOverflowBoundary(t *testing.T) {
	// boundary: the largest whole TiB count int64 holds parses exactly; 2^63 bytes, by unit or as
	// the plain MaxInt64 spelling float64 rounds up to it, and the largest finite float are too
	// large rather than wrapping to a negative cap that disables the check.
	if got, err := ParseSize("8388607TiB"); err != nil || got != 8388607<<40 {
		t.Fatalf("ParseSize(8388607TiB) = %d, %v", got, err)
	}
	for _, in := range []string{"8388608TiB", "9223372036854775807", "1.7976931348623157e308 B"} {
		if got, err := ParseSize(in); err == nil || !strings.Contains(err.Error(), "too large") {
			t.Errorf("ParseSize(%q) = %d, %v, want the too-large error", in, got, err)
		}
	}
}

func TestParseSizeBoundary(t *testing.T) {
	// boundary: the unit multipliers themselves, and the zero/"none" spellings that disable a cap.
	if got, err := ParseSize("1KiB"); err != nil || got != 1024 {
		t.Fatalf("ParseSize(1KiB) = %d, %v", got, err)
	}
	for _, in := range []string{"0", "0B", "0GiB", "none"} {
		if got, err := ParseSize(in); err != nil || got != 0 {
			t.Errorf("ParseSize(%q) = %d, %v, want 0, nil", in, got, err)
		}
	}
}
