// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package admission

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/sentinel"
	"github.com/cordanaLLM/praetor/internal/util"
)

const executorProbeTimeout = 2 * time.Second

// ErrNoExecutor identifies a transient refusal after the executor readings were exhausted.
var ErrNoExecutor = errors.New("no remote executor admitted")

// RemoteWaiter applies the admission policy to the live readings of a configured executor list.
type RemoteWaiter struct {
	Executors     []Executor
	Policy        Policy
	DefaultLoad   bool // use the default load limit per executor core when --max-load was omitted
	MaxWait, Poll time.Duration
	Log           io.Writer
}

type executorReading struct {
	stats      sentinel.HostStats
	cores      int
	coreSource string
	err        error
}

// Wait returns the admitted executor with the largest memory headroom, then the lowest
// load per core. A final tie follows the inventory order.
func (w RemoteWaiter) Wait(ctx context.Context, label string) (Executor, RemoteAdmitInfo, Verdict, error) {
	if err := w.validate(); err != nil {
		return Executor{}, RemoteAdmitInfo{}, Verdict{}, err
	}
	waitCtx, cancel := context.WithTimeout(ctx, w.MaxWait)
	defer cancel()
	var last string
	for range maxPolls {
		selected, info, verdict, skipped := w.choose(probeExecutors(waitCtx, w.Executors))
		last = strings.Join(skipped, "; ")
		if err := ctx.Err(); err != nil {
			return Executor{}, RemoteAdmitInfo{}, Verdict{}, fmt.Errorf("admission %s: %w", label, err)
		}
		if verdict.Admit {
			return selected, info, verdict, logExecutorWait(w.Log, label, last)
		}
		if err := logExecutorWait(w.Log, label, last); err != nil {
			return Executor{}, RemoteAdmitInfo{}, Verdict{}, err
		}
		if !waitForNextReading(waitCtx, w.Poll) {
			break
		}
	}
	return Executor{}, RemoteAdmitInfo{}, Verdict{}, fmt.Errorf("admission %s: %w: %s", label, ErrNoExecutor, last)
}

// RemoteAdmitInfo names how many cores and jobs the admitted step will use on the executor.
type RemoteAdmitInfo struct {
	Cores      int
	CoreSource string
	Concurrent int
	Jobs       int
}

// validate checks that the RemoteWaiter has valid executors, positive intervals, and a log writer.
func (w RemoteWaiter) validate() error {
	if len(w.Executors) == 0 || len(w.Executors) > maxExecutors || w.Poll <= 0 || w.MaxWait <= 0 || w.Log == nil {
		return errors.New("invalid remote admission waiter")
	}
	return nil
}

// waitForNextReading sleeps for the polling interval or until context cancellation, reporting whether time remains.
func waitForNextReading(ctx context.Context, poll time.Duration) bool {
	deadline, _ := ctx.Deadline()
	remaining := time.Until(deadline)
	return remaining > 0 && SleepContext(ctx, min(poll, remaining)) == nil && time.Until(deadline) > 0
}

// logExecutorWait writes formatted admission retry reasons to the provided log writer.
func logExecutorWait(log io.Writer, label, reasons string) error {
	if reasons == "" {
		return nil
	}
	_, err := fmt.Fprintf(log, "%s admission %s: %s\n", time.Now().Format("15:04:05"), label, reasons)
	return err
}

// choose selects the eligible executor offering the most memory headroom, breaking ties with load per core.
func (w RemoteWaiter) choose(readings []executorReading) (Executor, RemoteAdmitInfo, Verdict, []string) {
	var selected Executor
	var best Verdict
	var bestInfo RemoteAdmitInfo
	var bestMemory uint64
	var bestLoad float64
	var skipped []string
	for i := 0; i < len(readings) && i < maxExecutors; i++ {
		e, reading := w.Executors[i], readings[i]
		if reading.err != nil {
			skipped = append(skipped, fmt.Sprintf("%s skipped: probe failed: %q", e.Name, reading.err.Error()))
			continue
		}
		if reading.cores < 1 {
			skipped = append(skipped, fmt.Sprintf("%s skipped: %q", e.Name, "no core count"))
			continue
		}
		policy := w.Policy
		if w.DefaultLoad {
			policy.MaxLoad1 = defaultMaxLoadPerCore * float64(reading.cores)
		}
		verdict := Judge(reading.stats, policy)
		if !verdict.Admit {
			skipped = append(skipped, fmt.Sprintf("%s held: %s (%s, load %s)", e.Name, verdict.Reason,
				ReadingsText(reading.stats), loadText(reading.stats)))
			continue
		}
		memory := reading.stats.RAMFreeBytes - policy.MinAvailableBytes
		load := reading.stats.CPULoad1Min / float64(reading.cores)
		if !best.Admit || memory > bestMemory || memory == bestMemory && load < bestLoad {
			selected = e
			best = verdict
			bestInfo = RemoteAdmitInfo{Cores: reading.cores, CoreSource: reading.coreSource, Jobs: StepParallelismJobs(reading.cores, 1), Concurrent: 1}
			bestMemory, bestLoad = memory, load
		}
	}
	return selected, bestInfo, best, skipped
}

// probeExecutors queries all configured remote executors concurrently to obtain system statistics.
func probeExecutors(ctx context.Context, executors []Executor) []executorReading {
	ctx, cancel := context.WithTimeout(ctx, executorProbeTimeout)
	defer cancel()
	results := make([]chan executorReading, len(executors))
	for i := 0; i < len(executors) && i < maxExecutors; i++ {
		result := make(chan executorReading, 1)
		results[i] = result
		go func(e Executor) { result <- probeExecutor(ctx, e) }(executors[i])
	}
	readings := make([]executorReading, len(executors))
	for i := 0; i < len(results) && i < maxExecutors; i++ {
		readings[i] = receiveReading(ctx, results[i])
	}
	return readings
}

// receiveReading retrieves an executor reading from a channel or returns context expiration errors.
func receiveReading(ctx context.Context, result <-chan executorReading) executorReading {
	select {
	case reading := <-result:
		return reading
	case <-ctx.Done():
		select {
		case reading := <-result:
			return reading
		default:
			return executorReading{err: ctx.Err()}
		}
	}
}

// probeExecutor connects to a remote executor via SSH to collect resource statistics and cgroup limits.
func probeExecutor(ctx context.Context, e Executor) executorReading {
	const procOutputLimit = 64 << 10
	if err := e.validate(); err != nil {
		return executorReading{err: err}
	}
	command := remoteProbeCommand()
	out, err := util.RunCommandBytes(ctx, "", "ssh", procOutputLimit, e.sshArgs(command)...)
	if err != nil {
		return executorReading{err: util.CommandDiagnostic(err, out.Stderr)}
	}
	sections, err := parseRemoteProbeSections(out.Stdout)
	if err != nil {
		return executorReading{err: err}
	}
	if bytes.Contains(sections[2], []byte("__MISSING__")) || bytes.Contains(sections[3], []byte("__MISSING__")) ||
		bytes.Contains(sections[4], []byte("__MISSING__")) {
		return executorReading{err: fmt.Errorf("executor %s: pod cgroup counters unreadable", e.Name)}
	}
	stats, err := sentinel.ReadLinuxAdmissionStats(bytes.NewReader(sections[0]), bytes.NewReader(sections[1]))
	if err != nil {
		return executorReading{err: err}
	}
	headroom, err := remoteExecutorHeadroom(e.Name, stats, sections[2], sections[3])
	if err != nil {
		return executorReading{err: err}
	}
	stats.RAMFreeBytes = headroom
	cores, source, err := measuredExecutorCores(e.Name, e.Cores, sections[4])
	if err != nil {
		return executorReading{err: err}
	}
	return executorReading{stats: stats, cores: cores, coreSource: source}
}

// sshArgs constructs standard hardened SSH command-line arguments for remote executor execution.
func (e Executor) sshArgs(command string) []string {
	return []string{"-T", "-oBatchMode=yes", "-oConnectTimeout=2", "-oConnectionAttempts=1",
		"-oStrictHostKeyChecking=yes", "-oClearAllForwardings=yes", "-oForwardAgent=no", "-oControlPath=none",
		"-oServerAliveInterval=5", "-oServerAliveCountMax=2", "-oStdinNull=no", "-oEscapeChar=none",
		"-oPermitLocalCommand=no", "-oForkAfterAuthentication=no", "-l", e.SSHUser, "--", e.SSHHost, command}
}
