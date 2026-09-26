package agenthook

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/router"
)

func evaluateAgentTraffic(ctx context.Context, row Registration, canonical Canonical, root string, in Invocation) Verdict {
	switch row.Event {
	case EventPreDispatch:
		return evaluateAgentBriefs(ctx, row, canonical, root, in)
	case EventDispatchReceipt:
		return evaluateDispatchReceipt(ctx, row, canonical, root, in)
	case EventDispatchAbort:
		return evaluateDispatchAbort(ctx, row, canonical, root, in)
	case EventPreHandback:
		return evaluateAgentHandback(ctx, row, canonical, root, in)
	case EventHandbackReceipt:
		return evaluateHandbackReceipt(ctx, row, canonical, root, in)
	case EventHandbackAbort:
		return evaluateHandbackAbort(ctx, row, canonical, root, in)
	case EventPostReturn:
		return evaluateAgentReturn(ctx, row, canonical, root, in)
	default:
		return trafficDenied(errors.New("unsupported agent traffic event"))
	}
}

func evaluateDispatchAbort(ctx context.Context, row Registration, canonical Canonical, root string, in Invocation) Verdict {
	store, err := newCorrelationStore(ctx, root, in.CorrelationDir)
	if err == nil {
		err = store.cancelPending(ctx, row.Client, canonical.ConversationID, canonical.ToolUseID)
	}
	if err != nil {
		return trafficDenied(fmt.Errorf("release dispatch: %w", err))
	}
	return Verdict{Outcome: Allow}
}

func evaluateAgentHandback(ctx context.Context, row Registration, canonical Canonical, root string, in Invocation) Verdict {
	store, err := newCorrelationStore(ctx, root, in.CorrelationDir)
	if err != nil {
		return trafficDenied(err)
	}
	entry, err := store.active(ctx, row.Client, canonical.ConversationID, canonical.AgentID)
	if err != nil {
		return trafficDenied(err)
	}
	if entry.HandbackDelivered {
		return trafficDenied(errors.New("subagent handback already delivered"))
	}
	verdict := validateAgentReturn(entry.Resolution, canonical.Return)
	if verdict.Outcome != Allow {
		return verdict
	}
	if err := store.markHandbackValidated(ctx, row.Client, canonical.ConversationID, canonical.AgentID,
		canonical.ToolUseID, canonical.Return); err != nil {
		return trafficDenied(err)
	}
	return verdict
}

func evaluateHandbackReceipt(ctx context.Context, row Registration, canonical Canonical, root string, in Invocation) Verdict {
	store, err := newCorrelationStore(ctx, root, in.CorrelationDir)
	if err == nil {
		err = store.markHandbackDelivered(ctx, row.Client, canonical.ConversationID, canonical.AgentID,
			canonical.ToolUseID, canonical.Return)
	}
	if err != nil {
		return trafficDenied(fmt.Errorf("confirm handback: %w", err))
	}
	return Verdict{Outcome: Allow}
}

func evaluateHandbackAbort(ctx context.Context, row Registration, canonical Canonical, root string, in Invocation) Verdict {
	store, err := newCorrelationStore(ctx, root, in.CorrelationDir)
	if err == nil {
		err = store.cancelHandback(ctx, row.Client, canonical.ConversationID, canonical.AgentID,
			canonical.ToolUseID, canonical.Return)
	}
	if err != nil {
		return trafficDenied(fmt.Errorf("release handback: %w", err))
	}
	return Verdict{Outcome: Allow}
}

func evaluateAgentBriefs(ctx context.Context, row Registration, canonical Canonical, root string, in Invocation) Verdict {
	if len(canonical.Briefs) == 0 || len(canonical.Briefs) > MaxDispatchBriefs {
		return trafficDenied(fmt.Errorf("dispatch must carry 1..%d briefs", MaxDispatchBriefs))
	}
	var resolution config.Resolution
	for index := 0; index < len(canonical.Briefs) && index < MaxDispatchBriefs; index++ {
		resolved, err := validateAgentBrief(ctx, root, canonical.Briefs[index])
		if err != nil {
			return trafficDenied(fmt.Errorf("brief %d: %w", index, err))
		}
		resolution = resolved
	}
	if row.Client != "claude" {
		return Verdict{Outcome: Allow}
	}
	if len(canonical.Briefs) != 1 {
		return trafficDenied(errors.New("client dispatch must carry exactly one brief"))
	}
	store, err := newCorrelationStore(ctx, root, in.CorrelationDir)
	if err == nil {
		err = store.reserve(ctx, row.Client, canonical.ConversationID, canonical.ToolUseID, resolution)
	}
	if err != nil {
		return trafficDenied(fmt.Errorf("reserve dispatch: %w", err))
	}
	return Verdict{Outcome: Allow}
}

func evaluateDispatchReceipt(ctx context.Context, row Registration, canonical Canonical, root string, in Invocation) Verdict {
	store, err := newCorrelationStore(ctx, root, in.CorrelationDir)
	if err == nil {
		err = store.promote(ctx, row.Client, canonical.ConversationID, canonical.ToolUseID, canonical.AgentID)
	}
	if err != nil {
		return trafficDenied(fmt.Errorf("bind dispatch: %w", err))
	}
	return Verdict{Outcome: Allow}
}

func evaluateAgentReturn(ctx context.Context, row Registration, canonical Canonical, root string, in Invocation) Verdict {
	if row.Client == "codex" {
		return Verdict{Outcome: Skip, Reason: "codex return register unenforceable: spawn payload has no documented subagent correlation key"}
	}
	store, err := newCorrelationStore(ctx, root, in.CorrelationDir)
	if err != nil {
		return trafficDenied(err)
	}
	entry, err := store.active(ctx, row.Client, canonical.ConversationID, canonical.AgentID)
	if err != nil {
		return trafficDenied(err)
	}
	if entry.HandbackDelivered {
		if err := store.complete(ctx, row.Client, canonical.ConversationID, canonical.AgentID); err != nil {
			return trafficDenied(err)
		}
		return Verdict{Outcome: Allow}
	}
	verdict := validateAgentReturn(entry.Resolution, canonical.Return)
	if verdict.Outcome != Allow {
		return verdict // keep correlation: a continued subagent must be checked again
	}
	if err := store.complete(ctx, row.Client, canonical.ConversationID, canonical.AgentID); err != nil {
		return trafficDenied(err)
	}
	return verdict
}

// validateAgentBrief resolves the brief's task through the digest-bound register
// authority, so the resolution carries the manifest SHA-256 that ValidateEmission and the
// stored return contract require. The label must be declared by the routing vocabulary
// that governs root; compile-context validates manifest task rows against the same set.
func validateAgentBrief(ctx context.Context, root, text string) (config.Resolution, error) {
	task, err := caveman.ExtractBriefTask(text)
	if err != nil {
		return config.Resolution{}, err
	}
	if !router.ValidTaskLabel(task) {
		return config.Resolution{}, fmt.Errorf("invalid task label %q", task)
	}
	authority, labels, err := compiler.LoadRegisterTaskAuthority(ctx, root)
	if err != nil {
		return config.Resolution{}, fmt.Errorf("load register policy: %w", err)
	}
	if !slices.Contains(labels, task) {
		return config.Resolution{}, fmt.Errorf("task %q is not declared by routing", task)
	}
	resolution, err := authority.Resolve(config.SurfaceAgent, task)
	if err != nil {
		return config.Resolution{}, fmt.Errorf("resolve register: %w", err)
	}
	if _, err := config.ValidateEmission(resolution, config.SurfaceAgent, caveman.KindBrief, text); err != nil {
		return config.Resolution{}, err
	}
	return resolution, nil
}

func validateAgentReturn(resolution config.Resolution, text string) Verdict {
	if _, err := config.ValidateEmission(resolution, config.SurfaceAgent, caveman.KindReturn, text); err != nil {
		return trafficDenied(err)
	}
	return Verdict{Outcome: Allow}
}

func trafficDenied(err error) Verdict {
	return Verdict{Outcome: Deny, Reason: "[BLOCKED BY HISS] subagent text register: " + err.Error()}
}
