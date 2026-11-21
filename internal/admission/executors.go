// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package admission

import (
	"errors"
	"fmt"
	"net/netip"
	"path"
	"regexp"
	"strings"
	"unicode"

	"github.com/cordanaLLM/praetor/internal/strictjson"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	maxExecutorListBytes  = 64 << 10
	maxExecutors          = 32
	maxExecutorCores      = 1 << 16
	maxExecutorMemoryGiB  = 1 << 20
	maxWorkspacePath      = 4096
	executorLeasesDir     = "praetor-admit-leases"
	maxExecutorLeaseFiles = 256
)

// ErrExecutorLeaseCap is raised when an executor's lease directory exceeds maxExecutorLeaseFiles lock files.
var ErrExecutorLeaseCap = errors.New("lease directory exceeds lock file limit")

var (
	executorName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	executorUser = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,63}$`)
	sshHostName  = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?\.?$`)
)

// Executor is one Linux SSH destination declared by the operator's inventory.
type Executor struct {
	Name          string `json:"name"`
	SSHHost       string `json:"ssh_host"`
	SSHUser       string `json:"ssh_user"`
	WorkspaceRoot string `json:"workspace_root"`
	Cores         *int   `json:"cores"`
	MemoryGiB     uint64 `json:"memory_gib"`
	Class         string `json:"class"`
}

// ReadExecutors reads a bounded JSON array of distinct, validated CPU-offload destinations.
func ReadExecutors(filename string) ([]Executor, error) {
	raw, err := util.ReadFileLimited(filename, maxExecutorListBytes)
	if err != nil {
		return nil, err
	}
	var executors []Executor
	options := strictjson.Options{MaxBytes: maxExecutorListBytes, MaxDepth: 2, MaxTokens: 16*maxExecutors + 2, RejectNull: true}
	if err := strictjson.Decode(raw, &executors, options); err != nil {
		return nil, err
	}
	if len(executors) == 0 || len(executors) > maxExecutors {
		return nil, fmt.Errorf("executor list requires 1 to %d entries", maxExecutors)
	}
	names := make(map[string]bool, len(executors))
	for i := 0; i < len(executors) && i < maxExecutors; i++ {
		e := executors[i]
		if err := e.validate(); err != nil {
			return nil, fmt.Errorf("executor entry %d (%q): %w", i+1, e.Name, err)
		}
		if names[e.Name] {
			return nil, fmt.Errorf("duplicate executor name %q", e.Name)
		}
		names[e.Name] = true
	}
	return executors, nil
}

// validate checks that the executor attributes conform to name, network, path, core, memory, and class constraints.
func (e Executor) validate() error {
	switch {
	case !validExecutorName(e.Name):
		return fmt.Errorf("invalid or reserved executor name %q", e.Name)
	case !validSSHHost(e.SSHHost) || !executorUser.MatchString(e.SSHUser):
		return fmt.Errorf("invalid SSH host or user")
	case !validWorkspaceRoot(e.WorkspaceRoot):
		return fmt.Errorf("workspace_root must be a clean absolute Linux directory below /")
	case e.Cores != nil && (*e.Cores < 1 || *e.Cores > maxExecutorCores):
		return fmt.Errorf("cores must be 1 to %d", maxExecutorCores)
	case e.MemoryGiB < 1 || e.MemoryGiB > maxExecutorMemoryGiB:
		return fmt.Errorf("memory_gib must be 1 to %d", maxExecutorMemoryGiB)
	case e.Class != "cpu-offload":
		return fmt.Errorf("unknown executor class %q", e.Class)
	}
	return nil
}

// validExecutorName reports whether name is an allowed alphanumeric identifier and not the reserved name local.
func validExecutorName(name string) bool {
	return name != "local" && executorName.MatchString(name)
}

// validSSHHost reports whether host is a zoneless IP address or valid hostname without consecutive dots.
func validSSHHost(host string) bool {
	if addr, err := netip.ParseAddr(host); err == nil {
		return addr.Zone() == ""
	}
	return sshHostName.MatchString(host) && !strings.Contains(host, "..")
}

// validWorkspaceRoot reports whether root is a clean absolute Linux directory path below root without control characters.
func validWorkspaceRoot(root string) bool {
	return len(root) <= maxWorkspacePath && path.IsAbs(root) && root != "/" && path.Clean(root) == root &&
		strings.IndexFunc(root, unicode.IsControl) == -1
}
