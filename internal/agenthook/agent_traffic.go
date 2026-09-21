package agenthook

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/router"
)

const routingConfigRel = ".config/models/routing.yaml"

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

func validateAgentBrief(ctx context.Context, root, text string) (config.Resolution, error) {
	task, err := caveman.ExtractBriefTask(text)
	if err != nil {
		return config.Resolution{}, err
	}
	if !router.ValidTaskLabel(task) {
		return config.Resolution{}, fmt.Errorf("invalid task label %q", task)
	}
	manifest, err := loadTrafficManifest(ctx, root)
	if err != nil {
		return config.Resolution{}, err
	}
	if err := requireDeclaredTask(ctx, root, task); err != nil {
		return config.Resolution{}, err
	}
	resolution := manifest.EffectiveRegister().Resolve(config.SurfaceAgent, task)
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

func loadTrafficManifest(ctx context.Context, root string) (*config.Manifest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	manifest, err := config.LoadManifest(filepath.Join(root, ".standards.yaml"))
	if err != nil {
		return nil, fmt.Errorf("load register policy: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return manifest, nil
}

func requireDeclaredTask(ctx context.Context, root, task string) error {
	path := filepath.Join(root, filepath.FromSlash(routingConfigRel))
	labels := router.DefaultTaskLabels()
	if _, err := os.Lstat(path); err == nil {
		cfg, loadErr := router.LoadRoutingConfigContext(ctx, path)
		if loadErr != nil {
			return fmt.Errorf("load routing labels: %w", loadErr)
		}
		labels = router.DeclaredTaskLabels(cfg)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect routing labels: %w", err)
	}
	for index := 0; index < len(labels) && index < router.MaxTaskLabels; index++ {
		if labels[index] == task {
			return nil
		}
	}
	return fmt.Errorf("task %q is not declared by routing", task)
}

func trafficDenied(err error) Verdict {
	return Verdict{Outcome: Deny, Reason: "[BLOCKED BY HISS] subagent text register: " + err.Error()}
}
