// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"strconv"
	"strings"
	"testing"
)

// ansibleFindings compares a report's findings, as rule@line, with want.
func ansibleFindings(t *testing.T, label string, rep *ScanReport, want ...string) {
	t.Helper()
	got := make([]string, 0, len(rep.Violations))
	for _, v := range rep.Violations {
		got = append(got, v.RuleID+"@"+strconv.Itoa(v.LineNumber))
	}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("%s: findings %v, want %v; violations %+v", label, got, want, rep.Violations)
	}
}

const ansiblePlaybook = `---
- name: Configure hosts
  hosts: all
  tasks:
    - name: Rebuild the cache
      ansible.builtin.command: /usr/local/bin/rebuild-cache
    - name: Count entries
      shell: cat /etc/hosts | wc -l
      changed_when: false
    - name: Probe
      ansible.builtin.uri:
        url: https://example.com
      ignore_errors: true
    - name: Group
      block:
        - name: Nested
          ansible.builtin.raw: uptime
      rescue:
        - name: Swallow
          ansible.builtin.debug:
            msg: failed
          failed_when: false
  handlers:
    - name: Restart
      ansible.builtin.shell: systemctl restart app
`

// Positive (#182): a command task without changed_when, a shell pipe without pipefail, and a
// discarded failure are reported in a play's tasks, a block's nested sections and its handlers.
func TestAnsibleScanner_ReportsEachInvariant(t *testing.T) {
	rep := scanFixtureFile(t, "site.yml", ansiblePlaybook)
	ansibleFindings(t, "site.yml", rep, "HISS-08@6", "HISS-07@8", "HISS-07@13", "HISS-08@17", "HISS-07@22", "HISS-08@25")
	if rep.Coverage.FilesRead != 1 || rep.Coverage.LanguagesRead["ansible"] != 1 {
		t.Errorf("a playbook must be read as ansible, got %+v", rep.Coverage)
	}
	root := t.TempDir()
	writeFixture(t, root, "roles/web/tasks/main.yml", "- name: Reload\n  command: nginx -s reload\n")
	writeFixture(t, root, "roles/web/handlers/main.yaml", "- name: Notify\n  ansible.legacy.shell: echo done | logger\n  changed_when: true\n")
	rep = scanFixture(t, root, ScanOptions{})
	ansibleFindings(t, "role", rep, "HISS-07@2", "HISS-08@2")
	if rep.Coverage.LanguagesRead["ansible"] != 2 {
		t.Errorf("both role files must be read as ansible, got %+v", rep.Coverage)
	}
}

// Negative: a task that states its change and failure conditions, a free-form creates, a
// registered failure, a pipe inside Jinja or quotes, an || and pipefail are clean; YAML that is
// not Ansible is not claimed, and stays non-source unscanned.
func TestAnsibleScanner_LegitimatePlaysAreClean(t *testing.T) {
	rep := scanFixtureFile(t, "clean.yaml", `- hosts: web
  pre_tasks:
    - name: Read the version
      ansible.builtin.command: cat /etc/version
      register: version
      changed_when: false
      failed_when: version.rc != 0
    - name: Unpack once
      command: tar xf /tmp/app.tgz creates=/opt/app
    - name: Install
      ansible.builtin.shell:
        cmd: set -o pipefail && curl -fsS https://example.com/key | gpg --dearmor -o /etc/key.gpg
        creates: /etc/key.gpg
    - name: Template pipe
      ansible.builtin.shell: echo {{ value | quote }} > /tmp/v
      args:
        removes: /tmp/stale
    - name: Quoted pipe
      shell: grep -E 'a|b' /etc/hosts || echo none
      changed_when: false
    - name: Tolerated
      ansible.builtin.command: /bin/probe
      ignore_errors: true
      register: probe
      changed_when: false
`)
	ansibleFindings(t, "clean.yaml", rep)
	for name, body := range map[string]string{
		"workflow.yml": "on: push\njobs:\n  build:\n    runs-on: ubuntu-latest\n",
		"labels.yml":   "- name: bug\n  color: red\n- name: docs\n  color: blue\n",
		"broken.yml":   "- hosts: [unclosed\n",
		"tasks.yml":    "- name: Run\n  command: /bin/true\n",
	} {
		rep := scanFixtureFile(t, name, body)
		if rep.Coverage.FilesRead != 0 || len(rep.Coverage.UnscannedLanguages) != 0 || len(rep.Violations) != 0 {
			t.Errorf("%s: want unscanned non-source YAML, got %+v / %+v", name, rep.Coverage, rep.Violations)
		}
	}
}

// Boundary: Ansible's boolean spellings, a template that is neither true nor false, PowerShell
// pipes, and a tasks/ directory outside roles/ that is not a role's.
func TestAnsibleScanner_Boundaries(t *testing.T) {
	rep := scanFixtureFile(t, "bools.yml", `- hosts: all
  tasks:
    - name: Yes
      ansible.builtin.ping:
      ignore_errors: yes
    - name: Templated
      ansible.builtin.ping:
      ignore_errors: "{{ ansible_check_mode }}"
    - name: Off
      ansible.builtin.ping:
      failed_when: off
    - name: PowerShell
      ansible.builtin.shell: Get-Item x | Select-Object Name
      args:
        executable: /usr/bin/pwsh
      changed_when: false
`)
	ansibleFindings(t, "bools.yml", rep, "HISS-07@5", "HISS-07@11")
	root := t.TempDir()
	writeFixture(t, root, "deploy/tasks/main.yml", "- name: Run\n  command: /bin/true\n")
	if rep := scanFixture(t, root, ScanOptions{}); rep.Coverage.FilesRead != 0 {
		t.Errorf("a tasks/ directory outside roles/ must not be claimed: %+v", rep.Coverage)
	}
	deep := "- hosts: all\n  tasks:\n" + nestedBlocks(maxAnsibleNesting+2)
	if rep := scanFixtureFile(t, "deep.yml", deep); rep.Breakdown["HISS-08"] != 0 || rep.Coverage.FilesRead != 1 {
		t.Errorf("a command nested past the block bound is not followed: %+v", rep.Violations)
	}
}

// nestedBlocks returns depth nested blocks whose innermost task runs a command.
func nestedBlocks(depth int) string {
	var sb strings.Builder
	indent := "    "
	for i := 0; i < depth; i++ {
		sb.WriteString(indent + "- block:\n")
		indent += "    "
	}
	sb.WriteString(indent + "- command: /bin/true\n")
	return sb.String()
}

// Positive, negative and boundary: a role task file sits in roles/<role>/tasks/ or handlers/,
// at any depth and in a subdirectory; a tasks/ directory directly under roles/ or outside it, and
// a role's other directories, are not.
func TestIsRoleTaskPath(t *testing.T) {
	for rel, want := range map[string]bool{
		"roles/web/tasks/main.yml": true, "site/roles/db/handlers/main.yaml": true,
		"roles/web/tasks/install/packages.yml": true,
		"roles/tasks/main.yml":                 false, "deploy/tasks/main.yml": false, "roles/web/templates/app.yml": false,
		"roles/web/tasks": false,
	} {
		if got := isRoleTaskPath(rel); got != want {
			t.Errorf("isRoleTaskPath(%q) = %t, want %t", rel, got, want)
		}
	}
}
