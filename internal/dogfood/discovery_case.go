package dogfood

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

func runDiscoveryCase(ctx context.Context, opts DiscoveryOptions, policy DiscoveryPolicy, item *DiscoveryCase) {
	item.Status = "failed"
	if err := ctx.Err(); err != nil {
		item.Status, item.Error = "skipped_due_to_context", err.Error()
		return
	}
	dir := filepath.Join(opts.ArtifactDir, item.ID)
	item.ReportPath = filepath.ToSlash(filepath.Join(item.ID, "report.json"))
	if err := util.MkdirSecure(dir, 0o700); err != nil {
		item.Error = err.Error()
		return
	}
	if err := observeDiscoveryCase(ctx, opts, policy, dir, item); err != nil {
		item.Error = err.Error()
	}
	payload := struct {
		Case      *DiscoveryCase       `json:"case"`
		Discovery *CapabilityDiscovery `json:"discovery,omitempty"`
	}{item, item.Discovery}
	if err := savePublicJSON(filepath.Join(dir, "report.json"), payload); err != nil {
		item.Status, item.Error = "failed", fmt.Sprintf("%s; write case evidence: %v", item.Error, err)
	}
}

func observeDiscoveryCase(ctx context.Context, opts DiscoveryOptions, policy DiscoveryPolicy, dir string, item *DiscoveryCase) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	if err := ctx.Err(); err != nil {
		item.Status = "skipped_due_to_context"
		return err
	}
	root := opts.Path
	if root == "" {
		var err error
		ctx, err = publicCommandContext(ctx, dir)
		if err != nil {
			return err
		}
		root = filepath.Join(dir, "checkout")
		item.SourceSHA, err = clonePublicSource(ctx, publicSource{url: item.Repository, sha: item.RequestedSHA}, root)
		if err != nil {
			return err
		}
	}
	opened, err := openSuiteDirectory(ctx, root)
	if err != nil {
		return err
	}
	if err := opened.Close(); err != nil {
		return err
	}
	item.Discovery, err = ObserveCapabilities(ctx, root, policy)
	return finishDiscoveryCase(ctx, root, item, err)
}

func finishDiscoveryCase(ctx context.Context, root string, item *DiscoveryCase, observeErr error) error {
	if item.Discovery == nil {
		return observeErr
	}
	item.TreeSHA256 = item.Discovery.TreeSHA256
	after, snapshotErr := snapshotDiscoveryTree(ctx, root)
	if snapshotErr != nil {
		return errors.Join(observeErr, snapshotErr)
	}
	if after.digest() != item.TreeSHA256 {
		item.Discovery = nil
		return errors.New("source changed during capability observation; no candidate admitted")
	}
	if observeErr != nil {
		item.Status = "partial"
		return observeErr
	}
	if item.Discovery.Status != "observed" {
		item.Status = "partial"
		return errors.New("capability observation is incomplete")
	}
	item.Status = "observed"
	return nil
}

func aggregateDiscovery(cases []DiscoveryCase) []DiscoveryCandidate {
	byKey := make(map[string]*DiscoveryCandidate)
	for i := 0; i < len(cases) && i < MaxDiscoveryRepositories; i++ {
		item := &cases[i]
		if item.Discovery == nil || item.TreeSHA256 == "" || item.Status != "observed" {
			continue
		}
		addDiscoveryCandidates(byKey, item)
	}
	result := make([]DiscoveryCandidate, 0, len(byKey))
	for _, candidate := range byKey {
		result = append(result, *candidate)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].RepositoryCount != result[j].RepositoryCount {
			return result[i].RepositoryCount > result[j].RepositoryCount
		}
		return result[i].Key < result[j].Key
	})
	return result
}

func addDiscoveryCandidates(byKey map[string]*DiscoveryCandidate, item *DiscoveryCase) {
	for j := 0; j < len(item.Discovery.Observations) && j < 128; j++ {
		observation := item.Discovery.Observations[j]
		if observation.Status != "unsupported" {
			continue
		}
		candidate := byKey[observation.Key]
		if candidate == nil {
			candidate = &DiscoveryCandidate{Key: observation.Key, Title: observation.Title, Kind: observation.Kind, Status: "candidate"}
			byKey[observation.Key] = candidate
		}
		candidate.RepositoryCount++
		candidate.Occurrences = append(candidate.Occurrences, DiscoveryOccurrence{CaseID: item.ID, Repository: item.Repository,
			SourceSHA: item.SourceSHA, TreeSHA256: item.TreeSHA256, ReportPath: item.ReportPath, EvidenceCount: observation.EvidenceCount})
	}
}
