package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// MaxRepositoryTopics is GitHub's limit on topics per repository ("Add no more than 20
	// topics"), and the bound of every topic loop here (HISS-02).
	MaxRepositoryTopics = 20
	// maxTopicLength is GitHub's limit on one topic name ("Use 50 characters or less").
	maxTopicLength = 50
)

// RepositoryMetadataReport says what ReconcileRepositoryMetadata wrote to the forge and what it
// left for the operator.
type RepositoryMetadataReport struct {
	// Updated names the repository fields written, in the order "description", "homepage".
	Updated []string
	// AddedTopics lists the declared topics the repository lacked and now carries.
	AddedTopics []string
	// VisibilityDrift is empty when the declared visibility matches the repository's, or none
	// is declared, and otherwise states both. Visibility is reported and never written: making
	// a repository public or private is a decision for the operator, not for a sync.
	VisibilityDrift string
}

// ghRepositoryMetadata is the part of GitHub's repository object this reconciliation reads.
type ghRepositoryMetadata struct {
	Description string   `json:"description"`
	Homepage    string   `json:"homepage"`
	Visibility  string   `json:"visibility"`
	Topics      []string `json:"topics"`
}

// ReconcileRepositoryMetadata converges the repository's description, homepage and topics on
// what the manifest declares, and reports visibility drift without changing it.
//
// The manifest is the operator's configuration, so a field it leaves unset is not an
// instruction to clear the forge's value: an empty description or homepage is not written, and
// topics are only ever added. A topic the repository carries and the manifest does not name is
// kept, the way labels outside .config/labels.yaml are, so an empty topic list never wipes the
// repository's topics. Every write is checked against the state GitHub returns for it.
//
// Every refusal that can be decided up front is decided before the first write: an invalid
// topic before the repository is read, and a topic union past GitHub's limit right after the
// read, so a run that fails there has changed nothing. A topic write GitHub rejects after the
// description or homepage was written names those fields in its error.
func (g *GitHubDriver) ReconcileRepositoryMetadata(ctx context.Context, declared config.RepositoryMetadata) (*RepositoryMetadataReport, error) {
	if err := g.Authenticate(ctx); err != nil {
		return nil, err
	}
	topics, err := normalizeTopics(declared.Topics)
	if err != nil {
		return nil, err
	}
	live, err := g.getRepositoryMetadata(ctx)
	if err != nil {
		return nil, err
	}
	missing, err := missingTopics(topics, live.Topics)
	if err != nil {
		return nil, err
	}
	report := &RepositoryMetadataReport{VisibilityDrift: visibilityDrift(declared.Visibility, live.Visibility)}
	if report.Updated, err = g.patchRepositoryFields(ctx, declared, live); err != nil {
		return report, err
	}
	if err := g.addTopics(ctx, live.Topics, missing); err != nil {
		return report, afterFieldWrites(err, report.Updated)
	}
	report.AddedTopics = missing
	return report, nil
}

// ValidateRepositoryTopics reports a declared topic GitHub would refuse, or more topics than it
// accepts. A --remote sync calls it before its first forge write, so a manifest GitHub would
// reject leaves the ruleset and labels untouched too; ReconcileRepositoryMetadata applies the
// same check again for any other caller.
func ValidateRepositoryTopics(declared []string) error {
	_, err := normalizeTopics(declared)
	return err
}

// afterFieldWrites adds the repository fields already written to err, so an operator whose
// sync fails on the topic write still learns the description or homepage changed.
func afterFieldWrites(err error, written []string) error {
	if len(written) == 0 {
		return err
	}
	return fmt.Errorf("%w (repository %s already written)", err, strings.Join(written, " and "))
}

// normalizeTopics lower-cases the declared topics, as GitHub stores them, drops duplicates and
// rejects a topic GitHub would refuse, so nothing is written for a manifest the forge rejects.
func normalizeTopics(declared []string) ([]string, error) {
	if len(declared) > MaxRepositoryTopics {
		return nil, fmt.Errorf("manifest repository.topics declares %d topics; GitHub accepts at most %d", len(declared), MaxRepositoryTopics)
	}
	topics := make([]string, 0, len(declared))
	for i := 0; i < len(declared) && i < MaxRepositoryTopics; i++ {
		topic := strings.ToLower(strings.TrimSpace(declared[i]))
		if err := validateTopic(topic); err != nil {
			return nil, fmt.Errorf("manifest repository.topics[%d] %q: %w", i, declared[i], err)
		}
		if !containsFold(topics, topic) {
			topics = append(topics, topic)
		}
	}
	return topics, nil
}

// validateTopic applies GitHub's topic rules: lowercase letters, numbers and hyphens, starting
// with a letter or number, at most 50 characters.
func validateTopic(topic string) error {
	if topic == "" {
		return fmt.Errorf("topic is empty")
	}
	if len(topic) > maxTopicLength {
		return fmt.Errorf("topic is longer than %d characters", maxTopicLength)
	}
	if topic[0] == '-' {
		return fmt.Errorf("topic must start with a letter or number")
	}
	for i := 0; i < len(topic) && i < maxTopicLength; i++ {
		if !isTopicByte(topic[i]) {
			return fmt.Errorf("topic may hold only lowercase letters, numbers and hyphens")
		}
	}
	return nil
}

// isTopicByte reports whether c may appear in a GitHub topic: a lowercase ASCII letter, a digit
// or a hyphen.
func isTopicByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-'
}

// visibilityDrift describes a declared visibility the repository does not have, or returns ""
// when they agree or nothing is declared.
func visibilityDrift(declared, live string) string {
	want := strings.ToLower(strings.TrimSpace(declared))
	if want == "" || strings.EqualFold(want, live) {
		return ""
	}
	if live == "" {
		live = "no visibility"
	}
	return fmt.Sprintf("manifest declares %s, GitHub reports %s", want, live)
}

// getRepositoryMetadata reads the repository object.
func (g *GitHubDriver) getRepositoryMetadata(ctx context.Context) (ghRepositoryMetadata, error) {
	var live ghRepositoryMetadata
	path, err := g.repoPath("")
	if err != nil {
		return live, fmt.Errorf("read repository metadata: %w", err)
	}
	body, status, err := g.sendRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return live, fmt.Errorf("read repository metadata: %w", err)
	}
	if status != http.StatusOK {
		return live, fmt.Errorf("unexpected status %d reading repository metadata: %s", status, util.BodyPreview(body))
	}
	if err := json.Unmarshal(body, &live); err != nil {
		return live, fmt.Errorf("parse repository metadata (raw: %q): %w", util.BodyPreview(body), err)
	}
	return live, nil
}

// patchRepositoryFields writes the declared description and homepage that differ from the
// repository's, and returns the names of the fields it wrote. A field the manifest leaves empty
// is not written.
func (g *GitHubDriver) patchRepositoryFields(ctx context.Context, declared config.RepositoryMetadata, live ghRepositoryMetadata) ([]string, error) {
	payload := map[string]string{}
	var updated []string
	for _, field := range [...]struct{ name, want, have string }{
		{"description", strings.TrimSpace(declared.Description), live.Description},
		{"homepage", strings.TrimSpace(declared.Homepage), live.Homepage},
	} {
		if field.want != "" && field.want != field.have {
			payload[field.name] = field.want
			updated = append(updated, field.name)
		}
	}
	if len(updated) == 0 {
		return nil, nil
	}
	written, err := g.sendRepositoryPatch(ctx, payload)
	if err != nil {
		return nil, err
	}
	if written.Description != payloadOr(payload, "description", written.Description) ||
		written.Homepage != payloadOr(payload, "homepage", written.Homepage) {
		return nil, fmt.Errorf("repository metadata readback differs from what was written: description %q, homepage %q",
			written.Description, written.Homepage)
	}
	return updated, nil
}

// payloadOr returns payload[key], or fallback when the payload does not set key.
func payloadOr(payload map[string]string, key, fallback string) string {
	if value, ok := payload[key]; ok {
		return value
	}
	return fallback
}

// sendRepositoryPatch sends one repository update and returns the repository GitHub answers with.
func (g *GitHubDriver) sendRepositoryPatch(ctx context.Context, payload map[string]string) (ghRepositoryMetadata, error) {
	var written ghRepositoryMetadata
	path, err := g.repoPath("")
	if err != nil {
		return written, fmt.Errorf("update repository metadata: %w", err)
	}
	body, status, err := g.sendRequest(ctx, http.MethodPatch, path, payload)
	if err != nil {
		return written, fmt.Errorf("update repository metadata: %w", err)
	}
	if status != http.StatusOK {
		return written, fmt.Errorf("unexpected status %d updating repository metadata: %s", status, util.BodyPreview(body))
	}
	if err := json.Unmarshal(body, &written); err != nil {
		return written, fmt.Errorf("parse updated repository metadata (raw: %q): %w", util.BodyPreview(body), err)
	}
	return written, nil
}

// missingTopics returns the declared topics the repository lacks. It fails when adding them to
// the topics the repository keeps would exceed GitHub's limit, which ReconcileRepositoryMetadata
// checks before its first write so the refusal leaves the repository unchanged.
func missingTopics(declared, live []string) ([]string, error) {
	var missing []string
	for i := 0; i < len(declared) && i < MaxRepositoryTopics; i++ {
		if !containsFold(live, declared[i]) {
			missing = append(missing, declared[i])
		}
	}
	if len(missing) > 0 && len(live)+len(missing) > MaxRepositoryTopics {
		return nil, fmt.Errorf("adding %d declared topics to the repository's %d would exceed GitHub's limit of %d; nothing was written",
			len(missing), len(live), MaxRepositoryTopics)
	}
	return missing, nil
}

// addTopics adds missing to the repository's live topics, keeping every topic it has. Nothing
// is written when none is missing.
func (g *GitHubDriver) addTopics(ctx context.Context, live, missing []string) error {
	if len(missing) == 0 {
		return nil
	}
	return g.putTopics(ctx, append(append(make([]string, 0, len(live)+len(missing)), live...), missing...))
}

// putTopics replaces the repository's topics with names and checks GitHub's answer holds them.
func (g *GitHubDriver) putTopics(ctx context.Context, names []string) error {
	path, err := g.repoPath("topics")
	if err != nil {
		return fmt.Errorf("update repository topics: %w", err)
	}
	body, status, err := g.sendRequest(ctx, http.MethodPut, path, map[string][]string{"names": names})
	if err != nil {
		return fmt.Errorf("update repository topics: %w", err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("unexpected status %d updating repository topics: %s", status, util.BodyPreview(body))
	}
	var written struct {
		Names []string `json:"names"`
	}
	if err := json.Unmarshal(body, &written); err != nil {
		return fmt.Errorf("parse updated repository topics (raw: %q): %w", util.BodyPreview(body), err)
	}
	for i := 0; i < len(names) && i < MaxRepositoryTopics; i++ {
		if !containsFold(written.Names, names[i]) {
			return fmt.Errorf("repository topics readback lacks %q: %v", names[i], written.Names)
		}
	}
	return nil
}

// containsFold reports whether haystack holds needle, ignoring case: GitHub stores topics in
// lower case.
func containsFold(haystack []string, needle string) bool {
	for i := 0; i < len(haystack) && i < MaxRepositoryTopics; i++ {
		if strings.EqualFold(haystack[i], needle) {
			return true
		}
	}
	return false
}
