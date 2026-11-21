// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package admission

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/sentinel"
)

const cgroupSectionEnd = "__PRAETOR_CGROUP_END__\n"

// LimitsMemoryMaxBytes is the step MemoryMax in cgroup byte counts (systemd K/M/G/T, base 1024).
func LimitsMemoryMaxBytes(l Limits) (uint64, error) {
	if err := l.Check(); err != nil {
		return 0, err
	}
	scale, ok := memoryUnitScale(l.MemoryMax[len(l.MemoryMax)-1])
	if !ok {
		return 0, fmt.Errorf("memory limit %q", l.MemoryMax)
	}
	n, err := strconv.ParseUint(l.MemoryMax[:len(l.MemoryMax)-1], 10, 64)
	if err != nil || n == 0 {
		return 0, fmt.Errorf("memory limit %q", l.MemoryMax)
	}
	return n * scale, nil
}

// memoryUnitScale returns the byte multiplier corresponding to a single-letter memory unit suffix.
func memoryUnitScale(unit byte) (uint64, bool) {
	switch unit {
	case 'K', 'k':
		return 1 << 10, true
	case 'M', 'm':
		return 1 << 20, true
	case 'G', 'g':
		return 1 << 30, true
	case 'T', 't':
		return 1 << 40, true
	default:
		return 0, false
	}
}

// parseCgroupBytes reads a cgroup memory counter line (a decimal byte count or "max").
func parseCgroupBytes(raw []byte) (uint64, bool, error) {
	text := strings.TrimSpace(string(raw))
	if text == "" {
		return 0, false, errorsMissing("empty cgroup value")
	}
	if text == "max" {
		return 0, true, nil
	}
	value, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("cgroup counter %q", text)
	}
	return value, false, nil
}

// errorsMissing constructs an error indicating a missing or unreadable cgroup metric.
func errorsMissing(msg string) error { return fmt.Errorf("%s", msg) }

// parseCPUMaxCores turns cgroup cpu.max "quota period" into a core count, or reports unlimited.
func parseCPUMaxCores(raw []byte) (int, bool, error) {
	text := strings.TrimSpace(string(raw))
	if text == "__MISSING__" {
		return 0, false, errorsMissing("pod cpu.max unreadable")
	}
	if text == "" || text == "max" {
		return 0, true, nil
	}
	fields := strings.Fields(text)
	if len(fields) != 2 {
		return 0, false, fmt.Errorf("cpu.max %q", text)
	}
	period, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || period <= 0 {
		return 0, false, fmt.Errorf("cpu.max period %q", fields[1])
	}
	if fields[0] == "max" {
		return 0, true, nil
	}
	quota, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || quota <= 0 {
		return 0, false, fmt.Errorf("cpu.max quota %q", fields[0])
	}
	return max(1, int(quota/period)), false, nil
}

// measuredExecutorCores is the lesser of the list cores and the pod cpu.max when both exist.
func measuredExecutorCores(name string, listCores *int, cpuMax []byte) (int, string, error) {
	cgroupCores, cgroupMax, err := parseCPUMaxCores(cpuMax)
	if err != nil {
		return 0, "", fmt.Errorf("executor %s: %w", name, err)
	}
	switch {
	case listCores != nil && *listCores >= 1 && !cgroupMax:
		return min(*listCores, cgroupCores), "list and pod cpu.max", nil
	case listCores != nil && *listCores >= 1 && cgroupMax:
		return *listCores, "list cores", nil
	case listCores == nil && !cgroupMax:
		return cgroupCores, "pod cpu.max", nil
	default:
		return 0, "", fmt.Errorf("executor %s: no core count (list cores missing, cpu.max unlimited)", name)
	}
}

// remoteExecutorHeadroom is min(MemAvailable, memory.max − memory.current); a bad cgroup read fails closed.
func remoteExecutorHeadroom(name string, stats sentinel.HostStats, memoryMax, memoryCurrent []byte) (uint64, error) {
	maxBytes, maxUnlimited, err := parseCgroupBytes(memoryMax)
	if err != nil {
		return 0, fmt.Errorf("executor %s: pod memory.max: %w", name, err)
	}
	current, currentUnlimited, err := parseCgroupBytes(memoryCurrent)
	if err != nil || currentUnlimited {
		return 0, fmt.Errorf("executor %s: pod memory.current: invalid cgroup counter", name)
	}
	cgroupFree := uint64(0)
	if maxUnlimited {
		cgroupFree = math.MaxUint64
	} else if maxBytes >= current {
		cgroupFree = maxBytes - current
	}
	return min(stats.RAMFreeBytes, cgroupFree), nil
}

// remoteProbeCommand constructs the shell command for reading load, memory, and cgroup metrics on a remote host.
func remoteProbeCommand() string {
	return "timeout --kill-after=1s 2s sh -c '" +
		`cgroup=${PRAETOR_ADMIT_CGROUP_DIR:-/sys/fs/cgroup}; ` +
		`for f in /proc/loadavg /proc/meminfo "$cgroup/memory.max" "$cgroup/memory.current" "$cgroup/cpu.max"; do` +
		` cat "$f" 2>/dev/null || echo __MISSING__; echo ` + cgroupSectionEnd +
		` done'`
}

// parseRemoteProbeSections splits probe output into sections for load, memory, and cgroup metrics.
func parseRemoteProbeSections(out []byte) ([][]byte, error) {
	chunks := bytes.Split(out, []byte(cgroupSectionEnd))
	if len(chunks) < 6 {
		return nil, errorsMissing("remote probe output too short")
	}
	return chunks[:5], nil
}

// StepParallelismJobs is cores divided by concurrent admitted steps, at least 1.
func StepParallelismJobs(cores, concurrent int) int {
	if cores < 1 {
		cores = 1
	}
	if concurrent < 1 {
		concurrent = 1
	}
	jobs := cores / concurrent
	if jobs < 1 {
		jobs = 1
	}
	return jobs
}
