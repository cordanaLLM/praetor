package dogfood

import "time"

// ScheduleAttempt is durable before execution; running without a held lock means
// interruption, never success. Failures provisionally count when the attempt starts.
type ScheduleAttempt struct {
	Number      int       `json:"number"`
	Fingerprint string    `json:"fingerprint"`
	ArtifactDir string    `json:"artifact_dir"`
	Status      string    `json:"status"`
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at,omitempty"`
	Error       string    `json:"error,omitempty"`
}

type scheduleState struct {
	Version             int              `json:"version"`
	Attempts            int              `json:"attempts"`
	ConsecutiveFailures int              `json:"consecutive_failures"`
	LastAttempt         *ScheduleAttempt `json:"last_attempt,omitempty"`
}

// ScheduleReport separates admission decisions from the outcome of the last run.
// Resource bytes include every retained entry, including partial runs and .git.
type ScheduleReport struct {
	RunnerSHA256        string           `json:"runner_sha256"`
	RunnerIdentity      string           `json:"runner_identity"`
	Version             int              `json:"version"`
	Config              *ScheduleConfig  `json:"config"`
	Fingerprint         string           `json:"fingerprint"`
	Status              string           `json:"status"`
	Verified            bool             `json:"verified"`
	NextDue             time.Time        `json:"next_due,omitempty"`
	Attempts            int              `json:"attempts"`
	ConsecutiveFailures int              `json:"consecutive_failures"`
	RetainedBytes       int64            `json:"retained_bytes"`
	RetainedRuns        int              `json:"retained_runs"`
	LastAttempt         *ScheduleAttempt `json:"last_attempt,omitempty"`
	Suite               *SuiteReport     `json:"suite,omitempty"`
	Scope               string           `json:"scope"`
}
