package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func FuzzLSPHandleMessage(f *testing.F) {
	f.Add([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"capabilities":{}}}`))
	f.Add([]byte(`{"jsonrpc":"2.0","id":2,"method":"textDocument/hover","params":{"position":{"line":0,"character":0}}}`))
	f.Add([]byte(`{"jsonrpc":"2.0","method":"initialized"}`))
	f.Add([]byte(`{"jsonrpc":"2.0","id":3,"method":"shutdown"}`))

	srv := NewServer(strings.NewReader(""), &bytes.Buffer{}, "../..")
	ctx := context.Background()

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 65536 {
			data = data[:65536]
		}
		c, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()

		_, _, _ = srv.HandleMessage(c, data)
	})
}
