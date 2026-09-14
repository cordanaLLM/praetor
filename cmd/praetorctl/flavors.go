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

// validateFlavorPlan rejects a plan before any tag is touched, so a sync either applies
// every transition or none: a partially applied sync would leave some release pointers
// moved and others stale with no record of which.
func validateFlavorPlan(transitions []flavors.TagTransition) error {
	for i := 0; i < len(transitions) && i < flavors.MaxFlavors; i++ {
		tr := transitions[i]
		if tr.Action == flavors.ActionUnresolved {
			return fmt.Errorf("flavor %q: source ref %q resolves to no commit; refusing to retarget the moving tag",
				tr.FlavorName, tr.TargetRef)
		}
		if err := util.ValidateExecArg(tr.FlavorName); err != nil {
			return fmt.Errorf("flavor %q is not a usable tag name: %w", tr.FlavorName, err)
		}
	}
	return nil
}

func applyFlavorTransitions(ctx context.Context, dir string, transitions []flavors.TagTransition) error {
	if err := validateFlavorPlan(transitions); err != nil {
		return err
	}

	applied := 0
	for i := 0; i < len(transitions) && i < flavors.MaxFlavors; i++ {
		tr := transitions[i]
		if tr.Action != flavors.ActionCreate && tr.Action != flavors.ActionUpdate {
			continue
		}
		out, err := util.RunGit(ctx, dir, "tag", "-f", tr.FlavorName, tr.TargetCommit)
		if err != nil {
			return fmt.Errorf("failed to apply flavor tag %s: %w (%s)", tr.FlavorName, err, out)
		}
		fmt.Printf("Updated tag %s -> %s (%s)\n", tr.FlavorName, tr.TargetCommit, tr.TargetRef)
		applied++
	}
	fmt.Printf("Flavors synchronized: %d tag(s) moved, %d already current.\n", applied, len(transitions)-applied)
	return nil
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
		return applyFlavorTransitions(ctx, *dir, transitions)
	default:
		return fmt.Errorf("unknown flavors action: %s (supported: plan, list, sync)", action)
	}
}
