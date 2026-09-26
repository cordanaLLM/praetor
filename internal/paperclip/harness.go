package paperclip

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	manifestFile = ".standards.yaml"
	paperclipDir = ".paperclip"
	harnessFile  = "harness.json"
	rulesFile    = "rules.md"
	// filePerm is the mode of the written harness files.
	filePerm os.FileMode = 0o644
	// dirPerm is the mode of the .paperclip directory.
	dirPerm os.FileMode = 0o755
	// maxHarnessValues bounds the contract and invariant lines of one harness.
	maxHarnessValues = 64
	// maxHarnessValueBytes bounds the length of one harness value.
	maxHarnessValueBytes = 4096
	// markdownLineLimit is markdownlint's default MD013 line length.
	markdownLineLimit = 80
)

// agitPushFormat is the push protocol every synthesized harness prescribes. The AGit push opens
// the review; it updates no local ref, so the second push of the same commit to a review branch
// records a remote-tracking ref that VerifyRun reads as local proof HEAD left the machine. The
// explicit destination never pushes a local main to the remote main, whatever branch is checked
// out.
const agitPushFormat = "git push origin HEAD:refs/for/main -o topic=<issue-id> && " +
	"git push origin HEAD:refs/heads/paperclip/<issue-id>"

// Harness represents the Paperclip agent runtime configuration.
type Harness struct {
	Version           int      `json:"version"`
	Platform          string   `json:"platform"`
	OperatingContract []string `json:"operating_contract"`
	AGitPushFormat    string   `json:"agit_push_format"`
	Invariants        []string `json:"invariants"`
}

// SynthesizeHarness generates a Paperclip agent harness embedding fleet contracts. The
// repository identity lookup (a git subprocess) runs under the caller's context. The
// platform is the identity .standards.yaml declares, else the origin remote's; with neither
// the error wraps util.ErrRepoIdentityUnresolved and no harness is returned, because the
// platform names a repository and none may be guessed.
func SynthesizeHarness(ctx context.Context, repoPath string) (*Harness, error) {
	if ctx == nil {
		return nil, fmt.Errorf("paperclip: context cannot be nil")
	}
	platform, err := resolvePlatform(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	contract := []string{
		"Branch push != shipping. Open PR required. Work ships after merge.",
		"Rebase onto main immediately: run git fetch origin && git rebase origin/main before proposing.",
		"Rule 0 Terminal Disposition: every run ends with structured disposition: in_review or blocked.",
		"Ed25519 Exit-0 Receipts: attach cryptographic execution receipts to all PR proposals.",
		"Timeout != failure. Re-check open PRs before retry; prevent duplicate PRs.",
		// A Paperclip run reports to an orchestrating agent, so its product is internal text.
		config.RegisterDirective(config.TextRegisterInternal),
	}

	invariants := []string{
		"HISS-01: Acyclic DAG control flow (no recursion)",
		"HISS-02: Scalar upper bounds on all loops; context timeout on all input and output",
		"HISS-04: McCabe Cyclomatic <= 10, Cognitive <= 15, Func LOC <= 75",
		"HISS-07: Zero .unwrap() / .expect(); all errors handled or wrapped",
		"HISS-10: Zero-warning tolerance across compiler, linters, and formatters",
		"HISS-15: 3D testing mandatory (Positive, Negative, Boundary >= 2 checks/dim)",
		"HISS-16: Canonical AGENTS.md compiled to vendor harnesses",
	}
	return &Harness{
		Version:           1,
		Platform:          platform,
		OperatingContract: contract,
		AGitPushFormat:    agitPushFormat,
		Invariants:        invariants,
	}, nil
}

// priorOperatingContract, priorRegisterDirectives and priorInvariants are the texts every
// earlier release synthesized, from 5d08985f until the contract moved to Caveman. The
// directive row was absent before the text register (#204) and changed form with the caveman
// skill (#225). They are literals, not calls, so a later change to the current text cannot
// silently rewrite what "earlier output" means.
var (
	priorOperatingContract = []string{
		"Pushing a branch is NOT shipping: an open PR is required, but still not shipped work until merged.",
		"Rebase onto main immediately: run git fetch origin && git rebase origin/main before proposing.",
		"Rule 0 Terminal Disposition: every run must end with a structured disposition (in_review or blocked).",
		"Ed25519 Exit-0 Receipts: attach cryptographic execution receipts to all PR proposals.",
		"Timeout Resilience: timeout is not failure; re-check open PRs before retrying to prevent duplicate PRs.",
	}
	priorRegisterDirectives = []string{
		"",
		"Text register internal: telegraphic: no filler, no preamble, no restatement; facts, paths, commands, verdict.",
		"Text register internal: `caveman` skill: fragments, no filler, verbatim code/paths/errors; facts, paths, commands, verdict.",
	}
	priorInvariants = []string{
		"HISS-01: Acyclic DAG control flow (no recursion)",
		"HISS-02: Scalar upper bounds on all loops; context timeout on all I/O",
		"HISS-04: McCabe Cyclomatic <= 10, Cognitive <= 15, Func LOC <= 75",
		"HISS-07: Zero .unwrap() / .expect(); all errors handled or wrapped",
		"HISS-10: Zero-warning tolerance across compiler, linters, and formatters",
		"HISS-15: 3D testing mandatory (Positive, Negative, Boundary >= 2 checks/dim)",
		"HISS-16: Canonical AGENTS.md compiled to vendor harnesses",
	}
)

// PriorGenerated reports whether the harness under repoPath is unmodified output of an
// earlier release for current's identity: harness.json byte-identical to one earlier
// synthesis, and rules.md absent or equal to that synthesis's rendering. Adoption refreshes
// only such a harness; an edited one is operator-owned and stays byte for byte.
func PriorGenerated(ctx context.Context, repoPath string, current *Harness) (bool, error) {
	if ctx == nil || current == nil {
		return false, fmt.Errorf("paperclip: prior harness check requires context and current harness")
	}
	harnessData, rulesData, rulesExist, err := readHarnessFiles(ctx, repoPath)
	if err != nil {
		return false, err
	}
	for index := 0; index < len(priorRegisterDirectives); index++ {
		prior := priorHarness(current, priorRegisterDirectives[index])
		rendered, err := MarshalHarness(&prior)
		if err != nil {
			return false, err
		}
		if bytes.Equal(rendered, harnessData) {
			return !rulesExist || string(rulesData) == renderRules(&prior), nil
		}
	}
	return false, nil
}

func priorHarness(current *Harness, directive string) Harness {
	prior := *current
	prior.OperatingContract = append([]string(nil), priorOperatingContract...)
	if directive != "" {
		prior.OperatingContract = append(prior.OperatingContract, directive)
	}
	prior.Invariants = priorInvariants
	return prior
}

func readHarnessFiles(ctx context.Context, repoPath string) ([]byte, []byte, bool, error) {
	jsonPath, err := util.ConfinePath(repoPath, filepath.Join(paperclipDir, harnessFile))
	if err != nil {
		return nil, nil, false, fmt.Errorf("resolve %s: %w", harnessFile, err)
	}
	harnessData, err := contextopt.ReadSnapshot(ctx, jsonPath)
	if err != nil {
		return nil, nil, false, fmt.Errorf("read %s: %w", harnessFile, err)
	}
	mdPath, err := util.ConfinePath(repoPath, filepath.Join(paperclipDir, rulesFile))
	if err != nil {
		return nil, nil, false, fmt.Errorf("resolve %s: %w", rulesFile, err)
	}
	rulesData, rulesExist, err := contextopt.ObserveSnapshot(ctx, mdPath)
	if err != nil {
		return nil, nil, false, fmt.Errorf("read %s: %w", rulesFile, err)
	}
	return harnessData, rulesData, rulesExist, nil
}

// resolvePlatform derives owner/name from the manifest, then the origin remote
// (util.ResolveRemoteIdentity). It never reads the checkout path and never substitutes a
// default owner: a parent directory names wherever the checkout sits, not its owner. A
// manifest that exists but cannot be read is an error rather than a reason to fall back.
func resolvePlatform(ctx context.Context, repoPath string) (string, error) {
	platform, ok, err := manifestPlatform(ctx, repoPath)
	if err != nil || ok {
		return platform, err
	}
	owner, repo, err := util.ResolveRemoteIdentity(ctx, repoPath)
	if err != nil {
		return "", fmt.Errorf("paperclip: harness platform needs repository.owner and repository.name in %s or an origin remote: %w",
			manifestFile, err)
	}
	return owner + "/" + repo, nil
}

// manifestPlatform reads owner/name from .standards.yaml when present and complete. The read
// is config.ReadYAMLDocument, bounded to a regular file and a single document (BUG-857). It
// ignores the cancellation of ctx, which governs only the git lookup.
func manifestPlatform(ctx context.Context, repoPath string) (string, bool, error) {
	var m struct {
		Repository struct {
			Owner string `yaml:"owner"`
			Name  string `yaml:"name"`
		} `yaml:"repository"`
	}
	path := filepath.Join(repoPath, manifestFile)
	err := config.ReadYAMLDocument(context.WithoutCancel(ctx), path, &m, util.YAMLDocumentOptions{AllowEmpty: true})
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("paperclip: repository identity: %w", err)
	}
	if m.Repository.Owner == "" || m.Repository.Name == "" {
		return "", false, nil
	}
	return fmt.Sprintf("%s/%s", m.Repository.Owner, m.Repository.Name), true, nil
}

// MarshalHarness renders the canonical bytes written to .paperclip/harness.json. Coverage
// digests and the writer share this serializer so line-bound provenance cannot drift.
func MarshalHarness(h *Harness) ([]byte, error) {
	if h == nil {
		return nil, fmt.Errorf("paperclip: harness cannot be nil")
	}
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal harness: %w", err)
	}
	return append(data, '\n'), nil
}

// WriteHarness writes .paperclip/harness.json and .paperclip/rules.md into repoPath.
// Both targets are confined to repoPath so a symlinked .paperclip cannot redirect them.
func WriteHarness(h *Harness, repoPath string) error {
	data, err := MarshalHarness(h)
	if err != nil {
		return err
	}
	dir, err := util.ConfinePath(repoPath, paperclipDir)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", paperclipDir, err)
	}
	if err := util.MkdirSecure(dir, dirPerm); err != nil {
		return fmt.Errorf("create %s dir: %w", paperclipDir, err)
	}

	jsonPath, err := util.ConfinePath(repoPath, filepath.Join(paperclipDir, harnessFile))
	if err != nil {
		return fmt.Errorf("resolve %s: %w", harnessFile, err)
	}
	if err := util.WriteFileSecure(jsonPath, data, filePerm); err != nil {
		return fmt.Errorf("write %s: %w", jsonPath, err)
	}

	mdPath, err := util.ConfinePath(repoPath, filepath.Join(paperclipDir, rulesFile))
	if err != nil {
		return fmt.Errorf("resolve %s: %w", rulesFile, err)
	}
	if err := util.WriteFileSecure(mdPath, []byte(renderRules(h)), filePerm); err != nil {
		return fmt.Errorf("write %s: %w", mdPath, err)
	}
	return nil
}

// renderRules renders the human-readable operating rules of a harness.
//
// The file is Markdown an adopter's own lint reads, so it is written to pass markdownlint's
// default configuration: every heading, list and fence stands between blank lines (MD022,
// MD031, MD032), and list items are wrapped within markdownLineLimit (MD013) instead of
// running to the full length a contract line may have. A push command too long to wrap
// gets a disable scoped to its fence, never one covering the file (BUG-806).
func renderRules(h *Harness) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Paperclip Operating Rules (%s)\n\n## Operating Contract\n\n", h.Platform)
	writeListItems(&b, h.OperatingContract)
	b.WriteString("\n## AGit Push Protocol\n\n")
	writeCommandFence(&b, h.AGitPushFormat)
	b.WriteString("\n## High-Integrity Invariants\n\n")
	writeListItems(&b, h.Invariants)
	return b.String()
}

// writeListItems writes each value as one wrapped list item.
func writeListItems(b *strings.Builder, values []string) {
	for i := 0; i < len(values) && i < maxHarnessValues; i++ {
		b.WriteString(wrapListItem(values[i]))
	}
}

// writeCommandFence writes command as a bash fence. A command line cannot be wrapped
// without changing it, so a line over markdownLineLimit gets an MD013 disable scoped to the
// fence alone.
func writeCommandFence(b *strings.Builder, command string) {
	long := false
	lines := strings.Split(command, "\n")
	for i := 0; i < len(lines) && i < maxHarnessValueBytes; i++ {
		long = long || len(lines[i]) > markdownLineLimit
	}
	if long {
		b.WriteString("<!-- markdownlint-disable MD013 -->\n\n")
	}
	fmt.Fprintf(b, "```bash\n%s\n```\n", command)
	if long {
		b.WriteString("\n<!-- markdownlint-enable MD013 -->\n")
	}
}

// wrapListItem renders text as one "- " list item whose lines stay within
// markdownLineLimit, breaking at spaces; continuation lines are indented two spaces so they
// stay inside the item. A word longer than the limit keeps a line of its own, which
// markdownlint allows because no whitespace follows the limit.
func wrapListItem(text string) string {
	chunks := unbreakableChunks(strings.Fields(text))
	var b strings.Builder
	line, count := "-", 0
	for i := 0; i < len(chunks) && i < maxHarnessValueBytes; i++ {
		if count > 0 && len(line)+1+len(chunks[i]) > markdownLineLimit {
			b.WriteString(line + "\n")
			line, count = " ", 0
		}
		line += " " + chunks[i]
		count++
	}
	b.WriteString(line + "\n")
	return b.String()
}

// unbreakableChunks joins every word Markdown could read as a heading, list marker or
// quote at the start of a line onto the word before it, so wrapping never starts a
// continuation line with one and cannot turn part of an item into a new block.
func unbreakableChunks(words []string) []string {
	chunks := make([]string, 0, len(words))
	for i := 0; i < len(words) && i < maxHarnessValueBytes; i++ {
		if len(chunks) > 0 && !safeLineStart(words[i]) {
			chunks[len(chunks)-1] += " " + words[i]
			continue
		}
		chunks = append(chunks, words[i])
	}
	return chunks
}

// safeLineStart reports whether a continuation line may begin with word without Markdown
// reading it as a heading (ATX, or a setext underline of "=" alone), a bullet or ordered
// list marker, a block quote, a code fence or an HTML block.
func safeLineStart(word string) bool {
	if strings.ContainsAny(word[:1], "#>-+*<") || strings.Trim(word, "=") == "" ||
		strings.HasPrefix(word, "```") || strings.HasPrefix(word, "~~~") {
		return false
	}
	rest := strings.TrimLeft(word, "0123456789")
	return rest == word || (rest != "." && rest != ")")
}

// LoadHarness reads and validates a Paperclip harness configuration.
func LoadHarness(path string) (*Harness, error) {
	ctx, cancel := context.WithTimeout(context.Background(), contextopt.MaxDuration)
	defer cancel()
	return LoadHarnessContext(ctx, path)
}

// LoadHarnessContext validates bounded configuration without following symlinks.
func LoadHarnessContext(ctx context.Context, path string) (*Harness, error) {
	data, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("read harness file: %w", err)
	}

	var h Harness
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, fmt.Errorf("parse harness json: %w", err)
	}

	if h.Version != 1 {
		return nil, fmt.Errorf("invalid harness: expected version 1")
	}
	if err := validateHarnessValues([]string{h.Platform, h.AGitPushFormat}); err != nil {
		return nil, fmt.Errorf("invalid harness identity or push format: %w", err)
	}
	for _, values := range [][]string{h.OperatingContract, h.Invariants} {
		if err := validateHarnessValues(values); err != nil {
			return nil, fmt.Errorf("invalid harness contract or invariants: %w", err)
		}
	}

	return &h, nil
}

func validateHarnessValues(values []string) error {
	if len(values) == 0 || len(values) > maxHarnessValues {
		return fmt.Errorf("expected 1..%d values", maxHarnessValues)
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" || len(value) > maxHarnessValueBytes {
			return fmt.Errorf("values must be nonempty and at most %d bytes", maxHarnessValueBytes)
		}
	}
	return nil
}
