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
	stats sentinel.HostStats
	err   error
}

// Wait returns the admitted executor with the largest memory headroom, then the lowest
// load per core. A final tie follows the inventory order.
func (w RemoteWaiter) Wait(ctx context.Context, label string) (Executor, Verdict, error) {
	if err := w.validate(); err != nil {
		return Executor{}, Verdict{}, err
	}
	waitCtx, cancel := context.WithTimeout(ctx, w.MaxWait)
	defer cancel()
	var last string
	for range maxPolls {
		selected, verdict, skipped := w.choose(probeExecutors(waitCtx, w.Executors))
		last = strings.Join(skipped, "; ")
		if err := ctx.Err(); err != nil {
			return Executor{}, Verdict{}, fmt.Errorf("admission %s: %w", label, err)
		}
		if verdict.Admit {
			return selected, verdict, logExecutorWait(w.Log, label, last)
		}
		if err := logExecutorWait(w.Log, label, last); err != nil {
			return Executor{}, Verdict{}, err
		}
		if !waitForNextReading(waitCtx, w.Poll) {
			break
		}
	}
	return Executor{}, Verdict{}, fmt.Errorf("admission %s: %w: %s", label, ErrNoExecutor, last)
}

func (w RemoteWaiter) validate() error {
	if len(w.Executors) == 0 || len(w.Executors) > maxExecutors || w.Poll <= 0 || w.MaxWait <= 0 || w.Log == nil {
		return errors.New("invalid remote admission waiter")
	}
	return nil
}

func waitForNextReading(ctx context.Context, poll time.Duration) bool {
	deadline, _ := ctx.Deadline()
	remaining := time.Until(deadline)
	return remaining > 0 && SleepContext(ctx, min(poll, remaining)) == nil && time.Until(deadline) > 0
}

func logExecutorWait(log io.Writer, label, reasons string) error {
	if reasons == "" {
		return nil
	}
	_, err := fmt.Fprintf(log, "%s admission %s: %s\n", time.Now().Format("15:04:05"), label, reasons)
	return err
}

func (w RemoteWaiter) choose(readings []executorReading) (Executor, Verdict, []string) {
	var selected Executor
	var best Verdict
	var bestMemory uint64
	var bestLoad float64
	var skipped []string
	for i := 0; i < len(readings) && i < maxExecutors; i++ {
		e, reading := w.Executors[i], readings[i]
		if reading.err != nil {
			skipped = append(skipped, fmt.Sprintf("%s skipped: probe failed: %q", e.Name, reading.err.Error()))
			continue
		}
		policy := w.Policy
		if w.DefaultLoad {
			policy.MaxLoad1 = defaultMaxLoadPerCore * float64(e.Cores)
		}
		verdict := Judge(reading.stats, policy)
		if !verdict.Admit {
			skipped = append(skipped, fmt.Sprintf("%s held: %s (%s, load %s)", e.Name, verdict.Reason,
				ReadingsText(reading.stats), loadText(reading.stats)))
			continue
		}
		memory := reading.stats.RAMFreeBytes - policy.MinAvailableBytes
		load := reading.stats.CPULoad1Min / float64(e.Cores)
		if !best.Admit || memory > bestMemory || memory == bestMemory && load < bestLoad {
			selected, best, bestMemory, bestLoad = e, verdict, memory, load
		}
	}
	return selected, best, skipped
}

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

func probeExecutor(ctx context.Context, e Executor) executorReading {
	const procOutputLimit = 64 << 10
	if err := e.validate(); err != nil {
		return executorReading{err: err}
	}
	command := "timeout --kill-after=1s 2s cat /proc/loadavg /proc/meminfo"
	out, err := util.RunCommandBytes(ctx, "", "ssh", procOutputLimit, e.sshArgs(command)...)
	if err != nil {
		return executorReading{err: util.CommandDiagnostic(err, out.Stderr)}
	}
	load, memory, ok := bytes.Cut(out.Stdout, []byte{'\n'})
	if !ok {
		return executorReading{err: errors.New("missing loadavg or meminfo output")}
	}
	stats, err := sentinel.ReadLinuxAdmissionStats(bytes.NewReader(load), bytes.NewReader(memory))
	return executorReading{stats: stats, err: err}
}

func (e Executor) sshArgs(command string) []string {
	return []string{"-T", "-oBatchMode=yes", "-oConnectTimeout=2", "-oConnectionAttempts=1",
		"-oStrictHostKeyChecking=yes", "-oClearAllForwardings=yes", "-oForwardAgent=no", "-oControlPath=none",
		"-oServerAliveInterval=5", "-oServerAliveCountMax=2", "-oStdinNull=no", "-oEscapeChar=none",
		"-oPermitLocalCommand=no", "-oForkAfterAuthentication=no", "-l", e.SSHUser, "--", e.SSHHost, command}
}
