package agenthook

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/caveman"
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
	case EventSubagentStart:
		return evaluateSubagentStart(ctx, canonical, root)
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
	if errors.Is(err, errNoCorrelation) {
		return unownedAgent()
	}
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
	if errors.Is(err, errNoCorrelation) {
		return unownedAgent()
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
	if errors.Is(err, errNoCorrelation) {
		return unownedAgent()
	}
	if err != nil {
		return trafficDenied(fmt.Errorf("release handback: %w", err))
	}
	return Verdict{Outcome: Allow}
}

func validateAllBriefs(authority briefAuthority, briefs []string) (config.Resolution, error) {
	var resolution config.Resolution
	for index := 0; index < len(briefs) && index < MaxDispatchBriefs; index++ {
		resolved, err := validateAgentBrief(authority, briefs[index])
		if err != nil {
			return config.Resolution{}, fmt.Errorf("brief %d: %w", index, err)
		}
		resolution = resolved
	}
	return resolution, nil
}

// reserveClaudeDispatch stores the pending row of a validated Claude dispatch: the register
// contract of its return and, for a read-only one, the digest of the prompt it was rewritten to.
func reserveClaudeDispatch(ctx context.Context, canonical Canonical, in Invocation, root string, res config.Resolution, prompt string) error {
	if len(canonical.Briefs) != 1 {
		return errors.New("client dispatch must carry exactly one brief")
	}
	store, err := newCorrelationStore(ctx, root, in.CorrelationDir)
	if err != nil {
		return fmt.Errorf("reserve dispatch: %w", err)
	}
	if err := store.reserve(ctx, "claude", canonical.ConversationID, canonical.ToolUseID, res, correlationLaunchDigest(prompt)); err != nil {
		return fmt.Errorf("reserve dispatch: %w", err)
	}
	return nil
}

func evaluateAgentBriefs(ctx context.Context, row Registration, canonical Canonical, root string, in Invocation) Verdict {
	if len(canonical.Briefs) == 0 || len(canonical.Briefs) > MaxDispatchBriefs {
		return trafficDenied(fmt.Errorf("dispatch must carry 1..%d briefs", MaxDispatchBriefs))
	}
	authority, err := loadBriefAuthority(ctx, root)
	if err != nil {
		return trafficDenied(err)
	}
	resolution, err := validateAllBriefs(authority, canonical.Briefs)
	if err != nil {
		return trafficDenied(err)
	}
	if verdict := evaluateBriefClaims(ctx, canonical.Briefs, in.IssueClaims); verdict.Outcome != Allow {
		return verdict
	}
	verdict, prompt, err := readOnlyDelivery(ctx, row.Client, root, canonical)
	if err != nil {
		return trafficDenied(err)
	}
	if row.Client == "claude" {
		if err := reserveClaudeDispatch(ctx, canonical, in, root, resolution, prompt); err != nil {
			return trafficDenied(err)
		}
	}
	return verdict
}

// evaluateDispatchReceipt binds a launched agent to its dispatch, then checks a read-only
// dispatch launched with the rewritten prompt (launchVerdict).
func evaluateDispatchReceipt(ctx context.Context, row Registration, canonical Canonical, root string, in Invocation) Verdict {
	store, err := newCorrelationStore(ctx, root, in.CorrelationDir)
	var pending correlationEntry
	if err == nil {
		pending, err = store.promote(ctx, row.Client, canonical.ConversationID, canonical.ToolUseID, canonical.AgentID)
	}
	if err != nil {
		return trafficDenied(fmt.Errorf("bind dispatch: %w", err))
	}
	return launchVerdict(pending, canonical)
}

// evaluateAgentReturn judges a subagent's final text at SubagentStop. A deny there does not
// block anything: Claude Code and Codex both keep the subagent running and hand it the
// reason as its next instruction. So only a register violation in a Praetor-owned return,
// which the subagent can rewrite, denies; everything it cannot repair is a stated skip.
// A first violation keeps the binding, so the continued subagent's next return is checked
// again. Once a stop hook continued it (stop_hook_active), the binding is released and the
// deny comes back for evaluate's returnBoundary to turn into a stated skip: a kept binding
// would outlive the agent until correlationActiveTTL, and a later resume of a released
// agent is unowned, like any completed one.
func evaluateAgentReturn(ctx context.Context, row Registration, canonical Canonical, root string, in Invocation) Verdict {
	if row.Client == "codex" {
		return codexReturn(canonical)
	}
	store, err := newCorrelationStore(ctx, root, in.CorrelationDir)
	if err != nil {
		return returnNotJudged(err)
	}
	entry, err := store.active(ctx, row.Client, canonical.ConversationID, canonical.AgentID)
	if errors.Is(err, errNoCorrelation) {
		return unownedAgent()
	}
	if err != nil {
		return returnNotJudged(err)
	}
	verdict := Verdict{Outcome: Allow}
	if !entry.HandbackDelivered {
		verdict = validateAgentReturn(entry.Resolution, canonical.Return)
	}
	if verdict.Outcome != Allow && !canonical.StopActive {
		return verdict
	}
	if err := store.complete(ctx, row.Client, canonical.ConversationID, canonical.AgentID); err != nil {
		return returnNotJudged(fmt.Errorf("release agent correlation: %w", err))
	}
	return verdict
}

// codexReturn reports the Codex return boundary: its spawn receipt carries no documented
// key linking the dispatch to the later agent id, and last_assistant_message is nullable.
func codexReturn(canonical Canonical) Verdict {
	if strings.TrimFunc(canonical.Return, isPythonSpace) == "" {
		return Verdict{Outcome: Skip, Reason: "codex return capture unavailable: last_assistant_message is absent"}
	}
	return Verdict{Outcome: Skip, Reason: "codex return register unenforceable: spawn payload has no documented subagent correlation key"}
}

// unownedAgent reports an agent Praetor did not dispatch through a gated brief (a client
// internal agent, an SDK or foreground launch, or a completed or expired binding): its text
// has no stored register contract, so the boundary is unenforceable, never a deny.
func unownedAgent() Verdict {
	return Verdict{Outcome: Skip, Reason: "subagent text register unenforceable: no Praetor-owned dispatch binds this agent"}
}

// returnNotJudged turns a store failure at SubagentStop into a stated skip: the subagent
// cannot repair it, and a deny would only keep it running.
func returnNotJudged(err error) Verdict {
	return Verdict{Outcome: Skip, Reason: "subagent return not judged: " + err.Error()}
}

// briefAuthority is the digest-bound register authority and the routing task vocabulary of
// one governed root, loaded once per dispatch: every brief of a dispatch is judged by the
// same manifest, and the files are read once however many briefs it carries.
type briefAuthority struct {
	register config.RegisterAuthority
	labels   []string
}

func loadBriefAuthority(ctx context.Context, root string) (briefAuthority, error) {
	register, labels, err := config.LoadRegisterTaskAuthority(ctx, root)
	if err != nil {
		return briefAuthority{}, fmt.Errorf("load register policy: %w", err)
	}
	return briefAuthority{register: register, labels: labels}, nil
}

// validateAgentBrief resolves the brief's task through the digest-bound register
// authority, so the resolution carries the manifest SHA-256 that ValidateEmission and the
// stored return contract require. The label must be declared by the routing vocabulary
// that governs root; compile-context validates manifest task rows against the same set.
func validateAgentBrief(authority briefAuthority, text string) (config.Resolution, error) {
	if _, err := caveman.ExtractBriefReadOnly(text); err != nil {
		return config.Resolution{}, err
	}
	task, err := caveman.ExtractBriefTask(text)
	if err != nil {
		return config.Resolution{}, err
	}
	if !router.ValidTaskLabel(task) {
		return config.Resolution{}, fmt.Errorf("invalid task label %q", task)
	}
	if !slices.Contains(authority.labels, task) {
		return config.Resolution{}, fmt.Errorf("task %q is not declared by routing", task)
	}
	resolution, err := authority.register.Resolve(config.SurfaceAgent, task)
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
