package adopt

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"github.com/cordanaLLM/praetor/internal/lefthookconfig"
	"gopkg.in/yaml.v3"
)

const (
	// canonicalLefthookPolicy is the vendorable hook policy .config/lefthook/README.md tells
	// adopters to extend. A root lefthook.yml extending it carries more than the generated
	// configuration: the evasion interceptor as an agent job, the staged-file checks and the
	// ledger gates.
	canonicalLefthookPolicy = ".config/lefthook/praetor.yml"
	// maxLefthookJobs bounds the job scan of an existing configuration (HISS-02).
	maxLefthookJobs = lefthookconfig.MaxJobs
	// maxReportedJobs bounds how many job names a skip reason lists per group.
	maxReportedJobs = 5
)

// priorLefthookDigests are the digests (priorRendering) of every lefthook.yml Praetor generated
// before the current template, keyed to what produced them. Like isLegacyVerificationMakefile,
// only these texts are recognised, in either consistent line-ending style: adoption migrates
// them to the current rendering instead of treating its own earlier output as foreign and
// leaving it unfixed forever (BUG-859). An edited copy matches no digest and stays untouched.
// The files under testdata/lefthook reproduce each digest (lefthook_identity_test.go). The last
// two are the renderings that carried the Go jobs in every repository, whatever its languages
// (#568).
var priorLefthookDigests = map[string]string{
	"25e9d28b31d2423874042e8c4f9d864bcf970e111a78f2b0b8ad63990081b435": "HISS-16 labels, root Go jobs",
	"2b94aaf2bb95773724a4ead9dcabad7f5931408b07cf02b11c6768ad384b7413": "HISS-16 labels, root Go jobs, checkpoint jobs",
	"3fa5b142144df4f5aa06afaf110fc070375709958f1a30e4643f4d21a09347a4": "unguarded root Go jobs, go run fallback",
	"bcd930633a8229b68880f56f1be10af56d01b39a59033b83b9f01e2fad960022": "unguarded root Go jobs, go run fallback, checkpoint jobs",
	"9b5222377ace84afd5d37f9a3706984fd4680dfa9d7ed766b49e3d3e48c01895": "unfolded run lines, no document start",
	"c3ecfea62fcfeec128e8acf13b73ae046c66acb362a919803708d7a4d49ed2ac": "unfolded run lines, no document start, checkpoint jobs",
	"b6c0736f5389b4adb35967e1a5c8a735ebc68a87e8b85639beddafcd895f8403": "document start, no gate refusal note",
	"d20ed3ba2c21261d98ecfece5982d604c2ff9f476773e6e107ba54cd6848db87": "document start, no gate refusal note, checkpoint jobs",
	"daf1de1af7779eef3b0dd8a0a5940ef416a4e28ccc606dbc3baa876990cc8934": "Go jobs in every repository",
	"3d4a25b0269015ca166d92aa0d38e7000ea9d8d95f7529c7ff93577efc186f03": "Go jobs in every repository, checkpoint jobs",
}

// lefthookIdentity is what adoption concluded about an existing lefthook.yml.
type lefthookIdentity struct {
	// prior marks an exact earlier Praetor rendering, which adoption migrates.
	prior bool
	// canonical marks a configuration that extends the vendored canonical policy.
	canonical bool
	// reason says why adoption keeps the file; empty when it is Praetor's own text.
	reason string
}

// isPriorLefthookConfig reports whether data is an earlier Praetor rendering, in either
// consistent line-ending style.
func isPriorLefthookConfig(data []byte) bool {
	return isPriorRendering(data, priorLefthookDigests)
}

// lefthookMatch is what one comparison of existing bytes with the current renderings found.
type lefthookMatch struct {
	// found: the bytes hold a current rendering, line endings aside.
	found bool
	// checkpoint: the rendering found is the one carrying the checkpoint lifecycle jobs.
	checkpoint bool
	// exact: the bytes are the rendering's own LF bytes, the only bytes activation trusts.
	exact bool
}

// matchCurrentLefthook compares data with both current renderings for languages, without and
// with the checkpoint jobs. It is the one comparison classification, the write and hook
// activation (lefthookConfigIsPraetor) use. Mixed line endings match neither.
func matchCurrentLefthook(data []byte, languages hisscatalog.Language) lefthookMatch {
	for _, checkpoint := range []bool{false, true} {
		rendering := buildLefthookYAMLFor(languages, checkpoint)
		if string(data) == rendering {
			return lefthookMatch{found: true, checkpoint: checkpoint, exact: true}
		}
		if isLineEndingCheckout(data, rendering) {
			return lefthookMatch{found: true, checkpoint: checkpoint}
		}
	}
	return lefthookMatch{}
}

// isCurrentLefthookConfig reports whether data is exactly a current Praetor rendering for
// languages.
func isCurrentLefthookConfig(data []byte, languages hisscatalog.Language) bool {
	return matchCurrentLefthook(data, languages).exact
}

// readExistingLefthook returns the bytes of lefthook.yml and whether it exists.
func (s *adoptSession) readExistingLefthook() ([]byte, bool, error) {
	full, err := repoFile(s.repoPath, lefthookFile)
	if err != nil || !fileExists(full) {
		return nil, false, err
	}
	data, err := readRepoFile(full)
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

// lefthookLanguages returns the languages this run's lefthook.yml carries jobs for.
func (s *adoptSession) lefthookLanguages() hisscatalog.Language {
	return lefthookLanguages(s.verification)
}

// classifyLefthookConfig decides how adoption treats an existing lefthook.yml. Praetor's own
// renderings come first: a current one for languages, line endings aside, is verified and an
// earlier one (priorLefthookDigests) is migrated, neither needing --force. Every other
// configuration is the repository's and is kept, --force included, and not activated, because
// the audit checks only that the file exists and replacing it would drop whatever the
// repository composed in (#502). The reason says why: a configuration that extends the canonical
// policy names that policy; one that does not parse as a YAML mapping says so; any other names
// the generated jobs it lacks and the jobs it adds, so an operator can merge them by hand.
func classifyLefthookConfig(existing []byte, languages hisscatalog.Language) lefthookIdentity {
	if matchCurrentLefthook(existing, languages).found {
		return lefthookIdentity{}
	}
	if isPriorLefthookConfig(existing) {
		return lefthookIdentity{prior: true}
	}
	var parsed map[string]any
	if err := yaml.Unmarshal(existing, &parsed); err != nil {
		return lefthookIdentity{reason: "existing lefthook.yml is not a YAML mapping lefthook can read (" + err.Error() +
			"); kept, --force included, and not activated. " + lefthookRegenerateHint}
	}
	if extendsCanonicalPolicy(parsed) {
		return lefthookIdentity{canonical: true, reason: "lefthook.yml extends the canonical Praetor hook policy " +
			canonicalLefthookPolicy + " (extends or remotes); adoption does not replace it with the smaller generated configuration, " +
			"--force included, and does not activate it. Update the vendored policy, its scripts and " +
			evasionHookFile + " together from one reviewed Praetor commit (" +
			".config/lefthook/README.md), then run 'lefthook install'"}
	}
	missing, extra := lefthookJobDelta(lefthookJobs(parsed), languages)
	return lefthookIdentity{reason: "existing lefthook.yml differs from the scaffold adoption writes and is no earlier " +
		"Praetor rendering; kept, --force included, and not activated. " + describeLefthookJobDelta(missing, extra) +
		" Merge the generated jobs by hand, or " + lefthookRegenerateHint}
}

// lefthookRegenerateHint is how every note about a kept lefthook.yml ends: what regenerates it.
const lefthookRegenerateHint = "remove lefthook.yml and re-run adopt to regenerate it, then run 'lefthook install'"

// describeLefthookJobDelta says which generated jobs a kept configuration lacks, every one by
// name (a rendering holds at most a few dozen), and which jobs it adds, at most maxReportedJobs
// of them by name.
func describeLefthookJobDelta(missing, extra []string) string {
	lacks := "It holds every generated job"
	if len(missing) > 0 {
		lacks = fmt.Sprintf("It lacks %d generated jobs (%s)", len(missing),
			quoteFirst(missing, len(missing), len(missing), bareJobName))
	}
	if len(extra) == 0 {
		return lacks + " and adds none."
	}
	return fmt.Sprintf("%s and adds %d (%s).", lacks, len(extra), quoteFirst(extra, len(extra), maxReportedJobs, bareJobName))
}

// bareJobName renders a job name in a skip reason as lefthook spells it, unquoted: the names are
// slash paths such as pre-commit/commands/lint-docs, never text a quote must delimit.
func bareJobName(job string) string {
	return job
}

// lefthookJobDelta returns, sorted, the jobs of the generated rendering without checkpoint jobs
// that existing lacks, and the jobs existing defines beyond the rendering with them. The
// checkpoint jobs are optional: a configuration without them lacks nothing the generated one
// always holds, and one with them adds nothing.
func lefthookJobDelta(existing map[string]bool, languages hisscatalog.Language) (missing, extra []string) {
	required, known := generatedLefthookJobs(languages)
	for job := range required {
		if !existing[job] {
			missing = append(missing, job)
		}
	}
	for job := range existing {
		if !known[job] {
			extra = append(extra, job)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}

// generatedLefthookJobs returns the jobs of the rendering for languages without checkpoint jobs,
// which every generated configuration holds, and the jobs of the one with them.
func generatedLefthookJobs(languages hisscatalog.Language) (required, known map[string]bool) {
	return renderedLefthookJobs(buildLefthookYAMLFor(languages, false)), renderedLefthookJobs(buildLefthookYAMLFor(languages, true))
}

// renderedLefthookJobs names the jobs of a rendering. A rendering always parses
// (TestBuildLefthookYAML_FailsClosed); one that did not would name no job.
func renderedLefthookJobs(rendering string) map[string]bool {
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(rendering), &parsed); err != nil {
		return map[string]bool{}
	}
	return lefthookJobs(parsed)
}

// extendsCanonicalPolicy reports whether a parsed configuration pulls in the canonical policy,
// through extends or through the configs of a remotes entry (.config/lefthook/README.md).
func extendsCanonicalPolicy(parsed map[string]any) bool {
	if namesCanonicalPolicy(parsed["extends"]) {
		return true
	}
	remotes, isList := parsed["remotes"].([]any)
	for i := 0; isList && i < len(remotes) && i < maxLefthookJobs; i++ {
		remote, isMap := remotes[i].(map[string]any)
		if isMap && namesCanonicalPolicy(remote["configs"]) {
			return true
		}
	}
	return false
}

// namesCanonicalPolicy reports whether a path list, a string or a list of strings, names the
// canonical policy.
func namesCanonicalPolicy(paths any) bool {
	entries, ok := paths.([]any)
	if !ok {
		entries = []any{paths}
	}
	for i := 0; i < len(entries) && i < maxLefthookJobs; i++ {
		path, isString := entries[i].(string)
		if isString && strings.TrimPrefix(strings.TrimSpace(path), "./") == canonicalLefthookPolicy {
			return true
		}
	}
	return false
}

// lefthookJobs names every job of a parsed configuration as hook/kind/name
// (lefthookconfig.Job.Path), whichever syntax declares it: the commands and scripts maps or the
// jobs list.
func lefthookJobs(parsed map[string]any) map[string]bool {
	jobs := lefthookconfig.Jobs(parsed)
	names := make(map[string]bool, len(jobs))
	for _, job := range jobs {
		names[job.Path()] = true
	}
	return names
}
