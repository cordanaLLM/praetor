package p

import (
	"context"
	"net/http"
	"testing"
)

// Test files are exempt, as they are from the other Go rules.
func TestFetch(t *testing.T) {
	if _, err := fetch(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := http.Get("http://127.0.0.1:0"); err == nil {
		t.Fatal("expected a dial error")
	}
}

func fetch(ctx context.Context, url string) ([]byte, error) { return nil, ctx.Err() }
