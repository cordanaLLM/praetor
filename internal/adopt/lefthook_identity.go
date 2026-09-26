package adopt

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// canonicalLefthookPolicy is the vendorable hook policy .config/lefthook/README.md tells
	// adopters to extend. A root lefthook.yml extending it carries more than the generated
	// configuration: the evasion interceptor as an agent job, the staged-file checks and the
	// ledger gates.
	canonicalLefthookPolicy = ".config/lefthook/praetor.yml"
	// maxLefthookJobs bounds the job scan of an existing configuration (HISS-02).
	maxLefthookJobs = 512
	// maxReportedExtraJobs bounds how many preserved job names a skip reason lists.
	maxReportedExtraJobs = 5
)

// priorLefthookDigests are the SHA-256 digests of every lefthook.yml Praetor generated before
// the current template, keyed to what produced them. Like isLegacyVerificationMakefile, only
// these exact bytes are recognised: adoption migrates them to the current rendering instead of
// treating its own earlier output as foreign and leaving it unfixed forever (BUG-859). An
// edited copy is not exact and stays untouched. The files under testdata/lefthook reproduce
// each digest (lefthook_identity_test.go).
var priorLefthookDigests = map[string]string{
	"25e9d28b31d2423874042e8c4f9d864bcf970e111a78f2b0b8ad63990081b435": "HISS-16 labels, root Go jobs",
	"2b94aaf2bb95773724a4ead9dcabad7f5931408b07cf02b11c6768ad384b7413": "HISS-16 labels, root Go jobs, checkpoint jobs",
	"3fa5b142144df4f5aa06afaf110fc070375709958f1a30e4643f4d21a09347a4": "unguarded root Go jobs, go run fallback",
	"bcd930633a8229b68880f56f1be10af56d01b39a59033b83b9f01e2fad960022": "unguarded root Go jobs, go run fallback, checkpoint jobs",
}

// lefthookIdentity is what adoption concluded about an existing lefthook.yml.
type lefthookIdentity struct {
	// prior marks an exact earlier Praetor rendering, which adoption migrates.
	prior bool
	// canonical marks a configuration that extends the vendored canonical policy.
	canonical bool
	// reason says why adoption must not replace the file; empty when it may.
	reason string
}

// isPriorLefthookConfig reports whether data is exactly an earlier Praetor rendering.
func isPriorLefthookConfig(data []byte) bool {
	sum := sha256.Sum256(data)
	_, known := priorLefthookDigests[hex.EncodeToString(sum[:])]
	return known
}

// isCurrentLefthookConfig reports whether data is exactly a current Praetor rendering.
func isCurrentLefthookConfig(data []byte) bool {
	text := string(data)
	return text == buildLefthookYAMLFor(false) || text == buildLefthookYAMLFor(true)
}

// classifyExistingLefthook reads lefthook.yml, when present, and classifies it against the
// rendering adoption would write.
func (s *adoptSession) classifyExistingLefthook(current string) (lefthookIdentity, error) {
	full, err := repoFile(s.repoPath, lefthookFile)
	if err != nil || !fileExists(full) {
		return lefthookIdentity{}, err
	}
	data, err := readRepoFile(full)
	if err != nil {
		return lefthookIdentity{}, err
	}
	return classifyLefthookConfig(data, current), nil
}

// classifyLefthookConfig decides how adoption treats existing bytes. Praetor's own renderings,
// current or earlier, come first: they are neither user extensions nor foreign. A configuration
// that extends the canonical policy, or that defines every generated job plus more, is
// protected, because replacing it would silently drop hooks the repository chose to run
// (BUG-858). Anything else keeps the existing --force contract.
func classifyLefthookConfig(existing []byte, current string) lefthookIdentity {
	if isCurrentLefthookConfig(existing) {
		return lefthookIdentity{}
	}
	if isPriorLefthookConfig(existing) {
		return lefthookIdentity{prior: true}
	}
	var parsed map[string]any
	if err := yaml.Unmarshal(existing, &parsed); err != nil {
		return lefthookIdentity{}
	}
	if extendsCanonicalPolicy(parsed["extends"]) {
		return lefthookIdentity{canonical: true, reason: "lefthook.yml extends the canonical Praetor hook policy " +
			canonicalLefthookPolicy + "; adoption does not replace it with the smaller generated configuration, " +
			"--force included, and does not activate it. Update the vendored policy, its scripts and " +
			evasionHookFile + " together from one reviewed Praetor commit (" +
			".config/lefthook/README.md), then run 'lefthook install'"}
	}
	var generated map[string]any
	if err := yaml.Unmarshal([]byte(current), &generated); err != nil {
		return lefthookIdentity{}
	}
	if extra := extraLefthookJobs(lefthookJobs(parsed), lefthookJobs(generated)); len(extra) > 0 {
		return lefthookIdentity{reason: fmt.Sprintf("lefthook.yml defines every generated job plus %d more (%s); "+
			"adoption does not replace it, --force included, because that would drop them, and does not activate it. "+
			"Merge the generated jobs by hand, or remove lefthook.yml to regenerate it, then run 'lefthook install'",
			len(extra), summarizeJobs(extra))}
	}
	return lefthookIdentity{}
}

// summarizeJobs lists at most maxReportedExtraJobs job names for a message.
func summarizeJobs(jobs []string) string {
	if len(jobs) > maxReportedExtraJobs {
		return strings.Join(jobs[:maxReportedExtraJobs], ", ") + ", ..."
	}
	return strings.Join(jobs, ", ")
}

// extendsCanonicalPolicy reports whether an extends value, a string or a list of strings,
// names the canonical policy.
func extendsCanonicalPolicy(extends any) bool {
	entries, ok := extends.([]any)
	if !ok {
		entries = []any{extends}
	}
	for i := 0; i < len(entries) && i < maxLefthookJobs; i++ {
		path, isString := entries[i].(string)
		if isString && strings.TrimPrefix(strings.TrimSpace(path), "./") == canonicalLefthookPolicy {
			return true
		}
	}
	return false
}

// lefthookJobs names every job of a parsed configuration as hook/kind/name, where kind is
// commands or scripts. Keys that are not hooks (min_version, output, extends) hold no job map
// and contribute nothing.
func lefthookJobs(parsed map[string]any) map[string]bool {
	jobs := make(map[string]bool)
	for hook, body := range parsed {
		section, ok := body.(map[string]any)
		if !ok {
			continue
		}
		for _, kind := range []string{"commands", "scripts"} {
			addLefthookJobs(jobs, hook+"/"+kind+"/", section[kind])
		}
		if len(jobs) >= maxLefthookJobs {
			break
		}
	}
	return jobs
}

func addLefthookJobs(jobs map[string]bool, prefix string, group any) {
	named, ok := group.(map[string]any)
	if !ok {
		return
	}
	for name := range named {
		if len(jobs) >= maxLefthookJobs {
			return
		}
		jobs[prefix+name] = true
	}
}

// extraLefthookJobs returns, sorted, the jobs existing defines beyond generated when existing
// holds every generated job; otherwise nil. A configuration missing any generated job is not
// an extension of it, and keeps the --force contract.
func extraLefthookJobs(existing, generated map[string]bool) []string {
	for job := range generated {
		if !existing[job] {
			return nil
		}
	}
	var extra []string
	for job := range existing {
		if !generated[job] {
			extra = append(extra, job)
		}
	}
	sort.Strings(extra)
	return extra
}
