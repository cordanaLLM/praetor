package main

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/flavors"
	"github.com/cordanaLLM/praetor/internal/util"
)

// flavorsCommandTimeout bounds the whole flavors command (HISS-02); every git invocation
// below inherits it.
const flavorsCommandTimeout = 2 * time.Minute

// resolveFlavorRef resolves a declared source ref to the commit it points at. Patterns
// such as "refs/tags/v*" resolve to the highest-sorting matching ref. ok is false when
// nothing matches, which the planner turns into ActionUnresolved.
func resolveFlavorRef(ctx context.Context, dir, ref string) (string, bool) {
	if err := util.ValidateExecArg(ref); err != nil {
		return "", false
	}
	if strings.ContainsAny(ref, "*?[") {
		matched, ok := newestMatchingRef(ctx, dir, ref)
		if !ok {
			return "", false
		}
		ref = matched
		if err := util.ValidateExecArg(ref); err != nil {
			return "", false
		}
	}
	// "^{commit}" dereferences annotated tags, so a tag object and a branch head both
	// yield a commit SHA that can be compared for equality.
	out, err := util.RunGit(ctx, dir, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return "", false
	}
	commit := firstOutputLine(out)
	return commit, commit != ""
}

func newestMatchingRef(ctx context.Context, dir, pattern string) (string, bool) {
	out, err := util.RunGit(ctx, dir, "for-each-ref",
		"--sort=-v:refname", "--count=1", "--format=%(refname)", pattern)
	if err != nil {
		return "", false
	}
	matched := firstOutputLine(out)
	return matched, matched != ""
}

func firstOutputLine(out string) string {
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return ""
	}
	line, _, _ := strings.Cut(trimmed, "\n")
	return strings.TrimSpace(line)
}

// fetchCurrentTags maps each declared flavor name to the commit its moving tag currently
// points at. Flavors whose tag does not exist are absent from the map.
func fetchCurrentTags(ctx context.Context, dir string, cfg *flavors.Config) map[string]string {
	names := flavors.FlavorNames(cfg)
	currentTags := make(map[string]string, len(names))
	for i := 0; i < len(names) && i < flavors.MaxFlavors; i++ {
		if commit, ok := resolveFlavorRef(ctx, dir, "refs/tags/"+names[i]); ok {
			currentTags[names[i]] = commit
		}
	}
	return currentTags
}

// flavorSyncOptions selects how `flavors sync` treats a pending flavor and whether it
// publishes the tags it moved.
type flavorSyncOptions struct {
	// Strict fails the whole sync, before any tag moves, when a declared source ref
	// resolves to no commit.
	Strict bool
	// Push publishes every moved tag to Remote in one atomic force-push.
	Push   bool
	Remote string
}

// validateFlavorPlan rejects a plan before any tag is touched. An unusable tag name always
// aborts. An unresolved source ref aborts only under strict: by default that flavor is
// pending, because its source can legitimately not exist yet (no v* release has been cut,
// no lts-* branch has been opened), and a pending flavor is never retargeted. Letting it
// abort the sync instead froze every flavor that could move (#205).
func validateFlavorPlan(transitions []flavors.TagTransition, strict bool) error {
	for i := 0; i < len(transitions) && i < flavors.MaxFlavors; i++ {
		tr := transitions[i]
		if strict && tr.Action == flavors.ActionUnresolved {
			return fmt.Errorf("flavor %q: source ref %q resolves to no commit; refusing to sync under --strict",
				tr.FlavorName, tr.TargetRef)
		}
		if err := util.ValidateExecArg(tr.FlavorName); err != nil {
			return fmt.Errorf("flavor %q is not a usable tag name: %w", tr.FlavorName, err)
		}
	}
	return nil
}

// applyFlavorTransitions moves the local tag of every flavor whose source resolved to a
// different commit and returns the names it moved. A pending flavor keeps its tag.
func applyFlavorTransitions(ctx context.Context, dir string, transitions []flavors.TagTransition, strict bool) ([]string, error) {
	if err := validateFlavorPlan(transitions, strict); err != nil {
		return nil, err
	}

	moved := make([]string, 0, len(transitions))
	pending := 0
	for i := 0; i < len(transitions) && i < flavors.MaxFlavors; i++ {
		tr := transitions[i]
		if tr.Action == flavors.ActionUnresolved {
			pending++
			fmt.Printf("Pending %s: source %s resolves to no commit; tag left untouched\n", tr.FlavorName, tr.TargetRef)
			continue
		}
		if tr.Action != flavors.ActionCreate && tr.Action != flavors.ActionUpdate {
			continue
		}
		// --no-sign keeps the moving tag lightweight. With tag.gpgSign=true, as on a
		// workstation that signs its release tags, a bare `git tag -f` becomes a signed
		// annotated tag that needs a message, and the sync fails.
		out, err := util.RunGit(ctx, dir, "tag", "--no-sign", "-f", tr.FlavorName, tr.TargetCommit)
		if err != nil {
			return moved, fmt.Errorf("failed to apply flavor tag %s: %w (%s)", tr.FlavorName, err, out)
		}
		fmt.Printf("Updated tag %s -> %s (%s)\n", tr.FlavorName, tr.TargetCommit, tr.TargetRef)
		moved = append(moved, tr.FlavorName)
	}
	fmt.Printf("Flavors synchronized: %d tag(s) moved, %d already current, %d pending.\n",
		len(moved), len(transitions)-len(moved)-pending, pending)
	return moved, nil
}

// pushFlavorTags publishes the moved tags in one atomic force-push, so the remote takes
// every moved flavor or none. The set pushed is the set the reconciler moved, which comes
// from the flavors config rather than from tag names repeated in workflow YAML.
func pushFlavorTags(ctx context.Context, dir, remote string, moved []string) error {
	if len(moved) == 0 {
		fmt.Println("Nothing to publish: no flavor tag moved.")
		return nil
	}
	if err := util.ValidateExecArg(remote); err != nil {
		return fmt.Errorf("remote %q is not usable: %w", remote, err)
	}
	args := []string{"push", "--atomic", "--force", remote}
	for i := 0; i < len(moved) && i < flavors.MaxFlavors; i++ {
		args = append(args, "refs/tags/"+moved[i])
	}
	out, err := util.RunGit(ctx, dir, args...)
	if err != nil {
		return fmt.Errorf("failed to publish flavor tags to %s: %w (%s)", remote, err, out)
	}
	fmt.Printf("Published %d flavor tag(s) to %s: %s\n", len(moved), remote, strings.Join(moved, ", "))
	return nil
}

func syncFlavors(ctx context.Context, dir string, transitions []flavors.TagTransition, opts flavorSyncOptions) error {
	moved, err := applyFlavorTransitions(ctx, dir, transitions, opts.Strict)
	if err != nil {
		return err
	}
	if !opts.Push {
		return nil
	}
	return pushFlavorTags(ctx, dir, opts.Remote, moved)
}

func printFlavorPlan(transitions []flavors.TagTransition) {
	fmt.Println("=== cordanaLLM/praetor Release Flavor Reconciler ===")
	for i := 0; i < len(transitions) && i < flavors.MaxFlavors; i++ {
		tr := transitions[i]
		current := tr.CurrentRef
		if current == "" {
			current = "<absent>"
		}
		target := tr.TargetCommit
		if target == "" {
			target = "<unresolved>"
		}
		fmt.Printf("  [%s] %-10s : %s -> %s (source: %s)\n",
			strings.ToUpper(tr.Action), tr.FlavorName, current, target, tr.TargetRef)
	}
}

func runFlavors(args []string) error {
	fs := flag.NewFlagSet("flavors", flag.ContinueOnError)
	configPath := fs.String("config", ".config/flavors.yaml", "Path to flavors configuration")
	dir := fs.String("dir", ".", "Repository root directory")
	var opts flavorSyncOptions
	fs.BoolVar(&opts.Strict, "strict", false, "sync: fail before moving any tag when a declared source ref resolves to no commit")
	fs.BoolVar(&opts.Push, "push", false, "sync: publish the moved tags to --remote in one atomic force-push")
	fs.StringVar(&opts.Remote, "remote", "origin", "sync: remote that --push publishes to")

	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	action := positionalAt(positional, 0, "plan")

	ctx, cancel := context.WithTimeout(context.Background(), flavorsCommandTimeout)
	defer cancel()

	cfg, err := flavors.LoadConfigContext(ctx, *configPath)
	if err != nil {
		return fmt.Errorf("failed to load flavors config: %w", err)
	}

	resolve := func(ref string) (string, bool) { return resolveFlavorRef(ctx, *dir, ref) }
	transitions := flavors.PlanTransitions(cfg, fetchCurrentTags(ctx, *dir, cfg), resolve)
	printFlavorPlan(transitions)

	switch action {
	case "plan", "list":
		fmt.Println("\nRun 'praetorctl flavors sync' to apply tag updates.")
		return nil
	case "sync":
		return syncFlavors(ctx, *dir, transitions, opts)
	default:
		return fmt.Errorf("unknown flavors action: %s (supported: plan, list, sync)", action)
	}
}
