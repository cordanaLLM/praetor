package adopt

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/lefthookconfig"
	"github.com/cordanaLLM/praetor/internal/supplychain"
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
// The files under testdata/lefthook reproduce each digest (lefthook_identity_test.go). The
// "Go jobs in every repository" pair carried the Go jobs whatever the repository's languages
// (#568); the "online pre-commit audit" entries, one per language set with and without
// checkpoint jobs, ran the pre-commit audit against the forge before it passed --offline
// (preCommitAuditArgs); the "strict pre-push gate" entries, one per language set with and
// without checkpoint jobs, ran the gate without --admit-unsupported, which refused every push
// from a root with neither go.mod nor Cargo.lock (prePushGateArgs, #648); the "python3 by
// name" entries, one per language set, are the renderings whose checkpoint jobs named the
// interpreter python3 instead of starting it through the launcher adoption writes
// (lefthookPythonCommand, #339). A rendering without checkpoint jobs starts no interpreter and
// did not change. The "plain govulncheck" entries, one per language set holding Go with and
// without checkpoint jobs, are the renderings whose pre-push security job ran govulncheck ./...
// instead of the Go vulnerability gate (govulnGateArgs, #778); a rendering without Go jobs has no
// security job and did not change. The "engine from PATH" entries, one per language set with and
// without the REUSE job and with and without checkpoint jobs, are the renderings whose governance
// jobs ran whichever praetorctl came first on PATH instead of the engine the repository pins
// (engineLauncherFile, #906).
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
	"064c50473d6890c73c230875e6057772cafe6d6752a853ad453031c11ca90aff": "online pre-commit audit, governance jobs only",
	"95135352d1a8131742dd0f1ef0207616842410c4d56930b0f61ef78b79b2400a": "online pre-commit audit, governance jobs only, checkpoint jobs",
	"1c3eb49613cbdcbf8fc69ee0b289ae9b7f144da92fa40eb3179ede927cde0e1b": "online pre-commit audit, Go jobs",
	"095446dfe078ffcfc5dff7c590d12fbc695a3d3b9eabf714f8d36eed9ec3f561": "online pre-commit audit, Go jobs, checkpoint jobs",
	"cc8b9f422d3a8a139284c1373e479bde924c22025fb6f6f5cea2e18fcb26d41c": "online pre-commit audit, Rust jobs",
	"a535aa9f8745e41c1929c7d79079ed5bbbfbb03d41e58e3fd4be0d29fe7122ab": "online pre-commit audit, Rust jobs, checkpoint jobs",
	"419129472238157ea46c5ad26100b273ab46e2ca64d89f51f06bbbc509625ecf": "online pre-commit audit, Go and Rust jobs",
	"11a1713854b6d98bf5fb9025d001502daca00415ace8ecd3837550f92e2f49e6": "online pre-commit audit, Go and Rust jobs, checkpoint jobs",
	"fd7d6a64e2672f13865ec7632d909ba492d3366e32c71817e63cab8bc29ea929": "strict pre-push gate, governance jobs only",
	"8e1e73cf4c9d8e3a605c5b5575ca4bc63c0d2ecde8fd5c2c64d717ef7d66cc71": "strict pre-push gate, governance jobs only, checkpoint jobs",
	"b92b8c0473a44defde7c0ead9f5151c27c9b899dcfe68aaece5a7c4671259e7e": "strict pre-push gate, Go jobs",
	"0477b783574c7fc3b648c0c37692ca9fb32fd45f9234b99abd750cdb74b10b9f": "strict pre-push gate, Go jobs, checkpoint jobs",
	"e7255fc339416d267a023b0534162bcc6f64cef30689d395304ad0e3d9f3009e": "strict pre-push gate, Rust jobs",
	"5a7e085fb6a1e26b054c7f67fd6ee81dac1407d8128eaed63013c1407dc5c2c6": "strict pre-push gate, Rust jobs, checkpoint jobs",
	"5d969d0e03af74f7d75025770c98e93d40cd8ac7a14ed1913d958ffd21313cb8": "strict pre-push gate, Go and Rust jobs",
	"04e116207015f34efad4490a3d8573574b63a054b349463201e2d28f0c061541": "strict pre-push gate, Go and Rust jobs, checkpoint jobs",
	"6366ca411ab68b633d7e5e579cf3c4497735243112ba41118093b9c38c220d08": "python3 by name, governance jobs only, checkpoint jobs",
	"42cf6d9aa761700aa453b9fa4a19364f508db89828ef8d493b31836a8bc47c55": "python3 by name, Go jobs, checkpoint jobs",
	"7c7e9329b33038eaca07e3216d56d21dafd178b3445026af6be3326384f5a93c": "python3 by name, Rust jobs, checkpoint jobs",
	"80b28343df274a2fd174931c219cf883fe94a1338a9ce0169bdad979d4f407a4": "python3 by name, Go and Rust jobs, checkpoint jobs",
	"3cc9e2e22f4e98c249ce513a176d165aebd661921b589937da7e3781e6ee39ff": "plain govulncheck, Go jobs",
	"aa95033d2df0024b2b67d1408175027c39b05524a57e89ba84019b9abc9fd9d5": "plain govulncheck, Go jobs, checkpoint jobs",
	"56adc7c4a9aa1c73cf2d919853acb8a6c8dce1c7350f8c61362bdf63fcd902e8": "plain govulncheck, Go and Rust jobs",
	"b8be208e7174c611714c17664ac172ae7f1a95032afa4bfdca4dfb818939ca8c": "plain govulncheck, Go and Rust jobs, checkpoint jobs",
	"708fb4c2326e510de23efa8b8989cad17abe5d743b674f775d452821d80d9325": "engine from PATH, governance jobs only",
	"50f3a00e99e637ce80b9b3bd08197889e11a25e200e102066ad6476e69253e93": "engine from PATH, governance jobs only, checkpoint jobs",
	"7a9b196ddaf6eefb3d70dbfb1c6c406219b2d4e2ec9254f426cf23affe146842": "engine from PATH, governance jobs only, REUSE job",
	"fbdcb855f76713df09e627896342b7242ad1ca8dc14955d43579f46d54f9908c": "engine from PATH, governance jobs only, REUSE job, checkpoint jobs",
	"6e8397f4c5b56f8227ce5b40da28a35963cd8824e86416773cbb13a8822fce24": "engine from PATH, Go jobs",
	"1b34854125913918d8ef86cf576c3d74668810f708e1bf17973d1f79cb657eb5": "engine from PATH, Go jobs, checkpoint jobs",
	"50c7595c88c12014cb7ad8752e04a75881254fa7e937c3f6584fe9f47a081dc5": "engine from PATH, Go jobs, REUSE job",
	"526de57f2c8001ccd9a2632009fbe196ce8b74a373e552f7485772ae559070a2": "engine from PATH, Go jobs, REUSE job, checkpoint jobs",
	"98928f337e930b0a75c13d3158474ab65014fd3b13d79651e13fc55ff4454bb6": "engine from PATH, Rust jobs",
	"3267062423c743a045f2266ee8696bf42f59bcb0ba4cc07498788bf47b52a39c": "engine from PATH, Rust jobs, checkpoint jobs",
	"fc895b8f7e7c71b8c7065f7018bdf8c34381bd99e06ba47aaf7c7b3ecea9cda3": "engine from PATH, Rust jobs, REUSE job",
	"78e1c5409c04f6ec7a47ff8c9559f00bfc578e75ab2571e3a71a179b609ccbef": "engine from PATH, Rust jobs, REUSE job, checkpoint jobs",
	"c0dae53a3d657cd03aa2125c08508448c528a39732306f286e233fc530673517": "engine from PATH, Go and Rust jobs",
	"2681812cd4b8d0789f633cf4b154fa488fd097094082711b464a911b3bbe1a5f": "engine from PATH, Go and Rust jobs, checkpoint jobs",
	"914f94f2ffc841524179447d91d30bc371703f17f01210a4815173bd03c4cc03": "engine from PATH, Go and Rust jobs, REUSE job",
	"473316395ce9616099baf56df3d3cd399fbd2e961c5faa47150ee0483d7a685a": "engine from PATH, Go and Rust jobs, REUSE job, checkpoint jobs",
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

// matchCurrentLefthook compares data with both current renderings for shape, without and with
// the checkpoint jobs. It is the one comparison classification, the write and hook activation
// (lefthookConfigIsPraetor) use. Mixed line endings match neither.
func matchCurrentLefthook(data []byte, shape lefthookShape) lefthookMatch {
	for _, checkpoint := range []bool{false, true} {
		rendering := buildLefthookYAMLFor(shape, checkpoint)
		if string(data) == rendering {
			return lefthookMatch{found: true, checkpoint: checkpoint, exact: true}
		}
		if isLineEndingCheckout(data, rendering) {
			return lefthookMatch{found: true, checkpoint: checkpoint}
		}
	}
	return lefthookMatch{}
}

// isCurrentLefthookConfig reports whether data is exactly a current Praetor rendering for shape.
func isCurrentLefthookConfig(data []byte, shape lefthookShape) bool {
	return matchCurrentLefthook(data, shape).exact
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

// lefthookShape returns what this run's lefthook.yml carries jobs for: the languages
// (lefthookLanguages) and, when the root declares its licensing the REUSE way, the reuse-lint
// job (supplychain.ReuseDeclared). A root it cannot inspect is an error, never a repository
// without REUSE.
func (s *adoptSession) lefthookShape(ctx context.Context) (lefthookShape, error) {
	reuse, err := supplychain.ReuseDeclared(ctx, s.repoPath)
	if err != nil {
		return lefthookShape{}, fmt.Errorf("decide the reuse-lint job of %s: %w", lefthookFile, err)
	}
	return lefthookShape{languages: lefthookLanguages(s.verification), reuse: reuse}, nil
}

// classifyLefthookConfig decides how adoption treats an existing lefthook.yml. Praetor's own
// renderings come first: a current one for shape, line endings aside, is verified, and an
// earlier one (priorLefthookDigests) or the current one for the other REUSE switch
// (lefthookShape.otherReuse) is migrated, neither needing --force. Every other configuration is
// the repository's and is kept, --force included, and not activated, because the audit checks
// only that the file exists and replacing it would drop whatever the repository composed in
// (#502). The reason says why: a configuration that extends the canonical policy names that
// policy; one that does not parse as a YAML mapping says so; any other names the generated jobs
// it lacks and the jobs it adds, so an operator can merge them by hand.
func classifyLefthookConfig(existing []byte, shape lefthookShape) lefthookIdentity {
	if matchCurrentLefthook(existing, shape).found {
		return lefthookIdentity{}
	}
	if isPriorLefthookConfig(existing) || matchCurrentLefthook(existing, shape.otherReuse()).found {
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
	missing, extra := lefthookJobDelta(lefthookJobs(parsed), shape)
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
func lefthookJobDelta(existing map[string]bool, shape lefthookShape) (missing, extra []string) {
	required, known := generatedLefthookJobs(shape)
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

// generatedLefthookJobs returns the jobs of the rendering for shape without checkpoint jobs,
// which every generated configuration holds, and the jobs of the one with them.
func generatedLefthookJobs(shape lefthookShape) (required, known map[string]bool) {
	return renderedLefthookJobs(buildLefthookYAMLFor(shape, false)), renderedLefthookJobs(buildLefthookYAMLFor(shape, true))
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
