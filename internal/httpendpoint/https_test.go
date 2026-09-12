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
