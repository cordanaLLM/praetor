package config

import (
	"strings"
	"testing"
)

// evidenceLine returns the evidence line of the block rendered for policy.
func evidenceLine(t *testing.T, policy RegisterPolicy) string {
	t.Helper()
	block, err := RenderRegisterBlock(policy, false)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, "- Evidence above ") {
			return line
		}
	}
	t.Fatalf("block has no evidence line:\n%s", block)
	return ""
}

// Positive: register.evidence.dir replaces the default in the effective policy, in canonical
// form, and the rendered evidence line names it instead of .workingdir/evidence/.
func TestRegisterEvidenceDir_Positive(t *testing.T) {
	for written, want := range map[string]string{
		".workingdir2/evidence/": ".workingdir2/evidence/",
		".workingdir2/evidence":  ".workingdir2/evidence/",
		"scratch/agent evidence": "scratch/agent evidence/",
	} {
		m, err := loadRegisterManifest(t, "register:\n  evidence:\n    dir: \""+written+"\"\n")
		if err != nil {
			t.Fatalf("dir %q: %v", written, err)
		}
		policy := m.EffectiveRegister()
		if got := policy.EvidenceDir(); got != want {
			t.Errorf("dir %q: EvidenceDir = %q, want %q", written, got, want)
		}
		line := evidenceLine(t, policy)
		if !strings.Contains(line, "file under `"+want+"`;") || strings.Contains(line, EvidenceDirDefault) {
			t.Errorf("dir %q: evidence line = %q", written, line)
		}
	}
}

// The line a manifest with .workingdir2/evidence/ renders, the one an adopter keeping evidence
// under its legacy scratch root splices into AGENTS.md.
func TestRegisterEvidenceDir_RendersAdopterLine(t *testing.T) {
	m, err := loadRegisterManifest(t, "register:\n  evidence:\n    dir: .workingdir2/evidence/\n")
	if err != nil {
		t.Fatal(err)
	}
	const want = "- Evidence above 58 lines or 1500 tokens leaves the message as a file under `.workingdir2/evidence/`; " +
		"return `evidence: <path> sha256:<12 hex> lines:<n>` and fetch it only when a decision needs it."
	if got := evidenceLine(t, m.EffectiveRegister()); got != want {
		t.Fatalf("evidence line =\n%s\nwant\n%s", got, want)
	}
}

// Negative: every value that is not a portable, canonical, repository-relative directory is
// refused at load time, an explicit empty value included, and a policy built in code with such a
// value fails the render instead of falling back to the default.
func TestRegisterEvidenceDir_Negative(t *testing.T) {
	cases := map[string]struct{ value, want string }{
		"empty":           {`""`, "register evidence dir must be 1..255 bytes"},
		"absolute":        {"/srv/evidence/", "must be relative to the repository, not absolute"},
		"parent":          {"../evidence/", "must not leave the repository through '..'"},
		"inner parent":    {".workingdir/../../evidence/", "must not leave the repository through '..'"},
		"dot segment":     {"./.workingdir/evidence/", "must not hold an empty or '.' segment"},
		"double slash":    {".workingdir//evidence/", "must not hold an empty or '.' segment"},
		"lone slash":      {"/", "must be relative to the repository, not absolute"},
		"git directory":   {".git/evidence/", "must not lie inside .git"},
		"git upper case":  {".GIT/evidence/", "must not lie inside .git"},
		"drive letter":    {`"C:/evidence/"`, "without control characters"},
		"backslash":       {`'.workingdir\evidence\'`, "without control characters"},
		"backtick":        {"\"evi`dence/\"", "without control characters"},
		"pipe":            {`"evi|dence/"`, "without control characters"},
		"tab":             {`"evi\tdence/"`, "without control characters"},
		"oversized":       {strings.Repeat("e", MaxEvidenceDirBytes+1), "register evidence dir must be 1..255 bytes"},
		"integer":         {"3", "register evidence dir must be a string"},
		"sequence":        {"[a, b]", "register evidence dir must be a string"},
		"duplicated":      {"a/\n    dir: b/", `"dir"`},
		"unknown sibling": {"evidence/\n    directory: x/", `"directory"`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := loadRegisterManifest(t, "register:\n  evidence:\n    dir: "+tc.value+"\n")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
	policy := DefaultRegisterPolicy()
	policy.Evidence.Dir = "../escape/"
	if _, err := RenderRegisterBlock(policy, false); err == nil || !strings.Contains(err.Error(), "'..'") {
		t.Fatalf("an invalid in-code directory must fail the render, got %v", err)
	}
	m := &Manifest{Register: &RegisterPolicy{Evidence: EvidenceBounds{Dir: "/abs/"}}}
	if got := m.EffectiveRegister().EvidenceDir(); got != "/abs/" {
		t.Fatalf("an unloaded invalid directory must stay visible to its checks, got %q", got)
	}
}

// Boundary: without the key, and for a zero policy, the default directory renders; a single
// segment and a value of exactly MaxEvidenceDirBytes load; EvidenceRoot names the top-level
// directory an ignore rule covers.
func TestRegisterEvidenceDir_Boundary(t *testing.T) {
	m, err := loadRegisterManifest(t, "register:\n  evidence:\n    inline_max_lines: 40\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := m.EffectiveRegister().EvidenceDir(); got != EvidenceDirDefault {
		t.Fatalf("absent key: EvidenceDir = %q, want the default", got)
	}
	if got := (RegisterPolicy{}).EvidenceDir(); got != EvidenceDirDefault {
		t.Fatalf("zero policy: EvidenceDir = %q, want the default", got)
	}
	if line := evidenceLine(t, RegisterPolicy{}); !strings.Contains(line, "`"+EvidenceDirDefault+"`") {
		t.Fatalf("zero policy must render the default directory: %q", line)
	}
	longest := strings.Repeat("e", MaxEvidenceDirBytes-1) + "/"
	for value, want := range map[string]string{"evidence": "evidence/", longest: longest} {
		got, err := CheckEvidenceDir(value)
		if err != nil || got != want {
			t.Errorf("CheckEvidenceDir(%.20q) = %q, %v; want %q", value, got, err, want)
		}
	}
	if _, err := loadRegisterManifest(t, "register:\n  evidence:\n    dir: "+longest+"\n"); err != nil {
		t.Fatalf("a %d-byte directory must load: %v", MaxEvidenceDirBytes, err)
	}
	for dir, want := range map[string]string{EvidenceDirDefault: ".workingdir", ".workingdir2/evidence/": ".workingdir2", "evidence/": "evidence"} {
		if got := EvidenceRoot(dir); got != want {
			t.Errorf("EvidenceRoot(%q) = %q, want %q", dir, got, want)
		}
	}
}
