package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalDiscoveryErrorsAndBounds(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		wantErr    bool
	}{
		{"valid", `{"models":[{"name":"local"}]}`, 200, false},
		{"bad-status", "", 500, true}, {"malformed", "{", 200, true},
		{"oversized", strings.Repeat(" ", MaxRoutingFileBytes+1), 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				if _, err := w.Write([]byte(tc.body)); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			got, err := queryOllamaEndpoint(context.Background(), server.Client(), server.URL)
			if (err != nil) != tc.wantErr {
				t.Fatalf("models=%v error=%v", got, err)
			}
		})
	}
}
