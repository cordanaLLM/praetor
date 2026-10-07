package util

import "strings"

// SplitImageReference splits a container image reference into its repository, tag and
// digest. The digest is everything after the first "@". The tag is the text after the last
// ":" that follows the last "/", so a registry port such as example.com:5000 is never read
// as a tag. A part the reference omits is returned empty; no default tag is implied.
func SplitImageReference(ref string) (repository, tag, digest string) {
	name, digest, _ := strings.Cut(ref, "@")
	if colon := strings.LastIndex(name, ":"); colon > strings.LastIndex(name, "/") {
		return name[:colon], name[colon+1:], digest
	}
	return name, "", digest
}

// DockerFrom is one Dockerfile FROM instruction: the image reference it builds from, the first
// field after FROM that is not a --flag such as --platform, and the stage name its AS clause
// gives it, lower-cased because stage names match case-insensitively. Either is "" when the
// instruction omits it.
type DockerFrom struct {
	Image string
	Stage string
}

// ParseDockerFrom reads line as a Dockerfile FROM instruction, matched case-insensitively as the
// first word of the line, and reports false for any other line, a comment or parser directive
// among them. It is the one FROM reader: the notices base image, the credits inventory, the
// devcontainer pin move and the flavor template check all call it.
func ParseDockerFrom(line string) (DockerFrom, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 || !strings.EqualFold(fields[0], "FROM") {
		return DockerFrom{}, false
	}
	var from DockerFrom
	for index, field := range fields[1:] {
		if from.Image == "" {
			if !strings.HasPrefix(field, "--") {
				from.Image = field
			}
			continue
		}
		if strings.EqualFold(field, "AS") && index+2 < len(fields) {
			from.Stage = strings.ToLower(fields[index+2])
			break
		}
	}
	return from, true
}
