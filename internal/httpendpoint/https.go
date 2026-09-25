// Package httpendpoint validates explicitly configured HTTP service destinations.
package httpendpoint

import (
	"errors"
	"net/netip"
	"net/url"
	"path"
	"strconv"
	"strings"
)

const maxURLBytes = 4096

// ValidateHTTPS checks a canonical, credential-free HTTPS URL without I/O.
// DNS names must be lowercase ASCII (punycode is accepted); IP literals and ports
// must use canonical spelling. Literal clean paths may end in one slash. This
// checks syntax, not endpoint ownership, reachability, or authorization to connect.
func ValidateHTTPS(value string) error {
	if !literalURL(value) {
		return errors.New("HTTPS endpoint must be bounded literal ASCII without credentials, queries, fragments or escapes")
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Host == "" {
		return errors.New("endpoint requires an absolute credential-free HTTPS URL")
	}
	if u.String() != value || !canonicalAuthority(u) {
		return errors.New("HTTPS endpoint requires a canonical DNS or IP host and optional port 1..65535")
	}
	if !canonicalPath(u) {
		return errors.New("HTTPS endpoint requires a clean literal base path")
	}
	return nil
}

func literalURL(value string) bool {
	return LiteralURLText(value) && !strings.ContainsAny(value, "?#")
}

// LiteralURLText reports whether value is 1..4096 bytes of printable ASCII without
// space, backslash or percent sign. URL parsers disagree on backslashes (a path
// separator for WHATWG special schemes, invalid under RFC 3986), on percent-decoding
// hosts, and on stripping tabs and newlines; excluding them leaves one reading.
func LiteralURLText(value string) bool {
	if len(value) == 0 || len(value) > maxURLBytes || strings.ContainsAny(value, "\\%") {
		return false
	}
	for i := 0; i < len(value) && i < maxURLBytes; i++ {
		if value[i] < 33 || value[i] > 126 {
			return false
		}
	}
	return true
}

func canonicalAuthority(u *url.URL) bool {
	host, ok := AuthorityHost(u)
	return ok && CanonicalHost(host)
}

// AuthorityHost returns u's hostname when u.Host is exactly that hostname, bracketed
// when it contains a colon, plus an optional port 1..65535 in canonical decimal.
// It reports false for an empty or leading-zero port, a second colon outside
// brackets, or brackets around a non-IPv6 value, because such an authority reads
// as a different host under WHATWG URL parsing. Userinfo is not part of u.Host;
// callers that must refuse it check u.User. The host itself is not validated.
func AuthorityHost(u *url.URL) (string, bool) {
	host := u.Hostname()
	authority := host
	if strings.Contains(host, ":") {
		authority = "[" + host + "]"
	}
	if port := u.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 || strconv.Itoa(number) != port {
			return "", false
		}
		authority += ":" + port
	}
	return host, u.Host == authority
}

// CanonicalHost reports whether host is a canonical IP literal without a zone or a
// lowercase ASCII DNS name (punycode accepted) of at most 253 bytes whose labels are
// 1..63 alphanumeric or hyphen bytes without a leading or trailing hyphen. A scheme,
// port, path, userinfo, trailing dot or wildcard makes the value non-canonical, and
// so does a final label that is decimal or 0x-hexadecimal: WHATWG URL parsing reads
// such a name (for example 0x7f.0.0.1) as an IPv4 address rather than a DNS host.
func CanonicalHost(host string) bool {
	if address, err := netip.ParseAddr(host); err == nil {
		return address.Zone() == "" && address.String() == host
	}
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	labels := strings.Split(host, ".")
	if len(labels) > 127 || ipv4NumberLabel(labels[len(labels)-1]) {
		return false
	}
	for i := 0; i < len(labels) && i < 127; i++ {
		if !canonicalLabel(labels[i]) {
			return false
		}
	}
	return true
}

// ipv4NumberLabel mirrors the WHATWG "ends in a number" check for one lowercase
// label: all decimal digits, or 0x followed by zero or more hexadecimal digits.
func ipv4NumberLabel(label string) bool {
	if hex, ok := strings.CutPrefix(label, "0x"); ok {
		return strings.Trim(hex, "0123456789abcdef") == ""
	}
	return label != "" && strings.Trim(label, "0123456789") == ""
}

func canonicalLabel(label string) bool {
	if len(label) == 0 || len(label) > 63 || !hostnameAlnum(label[0]) || !hostnameAlnum(label[len(label)-1]) {
		return false
	}
	for i := 0; i < len(label) && i < 63; i++ {
		if !hostnameAlnum(label[i]) && label[i] != '-' {
			return false
		}
	}
	return true
}

func hostnameAlnum(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}

func canonicalPath(u *url.URL) bool {
	if u.Path == "" || u.Path == "/" {
		return true
	}
	return !strings.Contains(u.Path, "//") && strings.TrimSuffix(u.Path, "/") == path.Clean(u.Path) && u.EscapedPath() == u.Path
}
