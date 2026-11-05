// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
)

// BypassActorsProperty names the bypass actors of the praetor ruleset in a protection finding.
const BypassActorsProperty = "Bypass actors"

// maxBypassActors bounds the bypass actors read from one ruleset (HISS-02).
const maxBypassActors = 64

// liveBypassRuleset is the part of a ruleset body this comparison reads. BypassActors is a
// pointer so a body without the key (GitHub returns bypass_actors only to a caller with write
// access to the ruleset, REST reference "Get a repository ruleset", read 2026-11-05) is told
// apart from an empty list.
type liveBypassRuleset struct {
	BypassActors *[]struct {
		ActorID    *int64 `json:"actor_id"`
		ActorType  string `json:"actor_type"`
		BypassMode string `json:"bypass_mode"`
	} `json:"bypass_actors"`
}

// EvaluateBypassActors compares the bypass actors of the praetor ruleset as GitHub reports it
// (liveRuleset: the body of GET rulesets/{id}; nil when the repository has no such ruleset)
// with those policy renders (rulesetBypassActors). A declared actor that is missing and a live
// actor that is not declared are both drift: an extra actor can pass every rule of the ruleset.
// A body that omits bypass_actors is drift named unread, so an unread list never passes. sync
// --remote keeps a live ruleset's bypass actors (mergeRuleset), so this drift is changed on
// GitHub by hand.
func EvaluateBypassActors(policy config.BranchProtectionPolicy, liveRuleset []byte) (ProtectionFinding, error) {
	declared := bypassActorKeys(rulesetBypassActors(policy))
	finding := ProtectionFinding{Property: BypassActorsProperty, Declared: describeBypassActors(declared), Verdict: ProtectionEnforced}
	if liveRuleset == nil {
		finding.Live = "no " + RepositoryRulesetName + " ruleset"
		if len(declared) > 0 {
			finding.Verdict = ProtectionDrift
		}
		return finding, nil
	}
	var body liveBypassRuleset
	if err := json.Unmarshal(liveRuleset, &body); err != nil {
		return ProtectionFinding{}, fmt.Errorf("parse the bypass actors of the live ruleset: %w", err)
	}
	if body.BypassActors == nil {
		finding.Live, finding.Verdict = "unread (GitHub returns bypass_actors only with write access to the ruleset)", ProtectionDrift
		return finding, nil
	}
	actors := *body.BypassActors
	if len(actors) > maxBypassActors {
		return ProtectionFinding{}, fmt.Errorf("the live ruleset lists %d bypass actors, more than %d", len(actors), maxBypassActors)
	}
	live := make([]string, 0, len(actors))
	for i := 0; i < len(actors); i++ {
		live = append(live, bypassActorKey(actors[i].ActorType, actors[i].ActorID, actors[i].BypassMode))
	}
	slices.Sort(live)
	finding.Live = describeBypassActors(live)
	if !slices.Equal(declared, live) { // both sorted; a duplicated live actor is an extra one
		finding.Verdict = ProtectionDrift
	}
	return finding, nil
}

// bypassActorKeys renders rendered bypass actors as sorted comparison keys.
func bypassActorKeys(actors []map[string]any) []string {
	keys := make([]string, 0, len(actors))
	for i := 0; i < len(actors) && i < maxBypassActors; i++ {
		id := "-"
		if value, present := actors[i]["actor_id"]; present && value != nil {
			id = fmt.Sprint(value)
		}
		keys = append(keys, fmt.Sprint(actors[i]["actor_type"])+" "+id+" "+fmt.Sprint(actors[i]["bypass_mode"]))
	}
	slices.Sort(keys)
	return keys
}

// bypassActorKey is one live actor as "type id mode", the form bypassActorKeys renders; an actor
// without an id (OrganizationAdmin, DeployKey) reads "type - mode".
func bypassActorKey(actorType string, id *int64, mode string) string {
	idText := "-"
	if id != nil {
		idText = fmt.Sprint(*id)
	}
	return actorType + " " + idText + " " + mode
}

// describeBypassActors names a sorted actor list, or none.
func describeBypassActors(keys []string) string {
	if len(keys) == 0 {
		return "none"
	}
	return "[" + strings.Join(keys, ", ") + "]"
}
