package adopt

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
)

func addDotnetVerification(p *VerificationPlan, inputs verificationInputs) error {
	projects := []string{}
	for path := range inputs.files {
		if strings.HasSuffix(path, ".csproj") {
			projects = append(projects, path)
		}
	}
	if len(projects) == 0 {
		return missingDotnetProjects(p, inputs)
	}
	p.Runtimes = append(p.Runtimes, "dotnet")
	slices.Sort(projects)
	runner, err := dotnetRunner(inputs.files["global.json"])
	if err != nil {
		return err
	}
	count := 0
	for _, project := range projects {
		isTest, err := dotnetTestProject(inputs.files[project])
		if err != nil {
			return fmt.Errorf("%s: %w", project, err)
		}
		argument := "./" + project
		p.Build = append(p.Build, []string{"dotnet", "restore", argument, "--locked-mode"},
			[]string{"dotnet", "build", argument, "-c", "Release", "--no-restore", "-warnaserror"})
		if isTest {
			p.Test = append(p.Test, dotnetTestCommand(argument, runner))
			count++
		}
	}
	if count == 0 {
		p.unavailable("No explicit .NET test project was found; define the native test gate instead of accepting a zero-test solution.")
	}
	return nil
}

func missingDotnetProjects(p *VerificationPlan, inputs verificationInputs) error {
	for path := range inputs.files {
		if strings.HasSuffix(path, ".sln") || strings.HasSuffix(path, ".slnx") || path == "global.json" {
			p.Runtimes = append(p.Runtimes, "dotnet")
			p.unavailable("A .NET solution or SDK marker exists without a discoverable project; select its project verification commands.")
			break
		}
	}
	return nil
}

func dotnetRunner(data []byte) (string, error) {
	if len(data) == 0 {
		return "VSTest", nil
	}
	fields, err := verificationObject(data, "test")
	if err != nil {
		return "", fmt.Errorf("global.json: %w", err)
	}
	if fields["test"] == nil {
		return "VSTest", nil
	}
	test, err := verificationObject(fields["test"], "runner")
	if err != nil {
		return "", err
	}
	runner, err := verificationString(test["runner"])
	if err != nil {
		return "", err
	}
	if runner != "VSTest" && runner != "Microsoft.Testing.Platform" {
		return "", errors.New("global.json test.runner must select VSTest or Microsoft.Testing.Platform")
	}
	return runner, nil
}

func dotnetTestCommand(project, runner string) []string {
	command := []string{"dotnet", "test"}
	if runner == "Microsoft.Testing.Platform" {
		command = append(command, "--project")
	}
	return append(command, project, "-c", "Release", "--no-build")
}

// Project metadata is declarative only. Conditional test markers cannot establish
// a runnable project without evaluating MSBuild, so they remain unselected.
type dotnetProjectMetadata struct {
	scopes       []bool
	seenRoot     bool
	testSDK      bool
	explicitTest *bool
	ambiguous    bool
}

func dotnetTestProject(data []byte) (bool, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	metadata := &dotnetProjectMetadata{}
	for i := 0; i < 8192; i++ {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			if !metadata.seenRoot || len(metadata.scopes) != 0 {
				return false, errors.New(".NET metadata requires a Project root")
			}
			return metadata.isTest(), nil
		}
		if err != nil {
			return false, err
		}
		if err := metadata.consume(decoder, token); err != nil {
			return false, err
		}
	}
	return false, errors.New(".NET project exceeds XML token bound")
}

func (m *dotnetProjectMetadata) isTest() bool {
	if m.ambiguous {
		return false
	}
	if m.explicitTest != nil {
		return *m.explicitTest
	}
	return m.testSDK
}

func (m *dotnetProjectMetadata) consume(decoder *xml.Decoder, token xml.Token) error {
	switch typed := token.(type) {
	case xml.StartElement:
		if err := m.pushScope(typed); err != nil {
			return err
		}
		return m.consumeMarker(decoder, typed)
	case xml.EndElement:
		if len(m.scopes) == 0 {
			return errors.New("unexpected .NET closing element")
		}
		m.scopes = m.scopes[:len(m.scopes)-1]
	case xml.CharData:
		if len(m.scopes) == 0 && strings.TrimSpace(string(typed)) != "" {
			return errors.New("text outside .NET Project root")
		}
	}
	return nil
}

func (m *dotnetProjectMetadata) consumeMarker(decoder *xml.Decoder, start xml.StartElement) error {
	conditional := m.scopes[len(m.scopes)-1]
	if start.Name.Local == "IsTestProject" {
		var value string
		if err := decoder.DecodeElement(&value, &start); err != nil {
			return err
		}
		m.scopes = m.scopes[:len(m.scopes)-1]
		value = strings.TrimSpace(value)
		if conditional || m.explicitTest != nil || (value != "true" && value != "false") {
			m.ambiguous = true
		}
		test := value == "true"
		m.explicitTest = &test
	}
	if isDotnetTestSDK(start) {
		m.testSDK = true
		m.ambiguous = m.ambiguous || conditional
	}
	return nil
}

func hasXMLCondition(start xml.StartElement) bool {
	for _, attr := range start.Attr {
		if attr.Name.Local == "Condition" {
			return true
		}
	}
	return false
}

func isDotnetTestSDK(start xml.StartElement) bool {
	if start.Name.Local != "PackageReference" {
		return false
	}
	for _, attr := range start.Attr {
		if attr.Name.Local == "Include" && attr.Value == "Microsoft.NET.Test.Sdk" {
			return true
		}
	}
	return false
}

func (m *dotnetProjectMetadata) pushScope(start xml.StartElement) error {
	if len(m.scopes) == 0 {
		if m.seenRoot || start.Name.Local != "Project" {
			return errors.New(".NET metadata requires one Project root")
		}
		m.seenRoot = true
	}
	conditional := len(m.scopes) > 0 && m.scopes[len(m.scopes)-1]
	m.scopes = append(m.scopes, conditional || hasXMLCondition(start))
	return nil
}
