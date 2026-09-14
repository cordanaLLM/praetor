package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"
)

// =========================================================================
// helpers
// =========================================================================

func mustHandle(t *testing.T, srv *Server, ctx context.Context, payload string) (*JSONRPCResponse, []JSONRPCNotification) {
	t.Helper()
	resp, notifs, err := srv.HandleMessage(ctx, []byte(payload))
	if err != nil {
		t.Fatalf("HandleMessage(%s) error: %v", payload, err)
	}
	return resp, notifs
}

func didOpen(uri, code string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":%q,"languageId":"go","version":1,"text":%q}}}`, uri, code)
}

func publishedParams(t *testing.T, notifs []JSONRPCNotification) PublishDiagnosticsParams {
	t.Helper()
	if len(notifs) != 1 {
		t.Fatalf("expected exactly 1 publishDiagnostics notification, got %d", len(notifs))
	}
	params, ok := notifs[0].Params.(PublishDiagnosticsParams)
	if !ok {
		t.Fatalf("unexpected params type %T", notifs[0].Params)
	}
	return params
}

func diagnosticsOf(t *testing.T, notifs []JSONRPCNotification) []Diagnostic {
	t.Helper()
	return publishedParams(t, notifs).Diagnostics
}

// closeQuietly closes a test pipe end; pipe closes cannot fail, but the error is still
// observed rather than discarded.
func closeQuietly(t *testing.T, c io.Closer) {
	t.Helper()
	if err := c.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
}

func analyze(t *testing.T, uri, code string) []Diagnostic {
	t.Helper()
	srv := NewServer(&bytes.Buffer{}, &bytes.Buffer{}, "v1.0.0")
	diags, err := srv.AnalyzeGoSource(uri, code)
	if err != nil {
		t.Fatalf("AnalyzeGoSource error: %v", err)
	}
	return diags
}

func countDiagnostics(diags []Diagnostic, code, messagePart string) int {
	n := 0
	for _, d := range diags {
		if d.Code == code && strings.Contains(d.Message, messagePart) {
			n++
		}
	}
	return n
}

func writeFrame(t *testing.T, w io.Writer, msg string) {
	t.Helper()
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n%s", len(msg), msg); err != nil {
		t.Fatalf("failed to write frame: %v", err)
	}
}

func readFrame(t *testing.T, reader *bufio.Reader) []byte {
	t.Helper()
	header, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(header, "Content-Length:") {
		t.Fatalf("expected Content-Length header, got: %q err=%v", header, err)
	}
	if _, err := reader.ReadString('\n'); err != nil {
		t.Fatalf("failed reading header terminator: %v", err)
	}
	n, parseErr := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(header, "Content-Length:")))
	if parseErr != nil {
		t.Fatalf("failed parsing length %q: %v", header, parseErr)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(reader, body); err != nil {
		t.Fatalf("failed reading full body: %v", err)
	}
	return body
}

func goFunc(name string, bodyLines int) string {
	var sb strings.Builder
	sb.WriteString("package sample\n\nfunc " + name + "() {\n")
	for i := 0; i < bodyLines; i++ {
		fmt.Fprintf(&sb, "\tx%d := %d\n\t_ = x%d\n", i, i, i)
	}
	sb.WriteString("}\n")
	return sb.String()
}

// =========================================================================
// Positive Tests
// =========================================================================

func TestLSP_Positive_ClientSession(t *testing.T) {
	ctx := context.Background()
	srv := NewServer(&bytes.Buffer{}, &bytes.Buffer{}, "v1.0.0")

	testLSPInitAndOpen(t, srv, ctx)
	testLSPChangeWithViolations(t, srv, ctx)
	testLSPSaveCloseAndShutdown(t, srv, ctx)
}

func testLSPInitAndOpen(t *testing.T, srv *Server, ctx context.Context) {
	resp, _ := mustHandle(t, srv, ctx, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if resp == nil || resp.Error != nil {
		t.Fatalf("initialize failed: resp=%+v", resp)
	}
	resMap, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got %T", resp.Result)
	}
	caps, ok := resMap["capabilities"].(map[string]any)
	if !ok || caps["textDocumentSync"] != 1 {
		t.Errorf("expected textDocumentSync=1, got %v", resMap["capabilities"])
	}

	_, notifs := mustHandle(t, srv, ctx, `{"jsonrpc":"2.0","method":"initialized","params":{}}`)
	if len(notifs) != 0 {
		t.Fatalf("initialized produced %d notifications", len(notifs))
	}

	cleanCode := "package sample\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n"
	resp, notifs = mustHandle(t, srv, ctx, didOpen("file:///sample.go", cleanCode))
	if resp != nil {
		t.Fatalf("didOpen must not produce a response, got %+v", resp)
	}
	if diags := diagnosticsOf(t, notifs); len(diags) != 0 {
		t.Errorf("expected 0 diagnostics for clean code, got %+v", diags)
	}
}

func testLSPChangeWithViolations(t *testing.T, srv *Server, ctx context.Context) {
	var longFn strings.Builder
	longFn.WriteString("package sample\n\nimport \"net/http\"\n\nfunc BadEverything() {\n")
	for i := 0; i < 80; i++ {
		fmt.Fprintf(&longFn, "\tx%d := %d\n", i, i)
	}
	longFn.WriteString("\tBadEverything()\n")
	longFn.WriteString("\tfor iter := 0; iter < 10; iter++ {\n\t\thttp.Get(\"http://example.com\")\n\t}\n")
	longFn.WriteString("\tfor {\n\t}\n")
	longFn.WriteString("\t_ = http.Get(\"http://example.com\")\n")
	longFn.WriteString("\tpanic(\"illegal\")\n")
	longFn.WriteString("}\n")

	didChangePayload := fmt.Sprintf(`{"jsonrpc":"2.0","method":"textDocument/didChange","params":{"textDocument":{"uri":"file:///sample.go","version":2},"contentChanges":[{"text":%q}]}}`, longFn.String())
	_, notifs := mustHandle(t, srv, ctx, didChangePayload)
	diags := diagnosticsOf(t, notifs)

	checks := []struct {
		code, part string
		want       int
	}{
		{"HISS-01", "Direct recursion", 1},
		{"HISS-02", "Unbounded loop", 1},
		{"HISS-02", "http.Get inside loop", 1},
		{"HISS-04", "LOC", 1},
		{"HISS-04", "statement count", 1},
		{"HISS-07", "blank identifier", 1},
		{"HISS-07", "panic", 1},
	}
	for _, c := range checks {
		if got := countDiagnostics(diags, c.code, c.part); got != c.want {
			t.Errorf("%s (%s): got %d diagnostics, want %d; all=%+v", c.code, c.part, got, c.want, diags)
		}
	}
}

func testLSPSaveCloseAndShutdown(t *testing.T, srv *Server, ctx context.Context) {
	// didSave re-publishes the diagnostics of the stored (changed) document.
	_, notifs := mustHandle(t, srv, ctx, `{"jsonrpc":"2.0","method":"textDocument/didSave","params":{"textDocument":{"uri":"file:///sample.go"}}}`)
	saveDiags := diagnosticsOf(t, notifs)
	if countDiagnostics(saveDiags, "HISS-07", "panic") != 1 || countDiagnostics(saveDiags, "HISS-01", "") != 1 {
		t.Errorf("didSave must republish the stored document's diagnostics, got %+v", saveDiags)
	}
	if params := publishedParams(t, notifs); params.Version == nil || *params.Version != 2 {
		t.Errorf("didSave must carry the stored version 2, got %+v", params)
	}

	// didClose forgets the document and clears its diagnostics.
	_, notifs = mustHandle(t, srv, ctx, `{"jsonrpc":"2.0","method":"textDocument/didClose","params":{"textDocument":{"uri":"file:///sample.go"}}}`)
	if params := publishedParams(t, notifs); len(params.Diagnostics) != 0 || params.URI != "file:///sample.go" {
		t.Errorf("didClose must publish an empty diagnostics set, got %+v", params)
	}
	srv.docMu.RLock()
	_, stillStored := srv.documents["file:///sample.go"]
	_, versionStored := srv.versions["file:///sample.go"]
	srv.docMu.RUnlock()
	if stillStored || versionStored {
		t.Error("didClose must delete the document and its version")
	}
	_, notifs = mustHandle(t, srv, ctx, `{"jsonrpc":"2.0","method":"textDocument/didSave","params":{"textDocument":{"uri":"file:///sample.go"}}}`)
	if diags := diagnosticsOf(t, notifs); len(diags) != 0 {
		t.Errorf("a closed document has no stored text and must analyse clean, got %+v", diags)
	}

	resp, _ := mustHandle(t, srv, ctx, `{"jsonrpc":"2.0","id":2,"method":"shutdown","params":null}`)
	if resp == nil || resp.Error != nil || resp.Result != nil || resp.ID == nil {
		t.Errorf("shutdown error: resp=%+v", resp)
	}
}

func TestLSP_Positive_ResponseWireFormat(t *testing.T) {
	cases := []struct {
		name    string
		resp    JSONRPCResponse
		want    []string
		forbids []string
	}{
		{"shutdown success carries a null result",
			JSONRPCResponse{JSONRPC: "2.0", ID: float64(101), Result: nil},
			[]string{`"result":null`, `"id":101`}, []string{`"error"`}},
		{"parse error carries a null id and no result",
			JSONRPCResponse{JSONRPC: "2.0", ID: nil, Error: &JSONRPCError{Code: -32700, Message: "x"}},
			[]string{`"id":null`, `"error":{"code":-32700`}, []string{`"result"`}},
		{"string ids are preserved",
			JSONRPCResponse{JSONRPC: "2.0", ID: "abc", Result: map[string]any{"ok": true}},
			[]string{`"id":"abc"`, `"result":{"ok":true}`}, nil},
	}
	for _, c := range cases {
		wire, err := json.Marshal(c.resp)
		if err != nil {
			t.Fatalf("%s: marshal error: %v", c.name, err)
		}
		for _, w := range c.want {
			if !strings.Contains(string(wire), w) {
				t.Errorf("%s: frame %s lacks %s", c.name, wire, w)
			}
		}
		for _, f := range c.forbids {
			if strings.Contains(string(wire), f) {
				t.Errorf("%s: frame %s must not contain %s", c.name, wire, f)
			}
		}
	}
}

func TestLSP_Positive_FramedTransport(t *testing.T) {
	clientRead, serverWrite := io.Pipe()
	serverRead, clientWrite := io.Pipe()
	defer closeQuietly(t, clientRead)
	defer closeQuietly(t, serverWrite)
	defer closeQuietly(t, serverRead)
	defer closeQuietly(t, clientWrite)

	srv := NewServer(serverRead, serverWrite, "v1.0.0")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- srv.Run(ctx)
	}()

	reader := bufio.NewReader(clientRead)
	writeFrame(t, clientWrite, `{"jsonrpc":"2.0","id":100,"method":"initialize","params":{}}`)
	if body := readFrame(t, reader); !strings.Contains(string(body), `"standards-lsp"`) {
		t.Errorf("expected initialize response to name standards-lsp, got: %s", body)
	}

	// A parse error frame carries "id":null.
	writeFrame(t, clientWrite, `{"jsonrpc":"2.0", broken`)
	if body := readFrame(t, reader); !strings.Contains(string(body), `"id":null`) || !strings.Contains(string(body), `-32700`) {
		t.Errorf("expected a -32700 frame with a null id, got: %s", body)
	}

	// The shutdown frame must carry a result member, or vscode-jsonrpc drops it.
	writeFrame(t, clientWrite, `{"jsonrpc":"2.0","id":101,"method":"shutdown"}`)
	var decoded map[string]json.RawMessage
	body := readFrame(t, reader)
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("shutdown frame is not JSON: %s (%v)", body, err)
	}
	if _, hasResult := decoded["result"]; !hasResult || string(decoded["id"]) != "101" {
		t.Errorf("shutdown frame must contain result and id 101, got %s", body)
	}

	// Notifications after shutdown are dropped silently; exit ends the loop cleanly.
	writeFrame(t, clientWrite, `{"jsonrpc":"2.0","method":"textDocument/didSave","params":{"textDocument":{"uri":"file:///x.go"}}}`)
	writeFrame(t, clientWrite, `{"jsonrpc":"2.0","method":"exit"}`)
	select {
	case err := <-errChan:
		if err != nil {
			t.Errorf("Run must return nil after exit, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for server exit")
	}
}

func TestLSP_Positive_LineDelimitedFallback(t *testing.T) {
	in := strings.NewReader(`{"jsonrpc":"2.0","id":7,"method":"initialize","params":{}}` + "\n")
	srv := NewServer(in, &bytes.Buffer{}, "")
	payload, err := srv.readFramedMessage()
	if err != nil || !strings.Contains(string(payload), `"id":7`) {
		t.Fatalf("line-delimited message must be accepted, got %q err=%v", payload, err)
	}
	if srv.version != "v1.0.0" {
		t.Errorf("empty version must default to v1.0.0, got %q", srv.version)
	}
}

// =========================================================================
// Negative Tests
// =========================================================================

func TestLSP_Negative_MalformedAndUnknownMethods(t *testing.T) {
	ctx := context.Background()
	srv := NewServer(&bytes.Buffer{}, &bytes.Buffer{}, "v1.0.0")

	resp, notifs := mustHandle(t, srv, ctx, `{"jsonrpc":"2.0", invalid-json`)
	if resp == nil || resp.Error == nil || resp.Error.Code != -32700 || resp.ID != nil {
		t.Errorf("expected code -32700 with null id for parse error, got: %+v", resp)
	}
	if len(notifs) != 0 {
		t.Errorf("expected 0 notifications on parse error")
	}

	resp, _ = mustHandle(t, srv, ctx, `{"jsonrpc":"2.0","id":42,"method":"custom/unknown","params":{}}`)
	if resp == nil || resp.Error == nil || resp.Error.Code != -32601 {
		t.Errorf("expected code -32601 for unknown method, got: %+v", resp)
	}
	resp, _ = mustHandle(t, srv, ctx, `{"jsonrpc":"2.0","method":"custom/unknownNotification","params":{}}`)
	if resp != nil {
		t.Errorf("unknown notifications must not be answered, got %+v", resp)
	}

	// Malformed params never end the session: notifications are dropped, requests
	// receive -32602.
	for _, bad := range []string{
		`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":"nope"}`,
		`{"jsonrpc":"2.0","method":"textDocument/didOpen"}`,
		`{"jsonrpc":"2.0","method":"textDocument/didChange","params":[]}`,
		`{"jsonrpc":"2.0","method":"textDocument/didSave","params":1}`,
		`{"jsonrpc":"2.0","method":"textDocument/didClose","params":true}`,
	} {
		resp, notifs := mustHandle(t, srv, ctx, bad)
		if resp != nil || len(notifs) != 0 {
			t.Errorf("malformed notification must be dropped silently: %s -> resp=%+v notifs=%d", bad, resp, len(notifs))
		}
	}
	resp, _ = mustHandle(t, srv, ctx, `{"jsonrpc":"2.0","id":5,"method":"textDocument/didOpen","params":"nope"}`)
	if resp == nil || resp.Error == nil || resp.Error.Code != -32602 {
		t.Errorf("malformed request params must yield -32602, got %+v", resp)
	}

	_, notifs = mustHandle(t, srv, ctx, didOpen("file:///broken.go", "package sample\nfunc Broken( {\n"))
	diags := diagnosticsOf(t, notifs)
	if len(diags) != 1 || diags[0].Code != "SYNTAX" {
		t.Errorf("expected SYNTAX diagnostic, got: %+v", diags)
	}

	srv.isShutdown = true
	resp, _ = mustHandle(t, srv, ctx, `{"jsonrpc":"2.0","id":99,"method":"initialize"}`)
	if resp == nil || resp.Error == nil || resp.Error.Code != -32600 {
		t.Errorf("expected code -32600 for request after shutdown, got: %+v", resp)
	}
	resp, notifs = mustHandle(t, srv, ctx, `{"jsonrpc":"2.0","method":"initialized"}`)
	if resp != nil || len(notifs) != 0 {
		t.Errorf("a notification after shutdown must be dropped, got resp=%+v notifs=%d", resp, len(notifs))
	}
}

func TestLSP_Negative_CancelledContext(t *testing.T) {
	srv := NewServer(&bytes.Buffer{}, &bytes.Buffer{}, "v1.0.0")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := srv.HandleMessage(ctx, []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)); !errors.Is(err, context.Canceled) {
		t.Errorf("HandleMessage must surface the cancelled context, got %v", err)
	}
	if _, err := srv.publishDiagnosticsFor(ctx, "file:///x.go", "package p\n", 1); !errors.Is(err, context.Canceled) {
		t.Errorf("publishDiagnosticsFor must surface the cancelled context, got %v", err)
	}
}

func TestLSP_Negative_RunStopsOnCancelWhileIdle(t *testing.T) {
	serverRead, clientWrite := io.Pipe()
	defer closeQuietly(t, clientWrite)
	defer closeQuietly(t, serverRead)

	srv := NewServer(serverRead, &bytes.Buffer{}, "v1.0.0")
	ctx, cancel := context.WithCancel(context.Background())
	errChan := make(chan error, 1)
	go func() {
		errChan <- srv.Run(ctx)
	}()

	// The daemon idles on stdin with nothing to read; a signal-driven cancel must
	// still end it without waiting for the client to write or close the pipe.
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-errChan:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run must return the cancellation, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not observe the cancelled context while blocked on stdin")
	}
}

func TestLSP_Negative_FramingErrors(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  error
	}{
		{"missing content-length", "X-Custom: 1\r\n\r\n", errMissingContentLength},
		{"oversize content-length", fmt.Sprintf("Content-Length: %d\r\n\r\n{}", maxLSPMessageSize+1), nil},
		{"negative content-length", "Content-Length: -5\r\n\r\n{}", nil},
		{"unparsable content-length", "Content-Length: abc\r\n\r\n{}", nil},
		{"truncated body", "Content-Length: 50\r\n\r\n{\"jsonrpc\":\"2.0\"}", io.ErrUnexpectedEOF},
		{"header bound hit before content-length", strings.Repeat("X-H: 1\r\n", maxHeaderLines+1) + "Content-Length: 2\r\n\r\n{}", errMissingContentLength},
		{"too many header lines", "Content-Length: 2\r\n" + strings.Repeat("X-H: 1\r\n", maxHeaderLines) + "\r\n{}", errTooManyHeaders},
		{"oversize single line", strings.Repeat("{", maxLSPMessageSize+2) + "\n", errLineTooLong},
		{"eof", "", io.EOF},
	}
	for _, c := range cases {
		srv := NewServer(strings.NewReader(c.input), &bytes.Buffer{}, "v1.0.0")
		payload, err := srv.readFramedMessage()
		if err == nil {
			t.Errorf("%s: expected an error, got payload %q", c.name, payload)
			continue
		}
		if c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%s: expected %v, got %v", c.name, c.want, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	oversizeRun := NewServer(strings.NewReader(fmt.Sprintf("Content-Length: %d\r\n\r\n{}", maxLSPMessageSize+1)), &bytes.Buffer{}, "v1.0.0")
	if err := oversizeRun.Run(ctx); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("Run must fail on an oversize frame instead of desynchronising, got %v", err)
	}
}

func TestLSP_Negative_ResponseWriteFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := NewServer(strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`+"\n"), failingWriter{}, "v1.0.0")
	if err := srv.Run(ctx); err == nil || !strings.Contains(err.Error(), "failed writing") {
		t.Errorf("Run must surface write failures, got %v", err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

// =========================================================================
// Analyzer rule tests
// =========================================================================

func TestLSP_Rules_HISS01RecursionIgnoresDelegation(t *testing.T) {
	code := `package sample

type inner struct{}

func (inner) Close() error { return nil }

type wrapper struct{ in inner }

// Close delegates to a same-named method on another value: not recursion.
func (w *wrapper) Close() error { return w.in.Close() }

func (w *wrapper) Loop() { w.Loop() }

func plain() { plain() }

func shadow() { other.plain() }
`
	diags := analyze(t, "file:///r.go", code)
	if got := countDiagnostics(diags, "HISS-01", ""); got != 2 {
		t.Errorf("expected exactly the two real recursions, got %d: %+v", got, diags)
	}
	for _, d := range diags {
		if d.Code == "HISS-01" && !strings.Contains(d.Message, `"Loop"`) && !strings.Contains(d.Message, `"plain"`) {
			t.Errorf("false positive recursion diagnostic: %+v", d)
		}
	}
}

func TestLSP_Rules_HISS02LoopForms(t *testing.T) {
	code := `package sample

import "net/http"

func loops(urls []string) {
	for _, u := range urls {
		http.Get(u)
	}
	for i := 0; ; i++ {
		http.Post(urls[i], "", nil)
	}
	for i := 0; i < len(urls); i++ {
		http.Head(urls[i])
	}
	for range urls {
	}
}
`
	diags := analyze(t, "file:///l.go", code)
	if got := countDiagnostics(diags, "HISS-02", "Unbounded loop"); got != 1 {
		t.Errorf("for i := 0; ; i++ must be the only unbounded loop, got %d: %+v", got, diags)
	}
	if got := countDiagnostics(diags, "HISS-02", "inside loop"); got != 3 {
		t.Errorf("I/O in range, unbounded and bounded loops must all be flagged, got %d: %+v", got, diags)
	}
}

func TestLSP_Rules_HISS07BlankAssignments(t *testing.T) {
	code := `package sample

import "os"

func f() error {
	_, err := os.Stat("x")
	if err != nil {
		return err
	}
	_, ok := map[string]int{}["k"]
	if !ok {
		return nil
	}
	_ = os.Remove("x")
	_, _ = os.Stat("y")
	if statErr := err; statErr != nil {
	}
	return nil
}
`
	diags := analyze(t, "file:///b.go", code)
	if got := countDiagnostics(diags, "HISS-07", "blank identifier"); got != 2 {
		t.Errorf("only the fully discarded results are HISS-07, got %d: %+v", got, diags)
	}
	if got := countDiagnostics(diags, "HISS-07", "Empty error branch"); got != 1 {
		t.Errorf("empty error branch must be flagged once, got %d: %+v", got, diags)
	}

	testDiags := analyze(t, "file:///b_test.go", code+"\nfunc g() { panic(1) }\n")
	if got := countDiagnostics(testDiags, "HISS-07", ""); got != 0 {
		t.Errorf("_test.go documents are exempt from HISS-07, got %+v", testDiags)
	}
}

func TestLSP_Rules_TruncationIsReported(t *testing.T) {
	srv := NewServer(&bytes.Buffer{}, &bytes.Buffer{}, "v1.0.0")
	code := "package sample\n\nfunc f() {\n\tpanic(1)\n}\n"

	full, err := srv.AnalyzeGoSource("file:///t.go", code)
	if err != nil || countDiagnostics(full, "TRUNCATED", "") != 0 || countDiagnostics(full, "HISS-07", "panic") != 1 {
		t.Fatalf("untruncated analysis must find the panic without a truncation warning, got %+v err=%v", full, err)
	}

	srv.maxASTNodes = 4
	partial, err := srv.AnalyzeGoSource("file:///t.go", code)
	if err != nil {
		t.Fatal(err)
	}
	if countDiagnostics(partial, "TRUNCATED", "") != 1 || partial[0].Severity != 2 {
		t.Errorf("a capped walk must publish a warning diagnostic first, got %+v", partial)
	}
	if countDiagnostics(partial, "HISS-07", "panic") != 0 {
		t.Errorf("the capped walk should not have reached the panic, got %+v", partial)
	}
}

// =========================================================================
// Boundary Tests
// =========================================================================

func TestLSP_Boundary_EmptyFileAndLOCThreshold(t *testing.T) {
	srv := NewServer(&bytes.Buffer{}, &bytes.Buffer{}, "v1.0.0")

	emptyDiags, err := srv.AnalyzeGoSource("file:///empty.go", "   \n")
	if err != nil || len(emptyDiags) != 0 {
		t.Errorf("expected 0 diagnostics for an empty file, got %+v err=%v", emptyDiags, err)
	}

	// Exactly 75 LOC (func line + 73 body lines + closing brace) passes.
	var exact75 strings.Builder
	exact75.WriteString("package sample\n\nfunc Exact75() {\n")
	for i := 0; i < 73; i++ {
		exact75.WriteString("\tprintln(1)\n")
	}
	exact75.WriteString("}\n")
	if diags := analyze(t, "file:///exact75.go", exact75.String()); countDiagnostics(diags, "HISS-04", "LOC") != 0 {
		t.Errorf("function with exactly 75 LOC must not trigger the LOC limit, got %+v", diags)
	}

	var over75 strings.Builder
	over75.WriteString("package sample\n\nfunc Over75() {\n")
	for i := 0; i < 74; i++ {
		over75.WriteString("\tprintln(1)\n")
	}
	over75.WriteString("}\n")
	if diags := analyze(t, "file:///over75.go", over75.String()); countDiagnostics(diags, "HISS-04", "LOC") != 1 {
		t.Errorf("function with 76 LOC must trigger the LOC limit, got %+v", diags)
	}
}

func TestLSP_Boundary_StatementThreshold(t *testing.T) {
	// 50 statements pass, 51 fail; the LOC rule stays silent because of the width.
	var fifty strings.Builder
	fifty.WriteString("package sample\n\nfunc Fifty() {\n")
	for i := 0; i < 25; i++ {
		fifty.WriteString("\tprintln(1); println(2)\n")
	}
	fifty.WriteString("}\n")
	diags := analyze(t, "file:///fifty.go", fifty.String())
	if countDiagnostics(diags, "HISS-04", "statement count") != 0 || countDiagnostics(diags, "HISS-04", "LOC") != 0 {
		t.Errorf("50 statements must pass, got %+v", diags)
	}

	fiftyOne := strings.Replace(fifty.String(), "}\n", "\tprintln(3)\n}\n", 1)
	diags = analyze(t, "file:///fiftyone.go", fiftyOne)
	if countDiagnostics(diags, "HISS-04", "statement count") != 1 || countDiagnostics(diags, "HISS-04", "LOC") != 0 {
		t.Errorf("51 statements must trigger only the statement rule, got %+v", diags)
	}
}

func TestLSP_Boundary_BoundedLoopAndEmptyChange(t *testing.T) {
	boundedLoop := "package sample\n\nfunc BoundedLoop() {\n\tfor i := 0; i < 100; i++ {\n\t\tprintln(i)\n\t}\n}\n"
	if diags := analyze(t, "file:///loop.go", boundedLoop); countDiagnostics(diags, "HISS-02", "") != 0 {
		t.Errorf("bounded loop incorrectly flagged, got %+v", diags)
	}

	srv := NewServer(&bytes.Buffer{}, &bytes.Buffer{}, "v1.0.0")
	ctx := context.Background()
	mustHandle(t, srv, ctx, didOpen("file:///c.go", goFunc("f", 1)))
	resp, notifs := mustHandle(t, srv, ctx, `{"jsonrpc":"2.0","method":"textDocument/didChange","params":{"textDocument":{"uri":"file:///c.go","version":2},"contentChanges":[]}}`)
	if resp != nil || len(notifs) != 0 {
		t.Errorf("didChange without content changes must be a no-op, got resp=%+v notifs=%d", resp, len(notifs))
	}
	srv.docMu.RLock()
	ver := srv.versions["file:///c.go"]
	srv.docMu.RUnlock()
	if ver != 1 {
		t.Errorf("an empty didChange must not bump the stored version, got %d", ver)
	}
}

func TestLSP_Boundary_ContentLengthAtLimit(t *testing.T) {
	body := strings.Repeat("x", maxLSPMessageSize)
	srv := NewServer(strings.NewReader(fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)), &bytes.Buffer{}, "v1.0.0")
	payload, err := srv.readFramedMessage()
	if err != nil || len(payload) != maxLSPMessageSize {
		t.Errorf("a frame of exactly maxLSPMessageSize must be accepted, got len=%d err=%v", len(payload), err)
	}

	zero := NewServer(strings.NewReader("Content-Length: 0\r\n\r\n"), &bytes.Buffer{}, "v1.0.0")
	if payload, err = zero.readFramedMessage(); err != nil || len(payload) != 0 {
		t.Errorf("a zero-length frame must be accepted, got len=%d err=%v", len(payload), err)
	}
}
