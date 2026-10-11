// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"time"

	"github.com/cordanaLLM/praetor/internal/clientid"
)

// Operator settings: the clients, hooks, update, framework, forge and topology sections of
// the external policy layers. They are decoded by the same loader, under the same bounds and
// into the same sealed digest as complexity; there is no second settings system.

// Values of clients.mode. Advisory reports an ungoverned client and lets work continue;
// strict refuses a launch below `discovered` and fails verify below `observed`.
const (
	ClientModeAdvisory = "advisory"
	ClientModeStrict   = "strict"
)

// Values of clients.govern. Present governs every known client whose binary the host has;
// listed governs only the clients named under clients.selected.
const (
	GovernPresent = "present"
	GovernListed  = "listed"
)

// Values of hooks.scope. Governed applies the command policy only inside a repository that
// carries .standards.yaml; all applies it everywhere.
const (
	HookScopeGoverned = "governed"
	HookScopeAll      = "all"
)

// Client scopes a selection may name.
const (
	ScopeGlobal    = "global"
	ScopeWorkspace = "workspace"
)

// OperatorSettings is the merged operator configuration. Like EffectivePolicy it is
// immutable by convention. The framework, forge and topology sections are omitted from the
// sealed digest while they are empty, so a document without them keeps the digest it had
// before they existed.
type OperatorSettings struct {
	Clients   ClientSettings    `json:"clients"`
	Hooks     HookSettings      `json:"hooks"`
	Update    UpdateSettings    `json:"update"`
	Framework FrameworkSettings `json:"framework,omitzero"`
	Forge     ForgeSettings     `json:"forge,omitzero"`
	Topology  TopologySettings  `json:"topology,omitzero"`
}

// ClientSettings selects and configures the agent clients a workstation governs.
type ClientSettings struct {
	Mode           string                          `json:"mode"`
	VerifiedMaxAge time.Duration                   `json:"verified_max_age"`
	Govern         string                          `json:"govern"`
	Selected       map[clientid.ID]ClientSelection `json:"selected"`
}

// ClientSelection configures one client. Binary and ConfigRoot are host data and come from
// the workstation layer only. Registry and ConnectionProfile keep the spelling of the layer
// that set them; ResolveOperatorPath resolves a relative one against that layer's file.
type ClientSelection struct {
	Required          bool              `json:"required"`
	Scopes            []string          `json:"scopes"`
	Plugin            bool              `json:"plugin"`
	Binary            string            `json:"binary,omitempty"`
	ConfigRoot        string            `json:"config_root,omitempty"`
	Registry          string            `json:"registry,omitempty"`
	ConnectionProfile string            `json:"connection_profile,omitempty"`
	Permissions       ClientPermissions `json:"permissions"`
}

// ClientPermissions is the grant list Praetor appends to a client's own list when Manage is
// set. It never removes or widens an entry.
type ClientPermissions struct {
	Manage bool     `json:"manage"`
	Allow  []string `json:"allow"`
}

// HookSettings configures `praetorctl hook`.
type HookSettings struct {
	Scope         string                `json:"scope"`
	CommandPolicy CommandPolicySettings `json:"command_policy"`
	Python        [][]string            `json:"python"`
}

// CommandPolicySettings carries the operator deny patterns (RE2). Organisation names and
// other operator data arrive here; the engine ships none.
type CommandPolicySettings struct {
	Deny []string `json:"deny"`
}

// UpdateSettings configures how a workstation follows its operational fork.
type UpdateSettings struct {
	Channel          string        `json:"channel"`
	Source           string        `json:"source"`
	Checkout         string        `json:"checkout,omitempty"`
	Remote           string        `json:"remote"`
	Branch           string        `json:"branch"`
	Pin              string        `json:"pin,omitempty"`
	Interval         time.Duration `json:"interval"`
	RequireSigned    bool          `json:"require_signed"`
	AllowedSigners   string        `json:"allowed_signers,omitempty"`
	ReceiptPublicKey string        `json:"receipt_public_key,omitempty"`
	BinDir           string        `json:"bin_dir,omitempty"`
}

// FrameworkSettings names the frameworks the needs engine scores repositories against
// (ADR-0014). Every value is operator data: the engine ships none, and a language without a
// target has no framework to map its dependencies to.
type FrameworkSettings struct {
	// Targets maps a framework language (FrameworkLanguages) to that language's framework.
	Targets map[string]FrameworkTarget `json:"targets,omitzero"`
	// MigrationBranch is the branch `needs migrate` creates; empty selects the built-in name.
	MigrationBranch string `json:"migration_branch,omitzero"`
}

// FrameworkTarget is one language's framework. BuilderKits[0] is where demand requests for
// the language are routed. Contract keeps the spelling of the layer that set it;
// ResolveOperatorPath resolves a relative one against that layer's file. Checkout is host
// data, accepted for go in the workstation layer only.
type FrameworkTarget struct {
	Module      string   `json:"module,omitzero"`
	BuilderKits []string `json:"builder_kits,omitzero"`
	Contract    string   `json:"contract,omitzero"`
	Checkout    string   `json:"checkout,omitzero"`
}

// ForgeSettings supplies what an owner-dependent forge command uses when neither its flags
// nor the repository manifest name a value.
type ForgeSettings struct {
	// DefaultOwner is the last step of owner resolution (ResolveRepositoryIdentity).
	DefaultOwner string `json:"default_owner,omitzero"`
	// ReconcileRepos is the repository set `issue reconcile` walks without --repos.
	ReconcileRepos []string `json:"reconcile_repos,omitzero"`
	// ReviewBot is the bot account requested as a reviewer; empty requests none.
	ReviewBot string `json:"review_bot,omitzero"`
	// ClaimStale is how long an issue claim may go without an update before another session
	// may take it over (`praetorctl issue claim`, #937).
	// Unset (zero) means DefaultClaimStale.
	ClaimStale time.Duration `json:"claim_stale,omitzero"`
}

// DefaultClaimStale is the built-in stale window of an issue claim.
const DefaultClaimStale = 6 * time.Hour

// ClaimStaleWindow is the configured stale window, or DefaultClaimStale when none is set.
func (f ForgeSettings) ClaimStaleWindow() time.Duration {
	if f.ClaimStale <= 0 {
		return DefaultClaimStale
	}
	return f.ClaimStale
}

// TopologySettings names the organisation folders of the workstation dev root that the
// topology audit and the cleanup guard recognise besides the built-in set.
type TopologySettings struct {
	OrgContainers []string `json:"org_containers,omitzero"`
}

// DefaultOperatorSettings are the built-in values. Every known client is governed and
// required where its binary is present, and ungoverned clients are reported rather than
// blocking. Praetor manages no client's grant list by default: operator hosts opt in with
// permissions.manage in their own layer.
func DefaultOperatorSettings() OperatorSettings {
	selected := make(map[clientid.ID]ClientSelection)
	for _, id := range clientid.Known() {
		selected[id] = defaultClientSelection()
	}
	return OperatorSettings{
		Clients: ClientSettings{Mode: ClientModeAdvisory, VerifiedMaxAge: 168 * time.Hour, Govern: GovernPresent, Selected: selected},
		Hooks: HookSettings{Scope: HookScopeGoverned, CommandPolicy: CommandPolicySettings{Deny: []string{}},
			Python: [][]string{{"python3"}, {"python"}, {"py", "-3"}}},
		Update: UpdateSettings{Channel: "push", Source: "checkout", Remote: "origin", Branch: "main",
			Interval: 15 * time.Minute, RequireSigned: true},
	}
}

func defaultClientSelection() ClientSelection {
	return ClientSelection{Required: true, Scopes: []string{ScopeGlobal}, Plugin: true,
		Permissions: ClientPermissions{Allow: []string{}}}
}

// OperatorSettings returns the merged settings, or the built-in values when no layer carried
// an operator section.
func (p *EffectivePolicy) OperatorSettings() OperatorSettings {
	if p == nil || p.Operator == nil {
		return DefaultOperatorSettings()
	}
	return *p.Operator
}
