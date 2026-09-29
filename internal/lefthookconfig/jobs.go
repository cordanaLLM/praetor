// Package lefthookconfig reads the jobs of a parsed lefthook configuration, in either syntax
// lefthook accepts for a hook: the commands and scripts maps, or the jobs list. Adoption
// compares an existing lefthook.yml with the one it generates through it
// (internal/adopt/lefthook_identity.go), and the flavor audit checks through it that a
// configuration runs the jobs a flavor claims (internal/flavor/lefthook_setting.go), so both
// name and read a job the same way.
package lefthookconfig

import (
	"fmt"
	"sort"
)

// MaxJobs bounds the jobs read from one configuration (HISS-02).
const MaxJobs = 512

// Job is one job of a lefthook configuration.
type Job struct {
	// Hook is the git or agent hook that runs the job, such as pre-commit.
	Hook string
	// Name is kind/name, the way the commands and scripts maps name the job: commands/gofmt,
	// scripts/check.sh, or jobs/<name> for a group of a jobs list (listJobName).
	Name string
	// Run is the job's run line, or "" for a script, a group or a job without one.
	Run string
}

// Path names the job as hook/kind/name, such as pre-commit/commands/gofmt.
func (j Job) Path() string {
	return j.Hook + "/" + j.Name
}

// Jobs returns every job of a parsed configuration, ordered by hook and then as each hook
// declares them (map-declared jobs by name). Keys that are not hooks (min_version, output,
// extends) hold no job map and contribute nothing. A group's own jobs are not read: the group
// is one job named jobs/<name>.
func Jobs(parsed map[string]any) []Job {
	hooks := make([]string, 0, len(parsed))
	for hook := range parsed {
		hooks = append(hooks, hook)
	}
	sort.Strings(hooks)
	jobs := make([]Job, 0)
	for i := 0; i < len(hooks) && len(jobs) < MaxJobs; i++ {
		section, ok := parsed[hooks[i]].(map[string]any)
		if !ok {
			continue
		}
		for _, kind := range []string{"commands", "scripts"} {
			jobs = appendMapJobs(jobs, hooks[i], kind, section[kind])
		}
		jobs = appendListJobs(jobs, hooks[i], section["jobs"])
	}
	return jobs
}

// HookRuns returns the run lines of hook's jobs in parsed, in Jobs order, skipping jobs
// without one.
func HookRuns(parsed map[string]any, hook string) []string {
	jobs := Jobs(parsed)
	runs := make([]string, 0, len(jobs))
	for _, job := range jobs {
		if job.Hook == hook && job.Run != "" {
			runs = append(runs, job.Run)
		}
	}
	return runs
}

// appendMapJobs appends the jobs of one commands or scripts map, by name.
func appendMapJobs(jobs []Job, hook, kind string, group any) []Job {
	named, ok := group.(map[string]any)
	if !ok {
		return jobs
	}
	names := make([]string, 0, len(named))
	for name := range named {
		names = append(names, name)
	}
	sort.Strings(names)
	for i := 0; i < len(names) && len(jobs) < MaxJobs; i++ {
		jobs = append(jobs, Job{Hook: hook, Name: kind + "/" + names[i], Run: field(asMap(named[names[i]]), "run")})
	}
	return jobs
}

// appendListJobs appends the entries of one hook's jobs list.
func appendListJobs(jobs []Job, hook string, list any) []Job {
	entries, ok := list.([]any)
	if !ok {
		return jobs
	}
	for i := 0; i < len(entries) && len(jobs) < MaxJobs; i++ {
		entry := asMap(entries[i])
		jobs = append(jobs, Job{Hook: hook, Name: listJobName(entry, i), Run: listJobRun(entry)})
	}
	return jobs
}

// listJobName names one jobs-list entry the way the commands and scripts maps name the same
// job, so a configuration in either syntax compares with another: a script job is
// scripts/<script>, a group jobs/<name>, any other job commands/<name>. An unnamed job takes
// its run line, as lefthook itself names it (config.Job.PrintableName), then its position.
func listJobName(job map[string]any, index int) string {
	if script := field(job, "script"); script != "" {
		return "scripts/" + script
	}
	name := field(job, "name")
	if name == "" {
		name = field(job, "run")
	}
	if name == "" {
		return fmt.Sprintf("jobs/[%d]", index)
	}
	if _, grouped := job["group"]; grouped {
		return "jobs/" + name
	}
	return "commands/" + name
}

// listJobRun is the run line of a jobs-list entry that is neither a script nor a group.
func listJobRun(job map[string]any) string {
	if _, grouped := job["group"]; grouped || field(job, "script") != "" {
		return ""
	}
	return field(job, "run")
}

// asMap returns value as a job map, or nil when it is none: a job without a body reads as one
// with no fields.
func asMap(value any) map[string]any {
	job, isMap := value.(map[string]any)
	if !isMap {
		return nil
	}
	return job
}

// field returns a string field of a job, or "" when it is absent or not a string.
func field(job map[string]any, key string) string {
	value, isString := job[key].(string)
	if !isString {
		return ""
	}
	return value
}
