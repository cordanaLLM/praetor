package paperclip

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/gating"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"github.com/cordanaLLM/praetor/internal/lockdown"
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
// out. It is prescribed whatever forge origin names: the AGit refs/for push opens a review only
// on a forge that implements AGit (Forgejo, Gitea), the manifest declares no forge kind, and a
// form chosen from the remote's host name would be a guess of the kind BUG-852 removed from
// identity resolution. That residual of BUG-804 is stated in docs/guides/adoption-verification.md.
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

// harnessInvariants are the HISS rules a Paperclip run carries, in catalog order.
var harnessInvariants = [...]string{"HISS-01", "HISS-02", "HISS-04", "HISS-07", "HISS-10", "HISS-15", "HISS-16"}

// SynthesizeHarness generates a Paperclip agent harness embedding fleet contracts. The
// repository identity lookup (a git subprocess) runs under the caller's context. The
// platform is the identity .standards.yaml declares, else the origin remote's; with neither
// the error wraps util.ErrRepoIdentityUnresolved and no harness is returned, because the
// platform names a repository and none may be guessed. The invariants are the HISS catalog's
// adopted directives for facts (#68): the repository's languages (zero: unknown, every
// language clause labelled), so a Rust or C repository is not handed Go's context.Context or a
// Go one Rust's unwrap; the exceptions it declares and documents; and the function length its
// audit enforces, or, while that is unresolved, the audit ceiling a stricter policy tightens. A
// zero facts.CeilingFuncLOC is completed from config.AuditMaxFuncLOC, the ceiling every audit
// applies, so HISS-04 always states a number the refresh key can enumerate.
func SynthesizeHarness(ctx context.Context, repoPath string, facts hisscatalog.Facts) (*Harness, error) {
	if ctx == nil {
		return nil, fmt.Errorf("paperclip: context cannot be nil")
	}
	platform, err := resolvePlatform(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	if facts.CeilingFuncLOC == 0 {
		facts.CeilingFuncLOC = config.AuditMaxFuncLOC
	}
	invariants, err := catalogInvariants(facts)
	if err != nil {
		return nil, err
	}
	harness := releaseHarness(platform, receiptContract(receiptKeyPinned(ctx, repoPath)), invariants)
	return &harness, nil
}

// releaseHarness is this release's synthesis for platform under one set of repository facts:
// the receipt row (receiptContract of whether .standards.yaml pins a receipt key), and the
// invariants rendered for the HISS facts. SynthesizeHarness and currentReleaseHarnesses both
// build from it, so the refresh key enumerates exactly the text the synthesis writes.
func releaseHarness(platform, receiptRow string, invariants []string) Harness {
	return Harness{
		Version:  1,
		Platform: platform,
		OperatingContract: []string{
			"Branch push != shipping. Open PR required. Work ships after merge.",
			"Rebase onto main immediately: run git fetch origin && git rebase origin/main before proposing.",
			"Rule 0 Terminal Disposition: every run ends with structured disposition: in_review or blocked.",
			receiptRow,
			"Timeout != failure. Re-check open PRs before retry; prevent duplicate PRs.",
			// A Paperclip run reports to an orchestrating agent, so its product is internal text.
			config.RegisterDirective(config.TextRegisterInternal),
		},
		AGitPushFormat: agitPushFormat,
		Invariants:     invariants,
	}
}

// catalogInvariants renders harnessInvariants from the HISS catalog for facts.
func catalogInvariants(facts hisscatalog.Facts) ([]string, error) {
	invariants := make([]string, 0, len(harnessInvariants))
	for _, id := range harnessInvariants {
		rule, ok := hisscatalog.LookupRule(id)
		if !ok {
			return nil, fmt.Errorf("paperclip: HISS catalog has no rule %s", id)
		}
		invariants = append(invariants, rule.ID+": "+rule.AdoptedDirective(facts))
	}
	return invariants, nil
}

// priorOperatingContract, priorRegisterDirectives, priorAGitPushFormats and priorInvariants
// are the texts every earlier release synthesized, from 5d08985f until the contract moved to
// Caveman. The directive row was absent before the text register (#204) and changed form with
// the caveman skill (#225); the review-branch push joined the AGit push in #458. They are
// literals, not calls, so a later change to the current text cannot silently rewrite what
// "earlier output" means. cavemanOperatingContract and cavemanInvariants are the one Caveman
// release (#487) before the receipt row followed the pinned key (receiptContract): its own
// directive and push protocol, no other combination.
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
	priorAGitPushFormats = []string{
		"git push origin HEAD:refs/for/main -o topic=<issue-id>",
		"git push origin HEAD:refs/for/main -o topic=<issue-id> && git push origin HEAD:refs/heads/paperclip/<issue-id>",
	}
	cavemanOperatingContract = []string{
		"Branch push != shipping. Open PR required. Work ships after merge.",
		"Rebase onto main immediately: run git fetch origin && git rebase origin/main before proposing.",
		"Rule 0 Terminal Disposition: every run ends with structured disposition: in_review or blocked.",
		"Ed25519 Exit-0 Receipts: attach cryptographic execution receipts to all PR proposals.",
		"Timeout != failure. Re-check open PRs before retry; prevent duplicate PRs.",
		"Text register internal: `caveman` skill: fragments, no filler, verbatim code/paths/errors; facts, paths, commands, verdict.",
	}
	cavemanInvariants = []string{
		"HISS-01: Acyclic DAG control flow (no recursion)",
		"HISS-02: Scalar upper bounds on all loops; context timeout on all input and output",
		"HISS-04: McCabe Cyclomatic <= 10, Cognitive <= 15, Func LOC <= 75",
		"HISS-07: Zero .unwrap() / .expect(); all errors handled or wrapped",
		"HISS-10: Zero-warning tolerance across compiler, linters, and formatters",
		"HISS-15: 3D testing mandatory (Positive, Negative, Boundary >= 2 checks/dim)",
		"HISS-16: Canonical AGENTS.md compiled to vendor harnesses",
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
	// priorPinnedReceiptRows are the pinned-key receipt rows earlier releases wrote beside this
	// release's other rows. The row #523 introduced told every run on a pinned repository to mint
	// and attach a receipt, which a gate run with --dry-run never mints (#550). A harness carrying
	// one is unmodified earlier output under every fact combination (currentReleaseHarnesses).
	priorPinnedReceiptRows = []string{
		"Ed25519 Exit-0 Receipts: mint via `praetorctl gate run`; attach receipt to every PR proposal.",
	}
)

// PriorState is how the harness under a repository compares with earlier releases' output.
type PriorState struct {
	// Generated: harness.json is one earlier synthesis for the current identity, and rules.md
	// is absent or that synthesis's rendering. Adoption refreshes only such a harness; an
	// edited one is operator-owned and stays byte for byte.
	Generated bool
	// Rules: rules.md exists. A refresh rewrites it only then, so a rules.md the operator
	// removed stays removed.
	Rules bool
}

// PriorGenerated compares the harness under repoPath with every earlier synthesis for
// current's identity: each earlier release's text, and this release's text under repository
// facts other than current's (currentReleaseHarnesses). A harness equal to current itself is
// not earlier output. One consistent CRLF checkout style (core.autocrlf on Windows) compares as
// the LF bytes the release wrote.
func PriorGenerated(ctx context.Context, repoPath string, current *Harness) (PriorState, error) {
	if ctx == nil || current == nil {
		return PriorState{}, fmt.Errorf("paperclip: prior harness check requires context and current harness")
	}
	harnessData, rulesData, rulesExist, err := readHarnessFiles(ctx, repoPath)
	if err != nil {
		return PriorState{}, err
	}
	state := PriorState{Rules: rulesExist}
	harnessText, rulesText, ok := releaseText(harnessData, rulesData)
	if !ok {
		return state, nil
	}
	prior, err := matchPrior(harnessText, current)
	if err != nil {
		return PriorState{}, err
	}
	state.Generated = prior != nil && (!rulesExist || priorRules(rulesText, prior))
	return state, nil
}

// matchPrior returns the earlier synthesis harnessText renders byte for byte, or nil when it
// renders none of them or current itself. Text that does not decode as a harness renders no
// synthesis, since every synthesis marshals to a harness, so it matches none.
func matchPrior(harnessText string, current *Harness) (*Harness, error) {
	currentText, err := MarshalHarness(current)
	if err != nil || harnessText == string(currentText) {
		return nil, err
	}
	onDisk, decoded := decodedHarness(harnessText)
	if !decoded {
		return nil, nil
	}
	priors, err := priorHarnesses(current, statedFuncLOCs(current.Invariants))
	if err != nil {
		return nil, err
	}
	return renderedPrior(harnessText, &onDisk, priors)
}

// decodedHarness decodes text as a harness; decoded is false for text that is no harness JSON.
func decodedHarness(text string) (Harness, bool) {
	var harness Harness
	decoded := json.Unmarshal([]byte(text), &harness) == nil
	return harness, decoded
}

// renderedPrior returns the prior harnessText renders byte for byte. onDisk is harnessText
// decoded: only a prior equal to it field by field can render it, so only such a prior is
// marshalled and compared byte for byte.
func renderedPrior(harnessText string, onDisk *Harness, priors []Harness) (*Harness, error) {
	for index := 0; index < len(priors); index++ {
		if !sameHarness(onDisk, &priors[index]) {
			continue
		}
		rendered, err := MarshalHarness(&priors[index])
		if err != nil {
			return nil, err
		}
		if harnessText == string(rendered) {
			return &priors[index], nil
		}
	}
	return nil, nil
}

// sameHarness reports whether a and b hold the same values.
func sameHarness(a, b *Harness) bool {
	return a.Version == b.Version && a.Platform == b.Platform && a.AGitPushFormat == b.AGitPushFormat &&
		slices.Equal(a.OperatingContract, b.OperatingContract) && slices.Equal(a.Invariants, b.Invariants)
}

// priorRules reports whether rules is a rendering of prior some release wrote: the current
// markdownlint-clean layout (#477) or the unwrapped layout of every release before it.
func priorRules(rules string, prior *Harness) bool {
	return rules == renderRules(prior) || rules == renderUnwrappedRules(prior)
}

// renderUnwrappedRules is rules.md as every release before #477 wrote it: no blank line
// below a section heading and one unwrapped list item per value. It stays byte for byte so
// a released rules.md still reads as earlier output after the renderer changed.
func renderUnwrappedRules(h *Harness) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Paperclip Operating Rules (%s)\n\n## Operating Contract\n", h.Platform)
	for i := 0; i < len(h.OperatingContract) && i < maxHarnessValues; i++ {
		fmt.Fprintf(&b, "- %s\n", h.OperatingContract[i])
	}
	fmt.Fprintf(&b, "\n## AGit Push Protocol\n```bash\n%s\n```\n\n## High-Integrity Invariants\n", h.AGitPushFormat)
	for i := 0; i < len(h.Invariants) && i < maxHarnessValues; i++ {
		fmt.Fprintf(&b, "- %s\n", h.Invariants[i])
	}
	return b.String()
}

// releaseText folds one consistent CRLF checkout style to LF. A release never wrote mixed
// endings or a lone carriage return, so either one makes ok false: the harness is edited.
func releaseText(harness, rules []byte) (string, string, bool) {
	harnessText, _, err := util.NormalizeLineEndingsStrict(string(harness))
	if err != nil {
		return "", "", false
	}
	rulesText, _, err := util.NormalizeLineEndingsStrict(string(rules))
	return harnessText, rulesText, err == nil
}

// statedFuncLOC matches the function length a HISS-04 invariant states (hisscatalog funcLOCLimit).
var statedFuncLOC = regexp.MustCompile(`func LOC <= ([1-9][0-9]{0,5})\b`)

// statedFuncLOCs returns each function length invariants state, once, in order. matchPrior
// reads it from the current synthesis only, never from the harness on disk: a number read
// from the file on disk would make any hand-edited length a candidate, and so earlier output.
// It reads at most maxHarnessValues invariants, the most a valid harness carries.
func statedFuncLOCs(invariants []string) []int {
	var limits []int
	text := strings.Join(invariants[:min(len(invariants), maxHarnessValues)], "\n")
	for _, match := range statedFuncLOC.FindAllStringSubmatch(text, maxHarnessValues) {
		if limit, err := strconv.Atoi(match[1]); err == nil && !slices.Contains(limits, limit) {
			limits = append(limits, limit)
		}
	}
	return limits
}

// priorHarnesses is every earlier synthesis for current's identity: each register directive
// form under each push protocol, the Caveman release (cavemanHarness), then this release under
// every repository fact combination, with the function-length statements limitFacts accepts
// for limits, the lengths the current synthesis states (currentReleaseHarnesses).
func priorHarnesses(current *Harness, limits []int) ([]Harness, error) {
	released, err := currentReleaseHarnesses(current.Platform, limits)
	if err != nil {
		return nil, err
	}
	priors := make([]Harness, 0, len(priorRegisterDirectives)*len(priorAGitPushFormats)+1+len(released))
	for _, push := range priorAGitPushFormats {
		for _, directive := range priorRegisterDirectives {
			priors = append(priors, priorHarness(current, directive, push))
		}
	}
	priors = append(priors, cavemanHarness(current))
	return append(priors, released...), nil
}

// releasedReceiptRows is every receipt row the refresh key accepts beside this release's other
// rows: both rows receiptContract writes, one per answer receiptKeyPinned can give, then each
// pinned row an earlier release wrote (priorPinnedReceiptRows).
func releasedReceiptRows() []string {
	return append([]string{receiptContract(false), receiptContract(true)}, priorPinnedReceiptRows...)
}

// currentReleaseHarnesses renders this release for platform under every combination of the
// repository facts SynthesizeHarness reads: each receipt row (releasedReceiptRows), times every
// HISS fact combination releaseFacts builds from limits. A harness adoption wrote is still
// unmodified output after the operator pins receipt.public_key, as the unpinned row advises, the
// repository's languages or declared exceptions change, its policy resolves the function length
// the harness stated as the audit ceiling, or the release moved only its pinned receipt row;
// recognising it lets plain adopt refresh it without --force, which would also rewrite
// adopter-maintained files (#502). The set is enumerated rather than the fact-dependent rows
// normalised away, so recognition stays byte for byte: an edit to the receipt row or to one
// invariant, its function length included, still makes the harness operator-owned
// (limitFacts). It is bounded: len(releasedReceiptRows()) x len(releaseFacts(limits)) values.
// These are calls, so a later change to this text must first capture the rows as they stand as
// literals, as cavemanOperatingContract captured #487's and priorPinnedReceiptRows #523's.
func currentReleaseHarnesses(platform string, limits []int) ([]Harness, error) {
	combinations := releaseFacts(limits)
	rows := releasedReceiptRows()
	released := make([]Harness, 0, len(rows)*len(combinations))
	for _, facts := range combinations {
		invariants, err := catalogInvariants(facts)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			released = append(released, releaseHarness(platform, row, invariants))
		}
	}
	return released, nil
}

// releaseFacts is every HISS fact combination the refresh key accepts when the current
// synthesis states limits: every language set (hisscatalog.AllLanguages) times every exception
// set (hisscatalog.AllExceptions) times each function-length statement of limitFacts.
func releaseFacts(limits []int) []hisscatalog.Facts {
	statements := limitFacts(limits)
	combinations := make([]hisscatalog.Facts, 0,
		(int(hisscatalog.AllLanguages)+1)*(int(hisscatalog.AllExceptions)+1)*len(statements))
	for languages := range hisscatalog.AllLanguages + 1 {
		for exceptions := range hisscatalog.AllExceptions + 1 {
			for _, facts := range statements {
				facts.Languages, facts.Exceptions = languages, exceptions
				combinations = append(combinations, facts)
			}
		}
	}
	return combinations
}

// limitFacts is every function-length statement the refresh key accepts as earlier output, for
// a current synthesis stating limits. The audit ceiling (config.AuditMaxFuncLOC) comes from
// config, never from a harness, in both forms: stated unresolved, which a policy that later
// resolves replaces, and resolved at the ceiling. A plain number is accepted only when the
// current synthesis states it. Any other plain length is indistinguishable from an operator's
// edit, so it keeps the harness operator-owned: after the repository's resolved limit moves
// (50 to 45, or 50 up to the ceiling), `praetorctl adopt --force` refreshes it.
func limitFacts(stated []int) []hisscatalog.Facts {
	ceiling := config.AuditMaxFuncLOC
	statements := make([]hisscatalog.Facts, 0, 2+len(stated))
	statements = append(statements,
		hisscatalog.Facts{CeilingFuncLOC: ceiling},
		hisscatalog.Facts{MaxFuncLOC: ceiling, CeilingFuncLOC: ceiling})
	for _, limit := range stated {
		if limit != ceiling {
			statements = append(statements, hisscatalog.Facts{MaxFuncLOC: limit, CeilingFuncLOC: ceiling})
		}
	}
	return statements
}

// cavemanHarness is the Caveman release's synthesis for current's identity: its contract with
// the unconditional receipt row, under the review-branch push protocol it shipped with.
func cavemanHarness(current *Harness) Harness {
	prior := *current
	prior.OperatingContract = append([]string(nil), cavemanOperatingContract...)
	prior.AGitPushFormat = priorAGitPushFormats[len(priorAGitPushFormats)-1]
	prior.Invariants = cavemanInvariants
	return prior
}

func priorHarness(current *Harness, directive, push string) Harness {
	prior := *current
	prior.OperatingContract = append([]string(nil), priorOperatingContract...)
	if directive != "" {
		prior.OperatingContract = append(prior.OperatingContract, directive)
	}
	prior.AGitPushFormat = push
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

// receiptContract is the operating-contract row on Exit-0 receipts. A receipt is minted by
// `praetorctl gate run` and verifies only against the Ed25519 key .standards.yaml pins
// (receipt.public_key, lockdown.PinnedPublicKey; receiptKeyPinned); without one,
// Disposition.Validate and VerifyRun refuse any receipt attached (lockdown.ErrNoPinnedKey). So
// the row prescribes attaching receipts only when that key is pinned, and otherwise says a run
// attaches none, instead of prescribing a receipt nothing in the repository can verify (BUG-804).
// Even with the key pinned, only a gate run without --dry-run mints (gating runReceiptStage),
// with or without a go.mod: the Go stages then report not_applicable and the receipt stage
// still signs. Whether a run passes --dry-run is up to its caller, a repository-owned pre-push
// hook included, and no repository fact the synthesis reads says so. The pinned row therefore
// states the condition and forbids reporting a receipt the run did not mint, as the AGENTS.md
// harness does (adopt harnessReceiptLine, #503), instead of promising one per proposal (#550).
func receiptContract(pinned bool) string {
	if pinned {
		return "Ed25519 Exit-0 Receipts: only `" + gating.ReceiptCommand + "` without `--dry-run` mints one; " +
			"attach minted receipt to PR proposal; report no unminted receipt."
	}
	return "Ed25519 Exit-0 Receipts: none. " + manifestFile + " pins no valid receipt.public_key -> attach no receipt; " +
		"pin key from `praetorctl gate keygen` to require receipts."
}

// receiptKeyPinned reports whether the repository's manifest pins a well-formed receipt key.
// A missing manifest, a missing key and a malformed one all mean no receipt can verify. The
// manifest read is bounded by ctx (lockdown.PinnedPublicKey).
func receiptKeyPinned(ctx context.Context, repoPath string) bool {
	manifestPath, err := util.ConfinePath(repoPath, manifestFile)
	if err != nil {
		return false
	}
	_, err = lockdown.PinnedPublicKey(ctx, manifestPath)
	return err == nil
}

// resolvePlatform derives owner/name through config.ResolveRepositoryIdentity (ADR-0014 §3):
// the manifest's repository block, then the origin remote. It never reads the checkout path
// and substitutes no default owner: a parent directory names wherever the checkout sits, not
// its owner. A manifest that exists but cannot be read, and a remote read git did not
// answer, are errors rather than reasons to fall back. An identity neither source names
// wraps util.ErrRepoIdentityUnresolved as well as the resolver's own sentinel, so a caller
// that skips the harness for an unidentified repository keeps recognising the case.
func resolvePlatform(ctx context.Context, repoPath string) (string, error) {
	owner, repo, err := config.ResolveRepositoryIdentity(ctx, repoPath, "", "")
	if errors.Is(err, config.ErrOwnerUnknown) || errors.Is(err, config.ErrRepositoryNameUnknown) {
		return "", fmt.Errorf("paperclip: harness platform needs repository.owner and repository.name in %s or an origin remote: %w: %w",
			manifestFile, util.ErrRepoIdentityUnresolved, err)
	}
	if err != nil {
		return "", fmt.Errorf("paperclip: repository identity: %w", err)
	}
	return owner + "/" + repo, nil
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
func WriteHarness(h *Harness, repoPath string) error {
	return WriteHarnessFiles(h, repoPath, true)
}

// WriteHarnessFiles writes .paperclip/harness.json and, when rules is set, .paperclip/rules.md.
// A refresh of earlier output passes PriorState.Rules, so it never recreates a rules.md the
// operator removed. The directory and both files are created through a pinned handle on
// repoPath (util.MkdirConfined, util.WriteFileConfined), so a symlinked .paperclip cannot
// redirect them, not even one swapped in after a check, and a link planted at either file is
// refused instead of written through (BUG-826).
func WriteHarnessFiles(h *Harness, repoPath string, rules bool) error {
	data, err := MarshalHarness(h)
	if err != nil {
		return err
	}
	if err := util.MkdirConfined(repoPath, paperclipDir, dirPerm); err != nil {
		return fmt.Errorf("create %s dir: %w", paperclipDir, err)
	}
	if err := writeHarnessFile(repoPath, harnessFile, data); err != nil {
		return err
	}
	if !rules {
		return nil
	}
	return writeHarnessFile(repoPath, rulesFile, []byte(renderRules(h)))
}

// writeHarnessFile replaces name inside repoPath's .paperclip directory, confined to repoPath.
func writeHarnessFile(repoPath, name string, data []byte) error {
	rel := filepath.Join(paperclipDir, name)
	if err := util.WriteFileConfined(repoPath, rel, data, filePerm); err != nil {
		return fmt.Errorf("write %s: %w", filepath.Join(repoPath, rel), err)
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
