package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeOpenLedger initializes a ledger and replaces OPEN.md with content.
func writeOpenLedger(t *testing.T, content string) (root, openPath string) {
	t.Helper()
	root = t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	openPath = filepath.Join(root, WorkingDirName, "OPEN.md")
	if err := os.WriteFile(openPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return root, openPath
}

// assertOpenUnchanged fails when OPEN.md is not byte-identical to want.
func assertOpenUnchanged(t *testing.T, openPath, want string) {
	t.Helper()
	got, err := os.ReadFile(openPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("refused selector still wrote OPEN.md:\n%s", got)
	}
}

func TestCompleteTaskActsOnTheNumberListReports(t *testing.T) {
	root, openPath := writeOpenLedger(t, "# Open\n- [x] Already done\n- [ ] Alpha\n- [ ] Beta\n")

	before, err := ListTasks(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 3 || before[1].Description != "Alpha" || before[1].Index != 2 {
		t.Fatalf("listing numbers completed and pending rows alike: %+v", before)
	}
	if err := CompleteTask(root, "2"); err != nil {
		t.Fatal(err)
	}
	after, err := ListTasks(root)
	if err != nil {
		t.Fatal(err)
	}
	if !after[1].Completed || after[2].Completed {
		t.Fatalf("complete 2 marked a row other than the one listed as 2: %+v", after)
	}
	content, err := os.ReadFile(openPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "- [x] Alpha (completed:") {
		t.Fatalf("Alpha was not the completed row:\n%s", content)
	}
}

func TestCompleteTaskRejectsAmbiguousTextSelector(t *testing.T) {
	const ledger = "# Open\n- [ ] deploy staging\n- [ ] deploy prod\n"
	root, openPath := writeOpenLedger(t, ledger)

	err := CompleteTask(root, "deploy")
	if err == nil {
		t.Fatal("ambiguous selector completed a row")
	}
	for _, want := range []string{"matches 2 pending tasks", "1: deploy staging", "2: deploy prod"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error does not name the candidates (%q): %v", want, err)
		}
	}
	assertOpenUnchanged(t, openPath, ledger)
}

func TestCompleteTaskNumericSelectorNeverMatchesDescriptionText(t *testing.T) {
	const ledger = "# Open\n- [ ] task about 7 things\n"
	root, openPath := writeOpenLedger(t, ledger)

	err := CompleteTask(root, "7")
	if err == nil {
		t.Fatal("numeric selector fell through to substring matching")
	}
	if !strings.Contains(err.Error(), "no task numbered 7") {
		t.Fatalf("unexpected error: %v", err)
	}
	assertOpenUnchanged(t, openPath, ledger)
}

func TestCompleteTaskBoundarySelectors(t *testing.T) {
	const ledger = "# Open\n- [x] Done already\n- [ ] Only pending\n"
	root, openPath := writeOpenLedger(t, ledger)

	for name, selector := range map[string]string{
		"zero":           "0",
		"negative":       "-1",
		"past last":      "3",
		"already done":   "1",
		"unmatched text": "nothing like this",
	} {
		if err := CompleteTask(root, selector); err == nil {
			t.Fatalf("%s selector %q was accepted", name, selector)
		}
	}
	assertOpenUnchanged(t, openPath, ledger)

	empty, _ := writeOpenLedger(t, "# Open\n")
	if err := CompleteTask(empty, "1"); err == nil {
		t.Fatal("empty ledger accepted task number 1")
	}
}

// TestCompleteTaskRefusesAnEmptySelectorOnAPopulatedLedger pins the guard at the top of
// CompleteTask. It must run against a ledger that holds pending rows: on an empty ledger
// every selector is refused anyway, so the assertion would hold with the guard deleted -
// without it "" reaches the text path, and strings.Contains reports a match for every
// pending row, which silently completes the first one on a single-task ledger.
func TestCompleteTaskRefusesAnEmptySelectorOnAPopulatedLedger(t *testing.T) {
	const one = "# Open\n- [ ] Only pending\n"
	single, singlePath := writeOpenLedger(t, one)
	for _, selector := range []string{"", "   ", "\t"} {
		err := CompleteTask(single, selector)
		if err == nil {
			t.Fatalf("empty selector %q completed the only pending task", selector)
		}
		if !strings.Contains(err.Error(), "task selector cannot be empty") {
			t.Fatalf("empty selector %q took the matching path: %v", selector, err)
		}
	}
	assertOpenUnchanged(t, singlePath, one)

	const many = "# Open\n- [ ] Alpha\n- [ ] Beta\n"
	multi, multiPath := writeOpenLedger(t, many)
	if err := CompleteTask(multi, ""); err == nil {
		t.Fatal("empty selector completed a task on a multi-row ledger")
	}
	assertOpenUnchanged(t, multiPath, many)
}

// TestTasksRefuseAnUnterminatedCodeFence pins the boundary case of the fence tracker:
// OPEN.md is hand-edited, a pasted block without its closing fence used to swallow every
// row after it, and `state task list` then reported zero tasks while two were pending.
func TestTasksRefuseAnUnterminatedCodeFence(t *testing.T) {
	const ledger = "# Open\n```sh\npraetorctl state task add x\n\n- [ ] ship the release\n- [ ] file the receipt\n"
	root, openPath := writeOpenLedger(t, ledger)

	tasks, err := ListTasks(root)
	if err == nil {
		t.Fatalf("unterminated fence silently hid %d rows: %+v", len(tasks), tasks)
	}
	if !strings.Contains(err.Error(), "unterminated code fence opened at line 2") {
		t.Fatalf("error does not locate the fence: %v", err)
	}
	if err := CompleteTask(root, "1"); err == nil {
		t.Fatal("completion accepted a ledger with an unterminated fence")
	}
	if _, err := ArchiveCompletedTasks(root, "fixture"); err == nil {
		t.Fatal("archive accepted a ledger with an unterminated fence")
	}
	assertOpenUnchanged(t, openPath, ledger)

	// Boundary: the same ledger with the fence closed lists both rows again.
	closed, _ := writeOpenLedger(t, ledger[:len("# Open\n```sh\npraetorctl state task add x\n")]+"```\n- [ ] ship the release\n- [ ] file the receipt\n")
	reopened, err := ListTasks(closed)
	if err != nil || len(reopened) != 2 {
		t.Fatalf("closed fence hid rows: %d, %v", len(reopened), err)
	}
}

func TestTasksIgnoreCheckboxesInsideCodeFences(t *testing.T) {
	const ledger = "# Open\n- [ ] Real task\n\n```markdown\n- [ ] Example pending\n- [x] Example done\n```\n"
	root, openPath := writeOpenLedger(t, ledger)

	tasks, err := ListTasks(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Description != "Real task" {
		t.Fatalf("fenced examples were listed as tasks: %+v", tasks)
	}
	if err := CompleteTask(root, "Example pending"); err == nil {
		t.Fatal("a fenced example was completed")
	}
	count, err := ArchiveCompletedTasks(root, "fixture")
	if err != nil || count != 0 {
		t.Fatalf("archive moved a fenced example: %d, %v", count, err)
	}
	assertOpenUnchanged(t, openPath, ledger)
}

func TestListTasksRejectsALineAtTheScanBound(t *testing.T) {
	root, _ := writeOpenLedger(t, "# Open\n- [ ] "+strings.Repeat("x", maxTaskLineBytes)+"\n")

	if _, err := ListTasks(root); err == nil {
		t.Fatal("a line at the scan bound was accepted")
	}
	shortRoot, _ := writeOpenLedger(t, "# Open\n- [ ] "+strings.Repeat("x", maxTaskLineBytes-16)+"\n")
	tasks, err := ListTasks(shortRoot)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("a line just under the bound was refused: %d, %v", len(tasks), err)
	}
}

func TestInitializedLedgerHoldsNoFabricatedTasks(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	tasks, err := ListTasks(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("scaffold seeded %d task rows: %+v", len(tasks), tasks)
	}
	backlog, err := os.ReadFile(filepath.Join(root, WorkingDirName, "BACKLOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(backlog), "- [") {
		t.Fatalf("scaffold seeded backlog rows:\n%s", backlog)
	}
}
