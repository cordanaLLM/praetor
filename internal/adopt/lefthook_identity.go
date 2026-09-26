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

// currentLefthookRendering reports whether data is exactly a current Praetor rendering and, when
// it is, whether it is the one carrying the checkpoint jobs. It is the one byte comparison both
// classification and hook activation (lefthookConfigIsPraetor) use.
func currentLefthookRendering(data []byte) (current, checkpoint bool) {
	switch string(data) {
	case buildLefthookYAMLFor(false):
		return true, false
	case buildLefthookYAMLFor(true):
		return true, true
	}
	return false, false
}

// isCurrentLefthookConfig reports whether data is exactly a current Praetor rendering.
func isCurrentLefthookConfig(data []byte) bool {
	current, _ := currentLefthookRendering(data)
	return current
}

// readExistingLefthook returns the bytes of lefthook.yml, or nil when the file is absent.
func (s *adoptSession) readExistingLefthook() ([]byte, error) {
	full, err := repoFile(s.repoPath, lefthookFile)
	if err != nil || !fileExists(full) {
		return nil, err
	}
	return readRepoFile(full)
}

// lefthookExtendsCanonical reports whether existing configuration bytes pull in the canonical
// policy; adoption then leaves the policy's vendored scripts to that policy (BUG-858).
func lefthookExtendsCanonical(existing []byte) bool {
	var parsed map[string]any
	if err := yaml.Unmarshal(existing, &parsed); err != nil {
		return false
	}
	return extendsCanonicalPolicy(parsed)
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
	if extendsCanonicalPolicy(parsed) {
		return lefthookIdentity{canonical: true, reason: "lefthook.yml extends the canonical Praetor hook policy " +
			canonicalLefthookPolicy + " (extends or remotes); adoption does not replace it with the smaller generated configuration, " +
			"--force included, and does not activate it. Update the vendored policy, its scripts and " +
			evasionHookFile + " together from one reviewed Praetor commit (" +
			".config/lefthook/README.md), then run 'lefthook install'"}
	}
	required, known, ok := generatedLefthookJobs(current)
	if !ok {
		return lefthookIdentity{}
	}
	if extra := extraLefthookJobs(lefthookJobs(parsed), required, known); len(extra) > 0 {
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

// generatedLefthookJobs returns the jobs every generated rendering holds (the one without
// checkpoint jobs) and the jobs the current rendering holds. A configuration needs only the
// first set to count as an extension, so jobs a user added to a rendering made before the
// checkpoint lifecycle became available stay protected once it does; the second set is what
// counts as generated when naming the extra jobs.
func generatedLefthookJobs(current string) (required, known map[string]bool, ok bool) {
	var base, rendered map[string]any
	if yaml.Unmarshal([]byte(buildLefthookYAMLFor(false)), &base) != nil || yaml.Unmarshal([]byte(current), &rendered) != nil {
		return nil, nil, false
	}
	return lefthookJobs(base), lefthookJobs(rendered), true
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

// lefthookJobs names every job of a parsed configuration as hook/kind/name, where kind is
// commands or scripts, whichever syntax declares it: the commands and scripts maps or the jobs
// list (lefthookListJob). Keys that are not hooks (min_version, output, extends) hold no job
// map and contribute nothing.
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
		addLefthookJobList(jobs, hook+"/", section["jobs"])
		if len(jobs) >= maxLefthookJobs {
			break
		}
	}
	return jobs
}

// addLefthookJobList names the entries of a hook's jobs list.
func addLefthookJobList(jobs map[string]bool, prefix string, list any) {
	entries, ok := list.([]any)
	if !ok {
		return
	}
	for i := 0; i < len(entries) && len(jobs) < maxLefthookJobs; i++ {
		job, isMap := entries[i].(map[string]any)
		if !isMap {
			job = nil
		}
		jobs[prefix+lefthookListJob(job, i)] = true
	}
}

// lefthookListJob names one jobs-list entry the way the commands and scripts maps name the
// same job, so a configuration in either syntax compares with the generated one: a script job
// is scripts/<script>, a group jobs/<name>, any other job commands/<name>. An unnamed job takes
// its run line, as lefthook itself names it (config.Job.PrintableName), then its position.
func lefthookListJob(job map[string]any, index int) string {
	if script := lefthookJobField(job, "script"); script != "" {
		return "scripts/" + script
	}
	name := lefthookJobField(job, "name")
	if name == "" {
		name = lefthookJobField(job, "run")
	}
	if name == "" {
		return fmt.Sprintf("jobs/[%d]", index)
	}
	if _, grouped := job["group"]; grouped {
		return "jobs/" + name
	}
	return "commands/" + name
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

// extraLefthookJobs returns, sorted, the jobs existing defines beyond known when existing holds
// every required job; otherwise nil. A configuration missing any required job is not an
// extension of the generated one, and keeps the --force contract.
func extraLefthookJobs(existing, required, known map[string]bool) []string {
	for job := range required {
		if !existing[job] {
			return nil
		}
	}
	var extra []string
	for job := range existing {
		if !known[job] && !required[job] {
			extra = append(extra, job)
		}
	}
	sort.Strings(extra)
	return extra
}

// lefthookJobField returns a string field of a jobs-list entry, or "" when it is absent or not
// a string.
func lefthookJobField(job map[string]any, key string) string {
	value, isString := job[key].(string)
	if !isString {
		return ""
	}
	return value
}
