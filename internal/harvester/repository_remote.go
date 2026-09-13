package harvester

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode"
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
		cleaned, err := sanitizeRemote(fields[1])
		if err != nil {
			valid = false
			*probeErrors = appendBoundedError(*probeErrors, "remote URL rejected")
			continue
		}
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

func sanitizeRemote(raw string) (string, error) {
	if strings.ContainsFunc(raw, unicode.IsControl) {
		return "", errors.New("remote contains control characters")
	}
	if !strings.Contains(raw, "://") {
		var err error
		raw, err = inventorySCPURL(raw)
		if err != nil {
			return "", err
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.Opaque != "" || strings.Trim(u.Path, "/") == "" {
		return "", errors.New("remote URL is invalid")
	}
	if u.Scheme != "https" && u.Scheme != "http" && u.Scheme != "ssh" {
		return "", errors.New("remote scheme is unsupported")
	}
	u.User = nil
	u.RawQuery, u.Fragment, u.RawFragment = "", "", ""
	u.ForceQuery = false
	return u.String(), nil
}

func inventorySCPURL(raw string) (string, error) {
	host, path, found := strings.Cut(raw, ":")
	if at := strings.LastIndexByte(host, '@'); at >= 0 {
		host = host[at+1:]
	}
	if !found || host == "" || path == "" || strings.ContainsAny(host, "/?#\\ ") || strings.Contains(path, "@") {
		return "", errors.New("remote scp path is unsupported")
	}
	return "ssh://" + host + "/" + strings.TrimPrefix(path, "/"), nil
}
