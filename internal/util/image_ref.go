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
