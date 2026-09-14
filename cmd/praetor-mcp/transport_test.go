package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---- stdio ---------------------------------------------------------------------------------

// syncBuffer is a bytes.Buffer safe for concurrent Write and Len.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

// runStdioWith feeds input to runStdio and returns its output and error.
func runStdioWith(t *testing.T, srv *Server, input string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var out bytes.Buffer
	err := srv.runStdio(ctx, strings.NewReader(input), &out)
	return out.String(), err
}

// decodeLines parses newline-delimited JSON-RPC responses.
func decodeLines(t *testing.T, out string) []JSONRPCResponse {
	t.Helper()
	var responses []JSONRPCResponse
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		var resp JSONRPCResponse
		if err := json.Unmarshal([]byte(lines[i]), &resp); err != nil {
			t.Fatalf("response line %d is not JSON: %v: %s", i, err, lines[i])
		}
		responses = append(responses, resp)
	}
	return responses
}

func TestStdio_Positive_RequestResponse(t *testing.T) {
	srv, _ := newFixtureServer(t)
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n" +
		"\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\r\n" +
		`{"jsonrpc":"2.0","id":2,"method":"ping"}` + "\n" +
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`

	out, err := runStdioWith(t, srv, input)
	if err != nil {
		t.Fatalf("runStdio: %v", err)
	}
	responses := decodeLines(t, out)
	if len(responses) != 3 {
		t.Fatalf("got %d responses, want 3 (notification and blank line produce none):\n%s", len(responses), out)
	}
	for i, id := range []float64{1, 2, 3} {
		if responses[i].Error != nil || responses[i].ID != id {
			t.Errorf("response %d = %+v, want id %v without error", i, responses[i], id)
		}
	}
}

func TestStdio_Negative_ParseErrorsKeepSessionAlive(t *testing.T) {
	srv, _ := newFixtureServer(t)
	oversized := strings.Repeat("x", maxScannerBuffer+1)
	input := "not json\n" + oversized + "\n" + `{"jsonrpc":"2.0","id":9,"method":"ping"}` + "\n"

	out, err := runStdioWith(t, srv, input)
	if err != nil {
		t.Fatalf("runStdio must survive bad lines, got: %v", err)
	}
	responses := decodeLines(t, out)
	if len(responses) != 3 {
		t.Fatalf("got %d responses, want 3:\n%s", len(responses), out)
	}
	if responses[0].Error == nil || responses[0].Error.Code != -32700 {
		t.Errorf("invalid JSON: %+v, want -32700", responses[0])
	}
	if responses[1].Error == nil || responses[1].Error.Code != -32700 || !strings.Contains(responses[1].Error.Message, "line limit") {
		t.Errorf("oversized line: %+v, want -32700 line limit", responses[1])
	}
	if responses[2].Error != nil || responses[2].ID != float64(9) {
		t.Errorf("ping after bad lines: %+v", responses[2])
	}
}

func TestStdio_Boundary_CancelWithIdleOpenPipe(t *testing.T) {
	srv, _ := newFixtureServer(t)
	pr, pw := io.Pipe()
	t.Cleanup(func() {
		if err := pw.Close(); err != nil {
			t.Logf("close pipe: %v", err)
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	out := &syncBuffer{}
	go func() { done <- srv.runStdio(ctx, pr, out) }()

	if _, err := io.WriteString(pw, `{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for i := 0; i < 500 && out.Len() == 0 && time.Now().Before(deadline); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if out.Len() == 0 {
		t.Fatal("no response to ping before cancellation")
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("runStdio returned %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runStdio ignored cancellation while stdin stayed open")
	}

	// Boundary: exactly the line limit is accepted, one byte more is not.
	exact := strings.Repeat("x", maxScannerBuffer)
	line, err := readBoundedLine(bufio.NewReaderSize(strings.NewReader(exact+"\n"), stdioReadChunk))
	if err != nil || line.tooLong || len(line.data) != maxScannerBuffer {
		t.Errorf("line at the limit: tooLong=%v len=%d err=%v", line.tooLong, len(line.data), err)
	}
	line, err = readBoundedLine(bufio.NewReaderSize(strings.NewReader(exact+"y\n"), stdioReadChunk))
	if err != nil || !line.tooLong {
		t.Errorf("line over the limit: tooLong=%v err=%v", line.tooLong, err)
	}
	line, err = readBoundedLine(bufio.NewReaderSize(strings.NewReader("tail-without-newline"), stdioReadChunk))
	if !errors.Is(err, io.EOF) || string(line.data) != "tail-without-newline" {
		t.Errorf("unterminated last line: %q err=%v", line.data, err)
	}
}

// ---- HTTP ------------------------------------------------------------------------------------

// waitBound polls until the server reports its listen address.
func waitBound(t *testing.T, srv *Server, errCh <-chan error) string {
	t.Helper()
	for i := 0; i < 500; i++ {
		if addr := srv.BoundAddr(); addr != "" {
			return addr
		}
		select {
		case err := <-errCh:
			t.Fatalf("server exited before binding: %v", err)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("server did not bind within 5s")
	return ""
}

// doJSON posts one JSON-RPC body and returns the response (caller closes the body).
func doJSON(t *testing.T, client *http.Client, url, body string, mutate func(*http.Request)) *http.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if mutate != nil {
		mutate(req)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	return resp
}

func closeBody(t *testing.T, resp *http.Response) {
	t.Helper()
	if err := resp.Body.Close(); err != nil {
		t.Errorf("close body: %v", err)
	}
}

func TestHTTP_Positive_RunHTTPRealListener(t *testing.T) {
	srv, _ := newFixtureServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.RunHTTP(ctx, "127.0.0.1:0") }()
	addr := waitBound(t, srv, errCh)
	client := &http.Client{Timeout: 10 * time.Second}

	resp := doJSON(t, client, "http://"+addr+"/", `{"jsonrpc":"2.0","id":100,"method":"ping"}`, nil)
	var rpc JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	closeBody(t, resp)
	if resp.StatusCode != http.StatusOK || rpc.Error != nil || rpc.ID != float64(100) {
		t.Errorf("ping over HTTP: status %d, %+v", resp.StatusCode, rpc)
	}

	notif := doJSON(t, client, "http://"+addr+"/", `{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil)
	closeBody(t, notif)
	if notif.StatusCode != http.StatusAccepted {
		t.Errorf("notification status = %d, want 202", notif.StatusCode)
	}

	hctx, hcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer hcancel()
	hreq, err := http.NewRequestWithContext(hctx, http.MethodGet, "http://"+addr+"/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	hres, err := client.Do(hreq)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	closeBody(t, hres)
	if hres.StatusCode != http.StatusOK {
		t.Errorf("health status = %d", hres.StatusCode)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("RunHTTP returned %v after cancellation", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunHTTP did not shut down")
	}
}

func TestHTTP_Negative_TransportGuards(t *testing.T) {
	srv, _ := newFixtureServer(t)
	ts := httptest.NewServer(srv.httpHandler())
	defer ts.Close()
	client := ts.Client()
	ping := `{"jsonrpc":"2.0","id":1,"method":"ping"}`

	gctx, gcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer gcancel()
	getReq, err := http.NewRequestWithContext(gctx, http.MethodGet, ts.URL+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	getRes, err := client.Do(getReq)
	if err != nil {
		t.Fatal(err)
	}
	closeBody(t, getRes)
	if getRes.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET / status = %d, want 405", getRes.StatusCode)
	}

	cases := []struct {
		name   string
		body   string
		mutate func(*http.Request)
		status int
	}{
		{"text/plain simple request", ping, func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, http.StatusUnsupportedMediaType},
		{"foreign origin", ping, func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, http.StatusForbidden},
		{"rebound host", ping, func(r *http.Request) { r.Host = "evil.example:8080" }, http.StatusForbidden},
		{"oversized body", strings.Repeat(" ", maxScannerBuffer+1), nil, http.StatusRequestEntityTooLarge},
		{"invalid json", "{", nil, http.StatusBadRequest},
		{"loopback origin", ping, func(r *http.Request) { r.Header.Set("Origin", "http://localhost:3000") }, http.StatusOK},
		{"json with charset", ping, func(r *http.Request) { r.Header.Set("Content-Type", "application/json; charset=utf-8") }, http.StatusOK},
	}
	for _, tc := range cases {
		resp := doJSON(t, client, ts.URL+"/", tc.body, tc.mutate)
		closeBody(t, resp)
		if resp.StatusCode != tc.status {
			t.Errorf("%s: status = %d, want %d", tc.name, resp.StatusCode, tc.status)
		}
	}

	resp := doJSON(t, client, ts.URL+"/", "{", nil)
	var rpc JSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil {
		t.Fatalf("decode parse error: %v", err)
	}
	closeBody(t, resp)
	if rpc.Error == nil || rpc.Error.Code != -32700 {
		t.Errorf("invalid JSON body must carry a -32700 error, got %+v", rpc)
	}
}

func TestHTTP_Negative_BearerTokenAndOrigins(t *testing.T) {
	root := newFixtureRepo(t)
	srv, err := NewServerWithOptions(ServerOptions{RootDir: root, Version: "v", AuthToken: "s3cret", AllowedOrigins: []string{"https://ide.example"}})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.httpHandler())
	defer ts.Close()
	client := ts.Client()
	ping := `{"jsonrpc":"2.0","id":1,"method":"ping"}`

	cases := []struct {
		name   string
		mutate func(*http.Request)
		status int
	}{
		{"missing token", nil, http.StatusUnauthorized},
		{"wrong token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer nope") }, http.StatusUnauthorized},
		{"basic scheme", func(r *http.Request) { r.Header.Set("Authorization", "Basic s3cret") }, http.StatusUnauthorized},
		{"correct token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer s3cret") }, http.StatusOK},
		{"allowed origin", func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer s3cret")
			r.Header.Set("Origin", "https://ide.example/")
		}, http.StatusOK},
		{"other origin", func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer s3cret")
			r.Header.Set("Origin", "https://other.example")
		}, http.StatusForbidden},
	}
	for _, tc := range cases {
		resp := doJSON(t, client, ts.URL+"/", ping, tc.mutate)
		closeBody(t, resp)
		if resp.StatusCode != tc.status {
			t.Errorf("%s: status = %d, want %d", tc.name, resp.StatusCode, tc.status)
		}
	}
}

func TestHTTP_Boundary_BindPolicy(t *testing.T) {
	srv, _ := newFixtureServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.RunHTTP(ctx, "0.0.0.0:0"); !errors.Is(err, ErrUnauthenticatedExposure) {
		t.Errorf("non-loopback bind without token: got %v, want ErrUnauthenticatedExposure", err)
	}
	if err := srv.RunHTTP(ctx, "not-an-address"); err == nil {
		t.Error("invalid address accepted")
	}
	if err := srv.RunSSE(ctx, ":0"); !errors.Is(err, ErrUnauthenticatedExposure) {
		t.Errorf("wildcard SSE bind without token: got %v, want ErrUnauthenticatedExposure", err)
	}
	for host, want := range map[string]bool{"127.0.0.1": true, "localhost": true, "::1": true, "[::1]": true, "10.0.0.5": false, "example.com": false, "": false} {
		if got := isLoopbackHost(host); got != want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}

// ---- SSE -------------------------------------------------------------------------------------

// sseEvent is one parsed server-sent event.
type sseEvent struct {
	name string
	data string
}

// readSSEEvent reads frames until a complete named event arrives (comments are skipped).
func readSSEEvent(t *testing.T, reader *bufio.Reader) sseEvent {
	t.Helper()
	var ev sseEvent
	for i := 0; i < 64; i++ {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE frame: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "event: "):
			ev.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			ev.data = strings.TrimPrefix(line, "data: ")
		case line == "" && ev.name != "":
			return ev
		}
	}
	t.Fatal("no complete SSE event within 64 frames")
	return ev
}

func TestSSE_Positive_MessagesFlowOverTheStream(t *testing.T) {
	srv, _ := newFixtureServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- srv.RunSSE(ctx, "127.0.0.1:0") }()
	addr := waitBound(t, srv, errCh)
	client := &http.Client{}

	sctx, scancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer scancel()
	streamReq, err := http.NewRequestWithContext(sctx, http.MethodGet, "http://"+addr+"/sse", nil)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := client.Do(streamReq)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer closeBody(t, stream)
	if ct := stream.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("stream content type = %q", ct)
	}
	reader := bufio.NewReader(stream.Body)

	endpoint := readSSEEvent(t, reader)
	if endpoint.name != "endpoint" || !strings.HasPrefix(endpoint.data, "/messages?sessionId=") {
		t.Fatalf("first event = %+v, want endpoint", endpoint)
	}
	if srv.sessions.count() != 1 {
		t.Errorf("open sessions = %d, want 1", srv.sessions.count())
	}

	post := doJSON(t, client, "http://"+addr+endpoint.data, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`, nil)
	closeBody(t, post)
	if post.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /messages status = %d, want 202", post.StatusCode)
	}
	msg := readSSEEvent(t, reader)
	if msg.name != "message" || !strings.Contains(msg.data, `"protocolVersion":"2024-11-05"`) {
		t.Fatalf("message event = %+v, want initialize result", msg)
	}

	notif := doJSON(t, client, "http://"+addr+endpoint.data, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil)
	closeBody(t, notif)
	if notif.StatusCode != http.StatusAccepted {
		t.Errorf("notification status = %d, want 202", notif.StatusCode)
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("RunSSE returned %v after cancellation", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunSSE did not shut down while a stream was open")
	}
}

func TestSSE_Negative_SessionValidation(t *testing.T) {
	srv, _ := newFixtureServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ts := httptest.NewServer(srv.sseHandler(ctx))
	defer ts.Close()
	client := ts.Client()
	ping := `{"jsonrpc":"2.0","id":1,"method":"ping"}`

	cases := []struct {
		name   string
		url    string
		status int
	}{
		{"missing sessionId", ts.URL + "/messages", http.StatusBadRequest},
		{"unknown sessionId", ts.URL + "/messages?sessionId=deadbeef", http.StatusNotFound},
		{"POST to stream endpoint", ts.URL + "/sse", http.StatusMethodNotAllowed},
	}
	for _, tc := range cases {
		resp := doJSON(t, client, tc.url, ping, nil)
		closeBody(t, resp)
		if resp.StatusCode != tc.status {
			t.Errorf("%s: status = %d, want %d", tc.name, resp.StatusCode, tc.status)
		}
	}

	gctx, gcancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer gcancel()
	getReq, err := http.NewRequestWithContext(gctx, http.MethodGet, ts.URL+"/messages", nil)
	if err != nil {
		t.Fatal(err)
	}
	getRes, err := client.Do(getReq)
	if err != nil {
		t.Fatal(err)
	}
	closeBody(t, getRes)
	if getRes.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /messages status = %d, want 405", getRes.StatusCode)
	}
}

func TestSSE_Boundary_RegistryLimitsAndDelivery(t *testing.T) {
	reg := newSSERegistry()
	opened := make([]*sseSession, 0, maxSSESessions)
	for i := 0; i < maxSSESessions; i++ {
		session, err := reg.open()
		if err != nil {
			t.Fatalf("open session %d: %v", i, err)
		}
		opened = append(opened, session)
	}
	if _, err := reg.open(); !errors.Is(err, ErrSSESessionLimit) {
		t.Errorf("session %d: got %v, want ErrSSESessionLimit", maxSSESessions+1, err)
	}
	reg.close(opened[0].id)
	if _, err := reg.open(); err != nil {
		t.Errorf("open after close: %v", err)
	}
	if _, ok := reg.lookup(opened[0].id); ok {
		t.Error("closed session still resolvable")
	}
	if _, ok := reg.lookup(""); ok {
		t.Error("empty session id resolvable")
	}

	// Delivery gives up once the per-session queue is full and nobody drains it.
	full := &sseSession{id: "full", events: make(chan []byte, 1)}
	if !deliverSSE(full, []byte("first"), 10*time.Millisecond) {
		t.Error("first delivery into an empty queue failed")
	}
	if deliverSSE(full, []byte("second"), 10*time.Millisecond) {
		t.Error("delivery into a full undrained queue succeeded")
	}
}
