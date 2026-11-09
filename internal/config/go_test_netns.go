// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

// ExceptionRuleGoTestNetns is the rule of the gate race stage's Go test network namespace
// (internal/gating, stageConfig.moduleTestExec): an entry names the go.mod of one module whose
// compiled test binaries run without the loopback-only namespace wrapper (praetorctl go
// test-exec). The wrapper's user namespace maps only the caller, so a file the host's root owns
// shows the overflow uid there; a test of a production check that requires root ownership, such
// as the owner of a trusted system binary, cannot pass inside it. While the entry holds, the stage
// runs that module's test binaries in the gate's own namespaces and names the entry in its
// message; every other module stays wrapped, and CI runs every module as before. An expired entry
// fails the stage for its module instead of falling back to either path.
const ExceptionRuleGoTestNetns = "go-test-netns"

// goTestNetnsTargetProblem requires an entry of ExceptionRuleGoTestNetns to name the go.mod of one
// module by path (the module form the api-compatibility rule uses), with no glob or fingerprint,
// and a category even inside the uncategorized grace window: the entry removes one defence, so it
// carries the expiry of its rectification category. Tool and line range are refused by the shared
// target checks.
func (e Exception) goTestNetnsTargetProblem() string {
	if e.Rule != ExceptionRuleGoTestNetns {
		return ""
	}
	if e.Glob != "" || !goModFilePath(e.Path) || e.Fingerprint != "" {
		return "rule " + ExceptionRuleGoTestNetns + " must name the go.mod of one module by path, such as tools/retrieval/go.mod, and nothing else"
	}
	if e.expiryCategory() == "" {
		return "rule " + ExceptionRuleGoTestNetns + " requires a category"
	}
	return ""
}
