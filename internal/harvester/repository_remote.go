package harvester

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

func parseRemoteURLs(raw string, probeErrors *[]string) ([]string, bool) {
	urls := make([]string, 0)
	seen := make(map[string]struct{})
	valid := true
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if !inventoryRemoteFieldsValid(fields) {
			if strings.TrimSpace(line) != "" {
				valid = false
				*probeErrors = appendBoundedError(*probeErrors, "remote record is malformed")
			}
			continue
		}
		// util.ParseGitRemoteURL is the one network-remote parser; it drops user info,
		// query and fragment, so no credential reaches the report.
		parsed, err := util.ParseGitRemoteURL(fields[1])
		if err != nil {
			valid = false
			*probeErrors = appendBoundedError(*probeErrors, "remote URL rejected")
			continue
		}
		cleaned := parsed.String()
		if _, exists := seen[cleaned]; exists {
			continue
		}
		if len(urls) >= MaxRepositoryRemotes {
			valid = false
			*probeErrors = appendBoundedError(*probeErrors, fmt.Sprintf("remote count exceeds %d", MaxRepositoryRemotes))
			break
		}
		seen[cleaned] = struct{}{}
		urls = append(urls, cleaned)
	}
	sort.Strings(urls)
	return urls, valid
}

func inventoryRemoteFieldsValid(fields []string) bool {
	if len(fields) < 3 || len(fields) > 4 || (fields[2] != "(fetch)" && fields[2] != "(push)") {
		return false
	}
	return len(fields) == 3 || (fields[2] == "(fetch)" && strings.HasPrefix(fields[3], "[") && strings.HasSuffix(fields[3], "]"))
}
