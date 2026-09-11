package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"
)

// =========================================================================
// Positive Tests
// =========================================================================

func TestLSP_Positive_ClientSession(t *testing.T) {
	ctx := context.Background()
	inBuf := &bytes.Buffer{}
	outBuf := &bytes.Buffer{}
	srv := NewServer(inBuf, outBuf, "v1.0.0")

	testLSPInitAndOpen(t, srv, ctx)
	testLSPChangeWithViolations(t, srv, ctx)
	testLSPSaveAndShutdown(t, srv, ctx)
}

func testLSPInitAndOpen(t *testing.T, srv *Server, ctx context.Context) {
	initReq := []byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	resp, _, err := srv.HandleMessage(ctx, initReq)
	if err != nil || resp == nil || resp.Error != nil {
		t.Fatalf("initialize failed: err=%v resp=%+v", err, resp)
	}
	resMap, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected result map, got %T", resp.Result)
	}
	caps := resMap["capabilities"].(map[string]any)
	if caps["textDocumentSync"] != 1 {
		t.Errorf("expected textDocumentSync=1, got %v", caps["textDocumentSync"])
	}

	initializedReq := []byte(`{"jsonrpc":"2.0","method":"initialized","params":{}}`)
	_, notifs, err := srv.HandleMessage(ctx, initializedReq)
	if err != nil || len(notifs) != 0 {
		t.Fatalf("initialized failed: err=%v notifs=%d", err, len(notifs))
	}

	cleanCode := "package sample\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n"
	didOpenPayload := fmt.Sprintf(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///sample.go","languageId":"go","version":1,"text":%q}}}`, cleanCode)

	resp, notifs, err = srv.HandleMessage(ctx, []byte(didOpenPayload))
	if err != nil || resp != nil || len(notifs) != 1 {
		t.Fatalf("didOpen failed: err=%v resp=%+v notifs=%d", err, resp, len(notifs))
	}
	params := notifs[0].Params.(PublishDiagnosticsParams)
	if len(params.Diagnostics) != 0 {
		t.Errorf("expected 0 diagnostics for clean code, got %d", len(params.Diagnostics))
	}
}

func testLSPChangeWithViolations(t *testing.T, srv *Server, ctx context.Context) {
	var longFn strings.Builder
	longFn.WriteString("package sample\n\nimport \"net/http\"\n\nfunc BadEverything() {\n")
	for i := 0; i < 80; i++ {
		longFn.WriteString(fmt.Sprintf("\tx%d := %d\n", i, i))
	}
	longFn.WriteString("\tBadEverything()\n")
	longFn.WriteString("\tfor iter := 0; iter < 10; iter++ {\n\t\thttp.Get(\"http://example.com\")\n\t}\n")
	longFn.WriteString("\t_ = http.Get(\"http://example.com\")\n")
	longFn.WriteString("\tpanic(\"illegal\")\n")
	longFn.WriteString("}\n")

	didChangePayload := fmt.Sprintf(`{"jsonrpc":"2.0","method":"textDocument/didChange","params":{"textDocument":{"uri":"file:///sample.go","version":2},"contentChanges":[{"text":%q}]}}`, longFn.String())

	_, notifs, err := srv.HandleMessage(ctx, []byte(didChangePayload))
	if err != nil || len(notifs) != 1 {
		t.Fatalf("didChange error: %v notifs=%d", err, len(notifs))
	}

	changeParams := notifs[0].Params.(PublishDiagnosticsParams)
	foundHISS01, foundHISS04, foundHISS07 := false, false, false
	for _, d := range changeParams.Diagnostics {
		switch d.Code {
		case "HISS-01":
			foundHISS01 = true
		case "HISS-04":
			foundHISS04 = true
		case "HISS-07":
			foundHISS07 = true
		}
	}
	if !foundHISS01 || !foundHISS04 || !foundHISS07 {
		t.Errorf("expected HISS violations: hiss01=%v hiss04=%v hiss07=%v", foundHISS01, foundHISS04, foundHISS07)
	}
}

func testLSPSaveAndShutdown(t *testing.T, srv *Server, ctx context.Context) {
	didSavePayload := `{"jsonrpc":"2.0","method":"textDocument/didSave","params":{"textDocument":{"uri":"file:///sample.go"}}}`
	_, notifs, err := srv.HandleMessage(ctx, []byte(didSavePayload))
	if err != nil || len(notifs) != 1 {
		t.Fatalf("didSave error: %v notifs=%d", err, len(notifs))
	}

	shutdownReq := []byte(`{"jsonrpc":"2.0","id":2,"method":"shutdown","params":null}`)
	resp, _, err := srv.HandleMessage(ctx, shutdownReq)
	if err != nil || resp == nil || resp.Error != nil || resp.Result != nil {
		t.Errorf("shutdown error: err=%v resp=%+v", err, resp)
	}
}

func TestLSP_Positive_FramedTransport(t *testing.T) {
	clientRead, serverWrite := io.Pipe()
	serverRead, clientWrite := io.Pipe()
	defer clientRead.Close()
	defer serverWrite.Close()
	defer serverRead.Close()
	defer clientWrite.Close()

	srv := NewServer(serverRead, serverWrite, "v1.0.0")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- srv.Run(ctx)
	}()

	reader := bufio.NewReader(clientRead)
	testFramedInit(t, clientWrite, reader)
	testFramedShutdown(t, clientWrite, reader, errChan)
}

func testFramedInit(t *testing.T, clientWrite io.Writer, reader *bufio.Reader) {
	initMsg := `{"jsonrpc":"2.0","id":100,"method":"initialize","params":{}}`
	frame := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(initMsg), initMsg)
	if _, err := io.WriteString(clientWrite, frame); err != nil {
		t.Fatalf("failed to write frame: %v", err)
	}

	header, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(header, "Content-Length:") {
		t.Fatalf("expected Content-Length header, got: %q err=%v", header, err)
	}
	_, _ = reader.ReadString('\n')
	lenStr := strings.TrimSpace(strings.TrimPrefix(header, "Content-Length:"))
	n, parseErr := strconv.Atoi(lenStr)
	if parseErr != nil {
		t.Fatalf("failed parsing length %q: %v", lenStr, parseErr)
	}

	body := make([]byte, n)
	if _, err := io.ReadFull(reader, body); err != nil {
		t.Fatalf("failed reading full body: %v", err)
	}
	if !strings.Contains(string(body), `"standards-lsp"`) {
		t.Errorf("expected response to contain standards-lsp, got: %s", string(body))
	}
}

func testFramedShutdown(t *testing.T, clientWrite io.WriteCloser, reader *bufio.Reader, errChan <-chan error) {
	shutdownMsg := `{"jsonrpc":"2.0","id":101,"method":"shutdown"}`
	frameShutdown := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(shutdownMsg), shutdownMsg)
	if _, err := io.WriteString(clientWrite, frameShutdown); err != nil {
		t.Fatalf("failed writing shutdown: %v", err)
	}

	header, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed reading shutdown header: %v", err)
	}
	_, _ = reader.ReadString('\n')
	lenStr := strings.TrimSpace(strings.TrimPrefix(header, "Content-Length:"))
	n, parseErr := strconv.Atoi(lenStr)
	if parseErr == nil && n > 0 {
		body := make([]byte, n)
		_, _ = io.ReadFull(reader, body)
	}

	exitMsg := `{"jsonrpc":"2.0","method":"exit"}`
	frameExit := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(exitMsg), exitMsg)
	_, _ = io.WriteString(clientWrite, frameExit)
	clientWrite.Close()

	select {
	case <-errChan:
	case <-time.After(2 * time.Second):
		t.Errorf("timed out waiting for server exit")
	}
}

// =========================================================================
// Negative Tests
// =========================================================================

func TestLSP_Negative_MalformedAndUnknownMethods(t *testing.T) {
	ctx := context.Background()
	srv := NewServer(&bytes.Buffer{}, &bytes.Buffer{}, "v1.0.0")

	// 1. Malformed JSON
	badJSON := []byte(`{"jsonrpc":"2.0", invalid-json`)
	resp, notifs, err := srv.HandleMessage(ctx, badJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil || resp.Error == nil || resp.Error.Code != -32700 {
		t.Errorf("expected code -32700 for parse error, got: %+v", resp)
	}
	if len(notifs) != 0 {
		t.Errorf("expected 0 notifications on parse error")
	}

	// 2. Unknown method with ID
	unknownReq := []byte(`{"jsonrpc":"2.0","id":42,"method":"custom/unknown","params":{}}`)
	resp, _, err = srv.HandleMessage(ctx, unknownReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil || resp.Error == nil || resp.Error.Code != -32601 {
		t.Errorf("expected code -32601 for unknown method, got: %+v", resp)
	}

	// 3. Syntax error in didOpen
	syntaxErrorGo := `package sample
func Broken( {
`
	didOpenPayload := fmt.Sprintf(`{
		"jsonrpc":"2.0",
		"method":"textDocument/didOpen",
		"params":{
			"textDocument":{
				"uri":"file:///broken.go",
				"languageId":"go",
				"version":1,
				"text":%q
			}
		}
	}`, syntaxErrorGo)

	resp, notifs, err = srv.HandleMessage(ctx, []byte(didOpenPayload))
	if err != nil {
		t.Fatalf("didOpen error: %v", err)
	}
	if len(notifs) != 1 {
		t.Fatalf("expected 1 notification for syntax error, got %d", len(notifs))
	}
	diags := notifs[0].Params.(PublishDiagnosticsParams).Diagnostics
	if len(diags) != 1 || diags[0].Code != "SYNTAX" {
		t.Errorf("expected SYNTAX diagnostic, got: %+v", diags)
	}

	// 4. Request after shutdown
	srv.isShutdown = true
	postShutdownReq := []byte(`{"jsonrpc":"2.0","id":99,"method":"initialize"}`)
	resp, _, _ = srv.HandleMessage(ctx, postShutdownReq)
	if resp == nil || resp.Error == nil || resp.Error.Code != -32600 {
		t.Errorf("expected code -32600 for request after shutdown, got: %+v", resp)
	}
}

// =========================================================================
// Boundary Tests
// =========================================================================

func TestLSP_Boundary_EmptyFileAndLOCThreshold(t *testing.T) {
	srv := NewServer(&bytes.Buffer{}, &bytes.Buffer{}, "v1.0.0")

	// 1. Boundary: Empty file
	emptyDiags, err := srv.AnalyzeGoSource("file:///empty.go", "")
	if err != nil {
		t.Fatalf("empty file analysis error: %v", err)
	}
	if len(emptyDiags) != 0 {
		t.Errorf("expected 0 diagnostics for empty file, got %d", len(emptyDiags))
	}

	// 2. Boundary: Exactly 75 LOC function (should PASS HISS-04)
	var exact75 strings.Builder
	exact75.WriteString("package sample\n\nfunc Exact75() {\n")
	// Lines 1-3 are package, empty, func declaration.
	// We need total LOC of function to be exactly 75: lines 3 to 77.
	for i := 0; i < 73; i++ {
		exact75.WriteString("\t_ = 1\n")
	}
	exact75.WriteString("}\n")

	diags75, err := srv.AnalyzeGoSource("file:///exact75.go", exact75.String())
	if err != nil {
		t.Fatalf("exact75 analysis failed: %v", err)
	}
	hasHISS04 := false
	for _, d := range diags75 {
		if d.Code == "HISS-04" && strings.Contains(d.Message, "LOC") {
			hasHISS04 = true
		}
	}
	if hasHISS04 {
		t.Errorf("function with <= 75 LOC should not trigger HISS-04 LOC limit")
	}

	// 3. Boundary: 76 LOC function (should FAIL HISS-04)
	var over75 strings.Builder
	over75.WriteString("package sample\n\nfunc Over75() {\n")
	for i := 0; i < 75; i++ {
		over75.WriteString("\t_ = 1\n")
	}
	over75.WriteString("}\n")

	diags76, err := srv.AnalyzeGoSource("file:///over75.go", over75.String())
	if err != nil {
		t.Fatalf("over75 analysis failed: %v", err)
	}
	foundOver75 := false
	for _, d := range diags76 {
		if d.Code == "HISS-04" && strings.Contains(d.Message, "LOC") {
			foundOver75 = true
		}
	}
	if !foundOver75 {
		t.Errorf("expected function > 75 LOC to trigger HISS-04")
	}

	// 4. Boundary: Bounded loop with scalar condition should not trigger HISS-02
	boundedLoop := `package sample

func BoundedLoop() {
	for i := 0; i < 100; i++ {
		_ = i
	}
}
`
	loopDiags, err := srv.AnalyzeGoSource("file:///loop.go", boundedLoop)
	if err != nil {
		t.Fatalf("boundedLoop analysis failed: %v", err)
	}
	for _, d := range loopDiags {
		if d.Code == "HISS-02" && strings.Contains(d.Message, "Unbounded loop") {
			t.Errorf("bounded loop incorrectly flagged as unbounded loop")
		}
	}
}
