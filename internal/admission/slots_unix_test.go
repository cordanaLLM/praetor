// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package admission

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/sentinel"
	"github.com/cordanaLLM/praetor/internal/util"
)

// admitContext bounds fixture operations to ten seconds and registers cancellation with the test
// framework; callers need no separate cancel cleanup.
func admitContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// fixtureSlots creates a private test-owned slot directory with the requested capacity and millisecond
// polling. Temporary-directory cleanup is registered, but callers must close every acquired lease.
func fixtureSlots(t *testing.T, count int) Slots {
	t.Helper()
	path := filepath.Join(t.TempDir(), "slots")
	root, err := util.PrivateDirectory(admitContext(t), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	return Slots{Dir: path, MaxHeavy: count, Poll: time.Millisecond}
}

// closeLease releases a fixture lease and reports cleanup errors without aborting the caller's
// remaining cleanup.
func closeLease(t *testing.T, lease *Lease) {
	t.Helper()
	if err := lease.Close(); err != nil {
		t.Error(err)
	}
}

type queuedLog struct{ queued chan string }

// Write forwards only the initial queued diagnostic without blocking the admission writer; callers
// drain the buffered channel to synchronize waiter arrival.
func (l queuedLog) Write(data []byte) (int, error) {
	if strings.Contains(string(data), "admission queued") {
		select {
		case l.queued <- string(data):
		default:
		}
	}
	return len(data), nil
}

type slotScenario struct {
	slots   Slots
	started chan int
	results chan error
	log     queuedLog
	running atomic.Int32
	maximum atomic.Int32
}

// newSlotScenario creates a three-slot fixture and buffered result channels for five steps. Callers
// must release each step's end channel and join all results with finish.
func newSlotScenario(t *testing.T) *slotScenario {
	t.Helper()
	return &slotScenario{slots: fixtureSlots(t, 3), started: make(chan int, 5), results: make(chan error, 5), log: queuedLog{make(chan string, 5)}}
}

// step holds a lease while recording running and peak counts, then waits for the end signal or
// deadline. The caller must provide buffered result channels and join the reported lease-release
// result.
func (s *slotScenario) step(ctx context.Context, id int, end <-chan struct{}) {
	lease, err := s.slots.Acquire(ctx, fmt.Sprintf("step-%d", id), s.log)
	if err != nil {
		s.results <- err
		return
	}
	current := s.running.Add(1)
	for attempt := 0; attempt < 5; attempt++ {
		old := s.maximum.Load()
		if current <= old || s.maximum.CompareAndSwap(old, current) {
			break
		}
	}
	s.started <- id
	select {
	case <-end:
	case <-ctx.Done():
		err = ctx.Err()
	}
	time.Sleep(2 * time.Millisecond)
	s.running.Add(-1)
	s.results <- errors.Join(err, lease.Close())
}

// nextStarted waits for the expected step identifier within ctx, failing on overtaking or timeout; the
// caller still owns termination of the step.
func nextStarted(t *testing.T, ctx context.Context, started <-chan int, want int) {
	t.Helper()
	select {
	case got := <-started:
		if got != want {
			t.Fatalf("started %d, want %d", got, want)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

// nextQueued requires the named waiter's queued line before any step starts, failing on premature
// admission or timeout. The caller must later release holders and join the waiter.
func nextQueued(t *testing.T, ctx context.Context, queued <-chan string, started <-chan int, label string) {
	t.Helper()
	select {
	case id := <-started:
		t.Fatalf("step %d started before a holder ended", id)
	case text := <-queued:
		if !strings.Contains(text, label) {
			t.Fatalf("queue %q, want %s", text, label)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

// TestAdmitSlots_FiveStepsBoundAndArrivalOrder pins a maximum of three running steps and FIFO
// admission for two excess arrivals. Starting an extra step before a holder ends or allowing the later
// waiter to overtake fails the scenario.
func TestAdmitSlots_FiveStepsBoundAndArrivalOrder(t *testing.T) {
	ctx := admitContext(t)
	s := newSlotScenario(t)
	var ends [5]chan struct{}
	for id := 0; id < 5; id++ {
		ends[id] = make(chan struct{})
	}
	for id := 0; id < 3; id++ {
		go s.step(ctx, id, ends[id])
		nextStarted(t, ctx, s.started, id)
	}
	for id := 3; id < 5; id++ {
		go s.step(ctx, id, ends[id])
		nextQueued(t, ctx, s.log.queued, s.started, fmt.Sprintf("step-%d", id))
	}
	select {
	case id := <-s.started:
		t.Fatalf("step %d started before a holder ended", id)
	default:
	}
	close(ends[0])
	nextStarted(t, ctx, s.started, 3)
	close(ends[1])
	nextStarted(t, ctx, s.started, 4)
	for id := 2; id < 5; id++ {
		close(ends[id])
	}
	s.finish(t, ctx)
}

// finish joins exactly five fixture results and requires a peak of three with no unfinished steps.
// Callers must signal all step end channels before joining.
func (s *slotScenario) finish(t *testing.T, ctx context.Context) {
	t.Helper()
	for id := 0; id < 5; id++ {
		select {
		case err := <-s.results:
			if err != nil {
				t.Error(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if maximum := s.maximum.Load(); maximum != 3 {
		t.Fatalf("maximum %d, want exactly 3", maximum)
	}
	if current := s.running.Load(); current != 0 {
		t.Fatalf("unfinished steps %d", current)
	}
}

// The later waiter tries first after a slot opens: removing FIFO must fail deterministically.
func TestAdmitSlots_LowestTicketOnly(t *testing.T) {
	ctx := admitContext(t)
	slots := fixtureSlots(t, 1)
	holder, err := slots.Acquire(ctx, "holder", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	first := fixtureQueue(t, slots)
	second := fixtureQueue(t, slots)
	for _, q := range []*slotQueue{first, second} {
		lease, _, err := q.attempt(ctx, "waiting")
		if err != nil || lease != nil {
			t.Fatalf("initial ticket: %v, %v", lease, err)
		}
	}
	closeLease(t, holder)
	lease, reason, err := second.attempt(ctx, "later")
	if lease != nil {
		closeLease(t, lease)
		t.Fatal("later ticket overtook oldest")
	}
	if err != nil || !strings.Contains(reason, "position 2 of 2") {
		t.Fatalf("later: %q %v", reason, err)
	}
	lease, _, err = first.attempt(ctx, "oldest")
	if err != nil || lease == nil {
		t.Fatalf("oldest: %v", err)
	}
	closeLease(t, lease)
}

// fixtureQueue opens a slot root and its matching ticket directory for direct queue attempts,
// registering queue cleanup with the test. Callers must close any leases returned by those attempts.
func fixtureQueue(t *testing.T, slots Slots) *slotQueue {
	t.Helper()
	root, err := os.OpenRoot(slots.Dir)
	if err != nil {
		t.Fatal(err)
	}
	q := &slotQueue{root: root, slots: slots}
	t.Cleanup(func() {
		if err := q.close(); err != nil {
			t.Error(err)
		}
	})
	if err := q.openTickets(admitContext(t), slots.Dir); err != nil {
		t.Fatal(err)
	}
	return q
}

// TestAdmitSlots_DeadHolderAndDeadTicket requires stale metadata and an unlocked ticket to stop
// consuming capacity. A closed holder descriptor and planted unlocked ticket expose liveness checks
// based on file existence.
func TestAdmitSlots_DeadHolderAndDeadTicket(t *testing.T) {
	ctx := admitContext(t)
	slots := fixtureSlots(t, 1)
	first, err := slots.Acquire(ctx, "dead", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.file.Close(); err != nil {
		t.Fatal(err)
	}
	dead := filepath.Join(slots.Dir, "tickets", ticketName(1))
	if err := os.WriteFile(dead, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	next, err := slots.Acquire(ctx, "alive", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	closeLease(t, next)
	if _, err := os.Stat(dead); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dead ticket retained: %v", err)
	}
}

// TestAdmitSlots_WaitBoundNamesHolders requires deadline refusal to name all three planted holders and
// queue position, with exactly one initial queued line. Ignoring the wait deadline or losing holder
// metadata fails the assertions.
func TestAdmitSlots_WaitBoundNamesHolders(t *testing.T) {
	ctx := admitContext(t)
	slots := fixtureSlots(t, 3)
	for _, label := range []string{"alpha", "beta", "gamma"} {
		holder, err := slots.Acquire(ctx, label, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { closeLease(t, holder) })
	}
	short, cancel := context.WithTimeout(ctx, 5*time.Millisecond)
	defer cancel()
	var log bytes.Buffer
	lease, err := slots.Acquire(short, "held", &log)
	if lease != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("bound: %v %v", lease, err)
	}
	for _, text := range []string{"3 of 3 heavy steps running", "alpha", "beta", "gamma", "position 1 of 1", "refused"} {
		if !strings.Contains(err.Error(), text) {
			t.Errorf("refusal %v missing %q", err, text)
		}
	}
	if strings.Count(log.String(), "admission queued") != 1 {
		t.Fatalf("queue log %q", log.String())
	}
}

// TestAdmitSlots_DirectoryRefusals requires unsafe slot roots and queue locks to refuse admission.
// Planted symlinks, foreign ownership, group access, and missing parents expose any fallback that
// bypasses directory validation.
func TestAdmitSlots_DirectoryRefusals(t *testing.T) {
	for _, kind := range []string{"symlink", "foreign", "group-writable", "create", "lock"} {
		t.Run(kind, func(t *testing.T) {
			ctx := admitContext(t)
			slots := unsafeSlots(t, kind)
			lease, err := slots.Acquire(ctx, "must-not-run", io.Discard)
			if lease != nil {
				closeLease(t, lease)
				t.Fatal("unsafe directory admitted uncounted")
			}
			if err == nil || !strings.Contains(err.Error(), "refused") {
				t.Fatalf("unsafe %s accepted: %v", kind, err)
			}
		})
	}
}

// unsafeSlots plants one unsafe slot-root or queue-lock condition in a test-owned fixture, including
// simulated foreign ownership without chown. Temporary paths are cleaned by the test framework;
// callers must close any unexpected lease.
func unsafeSlots(t *testing.T, kind string) Slots {
	t.Helper()
	slots := fixtureSlots(t, 1)
	switch kind {
	case "symlink":
		path := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(slots.Dir, path); err != nil {
			t.Fatal(err)
		}
		slots.Dir = path
	case "foreign":
		slots.owns = func(os.FileInfo) bool { return false }
	case "group-writable":
		// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission, go_file-permissions_rule-fileperm -- planted unsafe directory must be refused
		if err := os.Chmod(slots.Dir, 0o770); err != nil {
			t.Fatal(err)
		}
	case "create":
		slots.Dir = filepath.Join(slots.Dir, "absent", "slots")
	case "lock":
		if err := os.Symlink("missing", filepath.Join(slots.Dir, "queue.lock")); err != nil {
			t.Fatal(err)
		}
	}
	return slots
}

// TestAdmitSlots_Ticket4097Refused holds 4096 planted ticket locks and requires the next waiter to be
// refused. Removing the ticket-cap check permits an extra ticket and fails this boundary case.
func TestAdmitSlots_Ticket4097Refused(t *testing.T) {
	ctx := admitContext(t)
	slots := fixtureSlots(t, 1)
	q := fixtureQueue(t, slots)
	for ticket := uint64(1); ticket <= MaxTickets; ticket++ {
		_, release, busy, err := util.OpenPrivateLock(q.tickets, ticketName(ticket), os.O_RDWR|os.O_CREATE, "planted ticket")
		if err != nil || busy {
			t.Fatalf("ticket %d: %v %v", ticket, busy, err)
		}
		t.Cleanup(func() {
			if err := release(); err != nil {
				t.Error(err)
			}
		})
	}
	lease, err := slots.Acquire(ctx, "ticket-4097", io.Discard)
	if lease != nil {
		closeLease(t, lease)
		t.Fatal("ticket 4097 admitted")
	}
	if err == nil || !strings.Contains(err.Error(), "ticket limit 4096") {
		t.Fatalf("ticket bound: %v", err)
	}
}

// TestAdmitSlots_ExplicitZeroAndValidation requires explicit zero to avoid creating a slot directory
// and validates the finite count and positive-poll boundaries. Planted negative, excessive, and zero-poll
// configurations must be rejected.
func TestAdmitSlots_ExplicitZeroAndValidation(t *testing.T) {
	ctx := admitContext(t)
	path := filepath.Join(t.TempDir(), "not-created")
	slots := Slots{Dir: path, MaxHeavy: 0, Poll: time.Millisecond}
	lease, err := slots.Acquire(ctx, "uncounted", io.Discard)
	if err != nil || !strings.Contains(lease.Text, "disabled (--max-heavy 0); admitted uncounted") {
		t.Fatalf("explicit zero: %v %v", lease, err)
	}
	closeLease(t, lease)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("zero counted: %v", err)
	}
	for _, slots := range []Slots{{MaxHeavy: -1, Poll: 1}, {MaxHeavy: MaxHeavySlots + 1, Poll: 1}, {MaxHeavy: 1}} {
		if slots.Check() == nil {
			t.Errorf("invalid slots %+v", slots)
		}
	}
	if err := (Slots{MaxHeavy: MaxHeavySlots, Poll: 1}).Check(); err != nil {
		t.Fatal(err)
	}
}

// TestAdmitSlots_BoundMismatchRefused requires callers sharing a directory to agree on capacity. A
// planted two-slot request against an initialized one-slot directory exposes independent per-caller
// bounds.
func TestAdmitSlots_BoundMismatchRefused(t *testing.T) {
	ctx := admitContext(t)
	slots := fixtureSlots(t, 1)
	lease, err := slots.Acquire(ctx, "one", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer closeLease(t, lease)
	slots.MaxHeavy = 2
	if _, err := slots.Acquire(ctx, "two", io.Discard); err == nil || !strings.Contains(err.Error(), "bound is 1, requested 2") {
		t.Fatalf("mixed bounds: %v", err)
	}
}

// TestAdmitSlots_RuntimeDirectoryResolution pins explicit-directory precedence over environment and
// runtime paths, plus the per-user temporary fallback. A planted override must remain untouched when
// an explicit fixture directory is supplied.
func TestAdmitSlots_RuntimeDirectoryResolution(t *testing.T) {
	ctx := admitContext(t)
	runtime := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	t.Setenv(SlotsDirEnv, "")
	path, err := defaultSlotsDir(ctx)
	if err != nil || path != filepath.Join(runtime, "praetor-admit") {
		t.Fatalf("runtime: %s %v", path, err)
	}
	override := t.TempDir()
	t.Setenv(SlotsDirEnv, override)
	path, err = defaultSlotsDir(ctx)
	if err != nil || path != override {
		t.Fatalf("override: %s %v", path, err)
	}
	explicit := fixtureSlots(t, 1)
	lease, err := explicit.Acquire(ctx, "explicit", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	closeLease(t, lease)
	if _, err := os.Stat(filepath.Join(override, "queue.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("explicit path lost precedence: %v", err)
	}
	t.Setenv(SlotsDirEnv, "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("TMPDIR", t.TempDir())
	path, err = defaultSlotsDir(ctx)
	if err != nil || !strings.Contains(path, fmt.Sprintf("praetor-%d", os.Getuid())) {
		t.Fatalf("temp fallback: %s %v", path, err)
	}
}

// TestAdmitSlots_SingleWaitBoundAndHostFailureRelease requires host-headroom timeout or read failure
// to refuse admission and release the reserved slot. Planted low memory and a read error expose a
// lease retained after host admission fails.
func TestAdmitSlots_SingleWaitBoundAndHostFailureRelease(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprint(failure), func(t *testing.T) {
			ctx := admitContext(t)
			slots := fixtureSlots(t, 1)
			var log bytes.Buffer
			w, _ := waiter([]sentinel.HostStats{host(1, 50, 1)}, nil, 5*time.Millisecond, &log)
			w.Sleep = SleepContext
			if failure {
				w.Read = func() (sentinel.HostStats, error) { return sentinel.HostStats{}, errors.New("planted read failure") }
			}
			_, _, err := w.Acquire(ctx, "host-held", slots)
			if err == nil || !strings.Contains(err.Error(), "refused") {
				t.Fatalf("host gate: %v", err)
			}
			if !failure && !strings.Contains(err.Error(), "available 1.0 GiB") {
				t.Fatalf("last gate missing: %v", err)
			}
			lease, err := slots.Acquire(ctx, "after-failure", io.Discard)
			if err != nil {
				t.Fatal(err)
			}
			closeLease(t, lease)
		})
	}
}

// TestAdmitSlots_LeaseAndContextBoundary pins holder metadata, private slot modes, repeated-close
// errors, and refusal of deadline-free waits. A planted context without cancellation must not permit
// an unbounded acquisition.
func TestAdmitSlots_LeaseAndContextBoundary(t *testing.T) {
	ctx := admitContext(t)
	slots := fixtureSlots(t, 3)
	lease, err := slots.Acquire(ctx, "metadata-holder", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	requireSlotMetadata(t, slots.Dir)
	closeLease(t, lease)
	if err := lease.Close(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("second close: %v", err)
	}
	var empty *Lease
	if err := empty.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := slots.Acquire(context.WithoutCancel(ctx), "unbounded", io.Discard); err == nil {
		t.Fatal("deadline-less wait accepted")
	}
}

// requireSlotMetadata requires the fixture's first slot to contain its holder label and an RFC3339Nano
// timestamp, and all three slot files to have mode 0600. The caller must hold the metadata-holder
// lease while checking.
func requireSlotMetadata(t *testing.T, dir string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "slot-0001"))
	if err != nil || !strings.Contains(string(data), "metadata-holder") {
		t.Fatalf("metadata %q: %v", data, err)
	}
	stamp := strings.Fields(string(data))[0]
	if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
		t.Fatal(err)
	}
	for slot := 1; slot <= 3; slot++ {
		info, err := os.Stat(filepath.Join(dir, fmt.Sprintf("slot-%04d", slot)))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("slot permissions: %v %v", info, err)
		}
	}

}

// TestAdmitSlots_TicketDirectoryRefusals requires the ticket directory to pass the same private-path
// barriers as the slot root. Planted symlinks, foreign ownership, and group access must refuse
// admission rather than bypass counting.
func TestAdmitSlots_TicketDirectoryRefusals(t *testing.T) {
	for _, kind := range []string{"symlink", "foreign", "group-writable"} {
		t.Run(kind, func(t *testing.T) {
			slots := unsafeTicketSlots(t, kind)
			lease, err := slots.Acquire(admitContext(t), "must-not-run", io.Discard)
			if lease != nil {
				closeLease(t, lease)
				t.Fatal("unsafe ticket directory admitted")
			}
			if err == nil || !strings.Contains(err.Error(), "ticket directory refused") {
				t.Fatalf("ticket %s: %v", kind, err)
			}
		})
	}
}

// unsafeTicketSlots plants a symlink, simulated foreign owner, or group-accessible ticket directory
// beneath a test-owned slot root. The test framework removes paths; callers must close any unexpected
// lease.
func unsafeTicketSlots(t *testing.T, kind string) Slots {
	t.Helper()
	slots := fixtureSlots(t, 1)
	path := filepath.Join(slots.Dir, "tickets")
	if kind == "symlink" {
		if err := os.Symlink(t.TempDir(), path); err != nil {
			t.Fatal(err)
		}
		return slots
	}
	root, err := util.PrivateDirectory(admitContext(t), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	if kind == "foreign" {
		slots.owns = func(info os.FileInfo) bool { return info.Name() != "tickets" }
	} else {
		// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission, go_file-permissions_rule-fileperm -- planted unsafe ticket directory must be refused
		if err := os.Chmod(path, 0o770); err != nil {
			t.Fatal(err)
		}
	}
	return slots
}

// TestAdmitSlots_TicketParentIdentity requires the pinned ticket root to match its entry beneath the
// slot root. Supplying a different fixture's ticket parent must fail the identity check.
func TestAdmitSlots_TicketParentIdentity(t *testing.T) {
	ctx := admitContext(t)
	left := fixtureQueue(t, fixtureSlots(t, 1))
	right := fixtureQueue(t, fixtureSlots(t, 1))
	if err := left.tickets.Close(); err != nil {
		t.Fatal(err)
	}
	left.tickets = nil
	err := left.openTickets(ctx, right.slots.Dir)
	if err == nil || !strings.Contains(err.Error(), "parent changed") {
		t.Fatalf("split parent accepted: %v", err)
	}
}
