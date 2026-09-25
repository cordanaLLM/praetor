package httpendpoint

import (
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
	for _, host := range []string{"example.test", "api.example.test", "xn--bcher-kva.example", "127.0.0.1", "::1", "a-b.example"} {
		if !CanonicalHost(host) {
			t.Fatalf("canonical host rejected: %q", host)
		}
	}
	for _, host := range []string{"", "EXAMPLE.test", "https://example.test", "example.test:443", "example.test/path",
		"user@example.test", "example.test.", ".example.test", "*.example.test", "-example.test", "example-.test",
		"bad_name.example", "127.000.0.1", "1.2.3", "[::1]", "fe80::1%eth0", "exa mple.test"} {
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
