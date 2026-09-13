package repairrun

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"sort"
)

// TestOutcome is a completed test reported by the trusted go test JSON wrapper.
type TestOutcome struct {
	Package string `json:"package"`
	Name    string `json:"name"`
	Status  string `json:"status"`
}
type testEvent struct {
	Action  string
	Package string
	Test    string
	Output  string
}

func testOutcomes(data []byte) ([]TestOutcome, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	running, completed := map[string]bool{}, map[string]TestOutcome{}
	for n := 0; n < 32768; n++ {
		var event testEvent
		err := d.Decode(&event)
		if errors.Is(err, io.EOF) {
			return completedTests(running, completed)
		}
		if err != nil {
			return nil, errors.New("verification did not return complete Go JSON events")
		}
		if err := acceptTestEvent(event, running, completed); err != nil {
			return nil, err
		}
	}
	return nil, errors.New("verification exceeded event count bound")
}

func acceptTestEvent(event testEvent, running map[string]bool, completed map[string]TestOutcome) error {
	if event.Test == "" {
		return nil
	}
	if event.Package == "" || len(event.Test) > 1024 || len(event.Package) > 1024 {
		return errors.New("invalid test event identity")
	}
	key := event.Package + ":" + event.Test
	switch event.Action {
	case "run":
		if running[key] {
			return errors.New("verification repeated a test identity")
		}
		running[key] = true
	case "pass", "fail", "skip":
		if !running[key] || completed[key].Name != "" {
			return errors.New("verification test completion lacks exactly one start")
		}
		completed[key] = TestOutcome{Package: event.Package, Name: event.Test, Status: event.Action}
	}
	return nil
}

func completedTests(running map[string]bool, completed map[string]TestOutcome) ([]TestOutcome, error) {
	if len(completed) == 0 || len(running) != len(completed) {
		return nil, errors.New("verification did not complete a nonempty set of tests")
	}
	result := make([]TestOutcome, 0, len(completed))
	for _, test := range completed {
		result = append(result, test)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Package+":"+result[i].Name < result[j].Package+":"+result[j].Name
	})
	return result, nil
}

func allTestsPassed(tests []TestOutcome) bool {
	passed := 0
	for _, test := range tests {
		if test.Status == "fail" {
			return false
		}
		if test.Status == "pass" {
			passed++
		}
	}
	return passed > 0
}

func containsFailedTest(tests []TestOutcome) bool {
	for _, test := range tests {
		if test.Status == "fail" {
			return true
		}
	}
	return false
}

func sameTestsPassed(before, after []TestOutcome) bool {
	if len(before) > len(after) || !validTestSet(before) || !validTestSet(after) {
		return false
	}
	actual := map[string]string{}
	for _, test := range after {
		actual[test.Package+":"+test.Name] = test.Status
	}
	for _, test := range before {
		status, exists := actual[test.Package+":"+test.Name]
		if !exists || (test.Status != "skip" && status != "pass") {
			return false
		}
	}
	return true
}

func validTestSet(tests []TestOutcome) bool {
	if len(tests) == 0 || len(tests) > 16384 {
		return false
	}
	previous := ""
	for _, test := range tests {
		key := test.Package + ":" + test.Name
		if test.Package == "" || test.Name == "" || len(key) > 2049 || key <= previous {
			return false
		}
		if test.Status != "pass" && test.Status != "fail" && test.Status != "skip" {
			return false
		}
		previous = key
	}
	return true
}

func failedTests(tests []TestOutcome) []TestOutcome {
	result := []TestOutcome{}
	for _, test := range tests {
		if test.Status == "fail" {
			result = append(result, test)
		}
	}
	return result
}
