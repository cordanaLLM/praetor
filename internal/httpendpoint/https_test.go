package httpendpoint

import (
	"net/url"
	"strings"
	"testing"
)

func TestValidateHTTPS(t *testing.T) {
	for _, value := range []string{"https://provider.example", "https://provider.example/api/v1", "https://tools.example/mcp/", "https://127.0.0.1:443/v1", "https://[::1]:65535/mcp/", "https://xn--bcher-kva.example"} {
		if err := ValidateHTTPS(value); err != nil {
			t.Fatalf("valid %s: %v", value, err)
		}
	}
	for _, value := range []string{"", "http://provider.example", "https://user:secret@provider.example", "https://example.test?", "https://example.test#", "https://example.test/%61", "https://example.test/../v1", "https://example.test//", "https://EXAMPLE.test", "https://example.test:0", "https://example.test:65536", "https://example.test:0443", "https://example.test:", "https://[0:0:0:0:0:0:0:1]", "https://127.000.0.1", "https://bad_name.example", "https://example.test/space here", "https://example.test/\n", "https://example.test\\path"} {
		if err := ValidateHTTPS(value); err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("invalid fixture %q accepted or leaked: %v", value, err)
		}
	}
}

func TestValidateHTTPSBounds(t *testing.T) {
	prefix := "https://example.test/"
	value := prefix + strings.Repeat("x", maxURLBytes-len(prefix))
	if err := ValidateHTTPS(value); err != nil {
		t.Fatal(err)
	}
	if err := ValidateHTTPS(value + "x"); err == nil {
		t.Fatal("accepted over-bound URL")
	}
	for _, size := range []int{63, 64} {
		err := ValidateHTTPS("https://" + strings.Repeat("a", size) + ".test")
		if (err == nil) != (size == 63) {
			t.Fatalf("DNS label boundary %d: %v", size, err)
		}
	}
}

func TestCanonicalHost(t *testing.T) {
	for _, host := range []string{"example.test", "api.example.test", "xn--bcher-kva.example", "127.0.0.1", "::1", "a-b.example",
		"example.1a", "example.0xg", "0x7f.example"} {
		if !CanonicalHost(host) {
			t.Fatalf("canonical host rejected: %q", host)
		}
	}
	for _, host := range []string{"", "EXAMPLE.test", "https://example.test", "example.test:443", "example.test/path",
		"user@example.test", "example.test.", ".example.test", "*.example.test", "-example.test", "example-.test",
		"bad_name.example", "127.000.0.1", "1.2.3", "[::1]", "fe80::1%eth0", "exa mple.test",
		// A numeric or 0x-hexadecimal final label makes WHATWG URL parsers read the
		// host as an IPv4 number, so the name is not an unambiguous DNS host.
		"example.1", "0x7f.0.0.1", "example.0x", "example.0xff", "0x7f", "2130706433"} {
		if CanonicalHost(host) {
			t.Fatalf("non-canonical host accepted: %q", host)
		}
	}
}

func TestCanonicalHostBounds(t *testing.T) {
	for _, size := range []int{63, 64} {
		if got := CanonicalHost(strings.Repeat("a", size) + ".test"); got != (size == 63) {
			t.Fatalf("label boundary %d accepted=%t", size, got)
		}
	}
	label := strings.Repeat("a", 61)
	longest := strings.Join([]string{label, label, label, label}, ".") // 4*61 + 3 = 247 bytes
	longest += ".abcde"                                                // 253 bytes
	if len(longest) != 253 || !CanonicalHost(longest) {
		t.Fatalf("253-byte host rejected: len=%d", len(longest))
	}
	if CanonicalHost(longest + "f") {
		t.Fatal("254-byte host accepted")
	}
}

func TestAuthorityHost(t *testing.T) {
	for raw, want := range map[string]string{
		"https://example.test":          "example.test",
		"https://example.test:1/x":      "example.test",
		"https://127.0.0.1:65535":       "127.0.0.1",
		"https://[::1]:443/mcp":         "::1",
		"//[2001:db8::1]":               "2001:db8::1",
		"//EXAMPLE.test./path?q#f":      "EXAMPLE.test.",
		"https://user@example.test:443": "example.test",
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := AuthorityHost(u); !ok || got != want {
			t.Fatalf("AuthorityHost(%q) = %q, %t; want %q", raw, got, ok, want)
		}
	}
	for _, raw := range []string{
		"//https:/example.test", "https://example.test:", "https://example.test:0", "https://example.test:65536",
		"https://example.test:0443", "//example.test:80:90", "//::1", "//[::1]:",
	} {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := AuthorityHost(u); ok {
			t.Fatalf("ambiguous authority %q accepted as %q", raw, got)
		}
	}
}

func TestLiteralURLText(t *testing.T) {
	for _, value := range []string{"example.test", "https://example.test/x?q=1#f", "[::1]:8080", "user@example.test"} {
		if !LiteralURLText(value) {
			t.Fatalf("literal URL text rejected: %q", value)
		}
	}
	for _, value := range []string{"", " ", "exa mple.test", "https://example.test\\x", "https://%65xample.test",
		"https://ex\u212Aample.test", "https://example.test/\t", "https://example.test/\x7f"} {
		if LiteralURLText(value) {
			t.Fatalf("non-literal URL text accepted: %q", value)
		}
	}
	longest := strings.Repeat("x", maxURLBytes)
	if !LiteralURLText(longest) || LiteralURLText(longest+"x") {
		t.Fatalf("URL text bound is not %d bytes", maxURLBytes)
	}
}
