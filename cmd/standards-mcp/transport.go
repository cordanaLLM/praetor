package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	// stdioReadChunk is the bufio read buffer; lines longer than it are assembled from
	// several chunks up to maxScannerBuffer.
	stdioReadChunk = 64 * 1024
	// maxLineChunks bounds the drain of an oversized line (HISS-02): 4096 chunks of
	// 64 KiB is 256 MiB, beyond which the peer is not a JSON-RPC client.
	maxLineChunks = 4096
	// maxStdioRequests preserves the existing 200,000-line ceiling for one
	// stdio session across both the reader and dispatcher (HISS-02).
	maxStdioRequests = 200_000
	// maxSSESessions bounds the concurrently open event streams.
	maxSSESessions = 64
	// maxSSEEvents bounds the events written on one stream (HISS-02).
	maxSSEEvents = 1_000_000
	// sseEventBuffer is the per-session queue between POST /messages and the stream.
	sseEventBuffer = 32
	// sseHeartbeat is the keep-alive comment interval on an idle stream.
	sseHeartbeat = 15 * time.Second
	// sseDeliverTimeout bounds how long a POST waits for room in the session queue.
	sseDeliverTimeout = 5 * time.Second
	// sessionIDBytes is the entropy of an SSE session identifier.
	sessionIDBytes = 16
	shutdownGrace  = 5 * time.Second
	// readHeaderTimeout and readTimeout bound the request side; the body is at most
	// maxScannerBuffer so 30 s is generous even on slow links.
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 30 * time.Second
	idleTimeout       = 120 * time.Second
)

var (
	// ErrLineTooLong is returned when a single stdio line exceeds the drain bound.
	ErrLineTooLong = errors.New("stdio line exceeds the drain bound")
	// ErrTooManyRequests is returned when a stdio session exceeds maxStdioRequests.
	ErrTooManyRequests = errors.New("stdio session exceeded the request bound")
	// ErrUnauthenticatedExposure is returned when http/sse would bind a non-loopback
	// address without a bearer token.
	ErrUnauthenticatedExposure = errors.New("refusing to bind a non-loopback address without -auth-token")
	// ErrSSESessionLimit is returned when maxSSESessions streams are already open.
	ErrSSESessionLimit = errors.New("sse session limit reached")
)

// ---- stdio -------------------------------------------------------------------------

// stdioLine is one newline-delimited request; tooLong marks a line that exceeded
// maxScannerBuffer and was drained instead of buffered.
type stdioLine struct {
	data    []byte
	tooLong bool
}

// RunStdio executes the stdio JSON-RPC 2.0 loop on the process's stdin/stdout.
func (s *Server) RunStdio(ctx context.Context) error {
	return s.runStdio(ctx, os.Stdin, os.Stdout)
}

// runStdio reads requests from in and writes responses to out until in is closed, ctx
// is cancelled, or the request bound is hit. Reading happens in a helper goroutine so
// a cancelled context is honoured even while the peer keeps the pipe open and idle.
func (s *Server) runStdio(ctx context.Context, in io.Reader, out io.Writer) error {
	lines := make(chan stdioLine)
	done := make(chan error, 1)
	go readStdioLines(ctx, in, lines, done)

	for i := 0; i < maxStdioRequests; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line, ok := <-lines:
			if !ok {
				return <-done
			}
			s.dispatchStdioLine(ctx, line, out)
		}
	}
	return ErrTooManyRequests
}

// readStdioLines feeds bounded lines into lines and reports the terminal error (nil on
// EOF) on done before closing lines.
func readStdioLines(ctx context.Context, in io.Reader, lines chan<- stdioLine, done chan<- error) {
	defer close(lines)
	reader := bufio.NewReaderSize(in, stdioReadChunk)

	for i := 0; i < maxStdioRequests; i++ {
		line, err := readBoundedLine(reader)
		if len(line.data) > 0 || line.tooLong {
			select {
			case lines <- line:
			case <-ctx.Done():
				done <- ctx.Err()
				return
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				done <- nil
			} else {
				done <- fmt.Errorf("stdio read: %w", err)
			}
			return
		}
	}
	done <- ErrTooManyRequests
}

// readBoundedLine reads one line, buffering at most maxScannerBuffer bytes and draining
// the remainder of an oversized line so the session can continue.
func readBoundedLine(reader *bufio.Reader) (stdioLine, error) {
	var line stdioLine
	for i := 0; i < maxLineChunks; i++ {
		chunk, err := reader.ReadSlice('\n')
		line = appendChunk(line, chunk)
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		line.data = bytes.TrimRight(line.data, "\r\n")
		return line, err
	}
	return stdioLine{tooLong: true}, ErrLineTooLong
}

// appendChunk accumulates chunk into line unless the line already overflowed. The
// terminating newline does not count towards the limit.
func appendChunk(line stdioLine, chunk []byte) stdioLine {
	if line.tooLong {
		return line
	}
	payload := len(chunk)
	if payload > 0 && chunk[payload-1] == '\n' {
		payload--
	}
	if len(line.data)+payload > maxScannerBuffer {
		return stdioLine{tooLong: true}
	}
	line.data = append(line.data, chunk...)
	return line
}

// dispatchStdioLine parses one line and writes the response, answering an oversized or
// malformed line with a JSON-RPC parse error instead of terminating the session.
func (s *Server) dispatchStdioLine(ctx context.Context, line stdioLine, out io.Writer) {
	if line.tooLong {
		writeStdioResponse(out, parseErrorResponse(fmt.Sprintf("Parse error: request exceeds the %d byte line limit", maxScannerBuffer)))
		return
	}
	if len(bytes.TrimSpace(line.data)) == 0 {
		return
	}

	var req JSONRPCRequest
	if err := json.Unmarshal(line.data, &req); err != nil {
		writeStdioResponse(out, parseErrorResponse("Parse error: invalid JSON-RPC payload"))
		return
	}

	if resp := s.HandleRequest(ctx, req); resp != nil {
		writeStdioResponse(out, resp)
	}
}

// parseErrorResponse builds the id-less -32700 response.
func parseErrorResponse(message string) *JSONRPCResponse {
	return &JSONRPCResponse{
		JSONRPC: "2.0",
		Error:   &JSONRPCError{Code: -32700, Message: message},
	}
}

// writeStdioResponse serializes a JSON-RPC response as one line on out.
func writeStdioResponse(out io.Writer, resp *JSONRPCResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to marshal response: %v\n", err)
		return
	}
	if _, err := fmt.Fprintf(out, "%s\n", data); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write response: %v\n", err)
	}
}

// ---- HTTP --------------------------------------------------------------------------

// RunHTTP serves JSON-RPC 2.0 requests over HTTP.
func (s *Server) RunHTTP(ctx context.Context, addr string) error {
	return s.serve(ctx, addr, s.httpHandler())
}

// httpHandler builds the plain HTTP transport: POST / carries one JSON-RPC message.
func (s *Server) httpHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/", s.handleJSONRPC)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !s.admit(w, r, false) {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if _, err := fmt.Fprintf(w, `{"status":"ok","server":"standards-mcp","version":%q}`, s.version); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write health response: %v\n", err)
	}
}

func (s *Server) handleJSONRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.admit(w, r, true) {
		return
	}
	req, ok := readJSONRPCBody(w, r)
	if !ok {
		return
	}

	resp := s.HandleRequest(r.Context(), req)
	if resp == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeJSON(w, resp)
}

// readJSONRPCBody reads a bounded request body and parses it; when it returns false the
// HTTP error has already been written.
func readJSONRPCBody(w http.ResponseWriter, r *http.Request) (JSONRPCRequest, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxScannerBuffer))
	if err != nil {
		http.Error(w, "Payload Too Large", http.StatusRequestEntityTooLarge)
		return JSONRPCRequest{}, false
	}

	var req JSONRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, parseErrorResponse("Parse error"))
		return JSONRPCRequest{}, false
	}
	return req, true
}

// writeJSON encodes an HTTP JSON payload without unchecked error suppression.
func writeJSON(w http.ResponseWriter, payload any) {
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		fmt.Fprintf(os.Stderr, "failed to encode response: %v\n", err)
	}
}

// ---- request admission (MCP transport security requirements) -----------------------

// admit enforces the transport security rules the MCP specification places on HTTP
// servers: the Host header must name the bound host (DNS rebinding), a browser Origin
// must be loopback or explicitly allowed (CSRF), the bearer token must match when one
// is configured, and JSON-RPC bodies must be declared as application/json (blocks
// preflight-free "simple" cross-site POSTs).
func (s *Server) admit(w http.ResponseWriter, r *http.Request, jsonBody bool) bool {
	if !s.hostAllowed(r.Host) {
		http.Error(w, "Forbidden: unexpected Host header", http.StatusForbidden)
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" && !s.originAllowed(origin) {
		http.Error(w, "Forbidden: origin not allowed", http.StatusForbidden)
		return false
	}
	if !s.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="standards-mcp"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return false
	}
	if jsonBody && !isJSONContentType(r.Header.Get("Content-Type")) {
		http.Error(w, "Unsupported Media Type: application/json required", http.StatusUnsupportedMediaType)
		return false
	}
	return true
}

// hostAllowed accepts loopback hosts and the host the server was started on.
func (s *Server) hostAllowed(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if isLoopbackHost(host) {
		return true
	}
	bound := s.bindHost()
	return bound != "" && strings.EqualFold(host, bound)
}

// isLoopbackHost reports whether host is localhost or a loopback IP literal.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// originAllowed accepts loopback origins and the configured allow-list.
func (s *Server) originAllowed(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	if isLoopbackHost(u.Hostname()) {
		return true
	}
	for i := 0; i < len(s.opts.AllowedOrigins); i++ {
		if strings.EqualFold(strings.TrimRight(s.opts.AllowedOrigins[i], "/"), strings.TrimRight(origin, "/")) {
			return true
		}
	}
	return false
}

// authorized checks the bearer token in constant time when one is configured.
func (s *Server) authorized(r *http.Request) bool {
	if s.opts.AuthToken == "" {
		return true
	}
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	presented := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	return subtle.ConstantTimeCompare([]byte(presented), []byte(s.opts.AuthToken)) == 1
}

// isJSONContentType reports whether the declared media type is application/json.
func isJSONContentType(header string) bool {
	mediaType, _, err := mime.ParseMediaType(header)
	return err == nil && mediaType == "application/json"
}

// ---- listener lifecycle --------------------------------------------------------------

// serve binds addr and serves handler until ctx is cancelled. A non-loopback bind is
// refused unless a bearer token is configured.
func (s *Server) serve(ctx context.Context, addr string, handler http.Handler) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid listen address %q: %w", addr, err)
	}
	if !isLoopbackHost(host) && s.opts.AuthToken == "" {
		return fmt.Errorf("%w: %s", ErrUnauthenticatedExposure, addr)
	}

	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	s.setBound(host, ln.Addr().String())

	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		// The write deadline must outlive the longest tool call, otherwise a mutating
		// tool completes its side effects and the client never learns the outcome.
		WriteTimeout: s.opts.ToolTimeout + shutdownGrace,
		IdleTimeout:  idleTimeout,
	}
	return s.runHTTPServer(ctx, server, ln)
}

// runHTTPServer serves on ln and shuts the server down gracefully when ctx ends.
func (s *Server) runHTTPServer(ctx context.Context, server *http.Server, ln net.Listener) error {
	errChan := make(chan error, 1)
	go func() {
		errChan <- server.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	case err := <-errChan:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// setBound records the configured host and the resolved listen address.
func (s *Server) setBound(host, addr string) {
	s.boundMu.Lock()
	defer s.boundMu.Unlock()
	s.boundHost = host
	s.boundAddr = addr
}

// bindHost returns the host the server was started on ("" before serve).
func (s *Server) bindHost() string {
	s.boundMu.Lock()
	defer s.boundMu.Unlock()
	return s.boundHost
}

// BoundAddr returns the resolved listen address once serve has bound it, else "".
func (s *Server) BoundAddr() string {
	s.boundMu.Lock()
	defer s.boundMu.Unlock()
	return s.boundAddr
}

// ---- SSE (MCP 2024-11-05 HTTP with SSE transport) ------------------------------------

// sseSession is one open event stream and the queue feeding it.
type sseSession struct {
	id     string
	events chan []byte
}

// sseRegistry maps session identifiers to open streams.
type sseRegistry struct {
	mu       sync.Mutex
	sessions map[string]*sseSession
}

func newSSERegistry() *sseRegistry {
	return &sseRegistry{sessions: make(map[string]*sseSession)}
}

// open registers a new session with a random identifier.
func (r *sseRegistry) open() (*sseSession, error) {
	raw := make([]byte, sessionIDBytes)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate session id: %w", err)
	}
	session := &sseSession{id: hex.EncodeToString(raw), events: make(chan []byte, sseEventBuffer)}

	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.sessions) >= maxSSESessions {
		return nil, ErrSSESessionLimit
	}
	r.sessions[session.id] = session
	return session, nil
}

func (r *sseRegistry) lookup(id string) (*sseSession, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, ok := r.sessions[id]
	return session, ok
}

func (r *sseRegistry) close(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, id)
}

func (r *sseRegistry) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sessions)
}

// RunSSE serves the MCP Server-Sent Events transport.
func (s *Server) RunSSE(ctx context.Context, addr string) error {
	return s.serve(ctx, addr, s.sseHandler(ctx))
}

// sseHandler builds the SSE transport: GET /sse opens the stream and announces the
// POST endpoint; POST /messages?sessionId=... submits a message whose response is
// pushed onto that stream as an SSE "message" event.
func (s *Server) sseHandler(ctx context.Context) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/sse", s.handleSSEEndpoint(ctx))
	mux.HandleFunc("/messages", s.handleSSEMessages)
	return mux
}

// handleSSEEndpoint opens an event stream for the life of the request.
func (s *Server) handleSSEEndpoint(ctx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		if !s.admit(w, r, false) {
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
			return
		}
		session, err := s.sessions.open()
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		defer s.sessions.close(session.id)

		// The stream outlives the server's write deadline by design; clear both
		// per-connection deadlines for this request only.
		clearDeadlines(w)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		if !writeSSEEvent(w, flusher, "endpoint", "/messages?sessionId="+session.id) {
			return
		}
		streamSSE(ctx, r.Context(), w, flusher, session)
	}
}

// clearDeadlines removes the read and write deadlines of a streaming response.
func clearDeadlines(w http.ResponseWriter) {
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		fmt.Fprintf(os.Stderr, "sse: clear write deadline: %v\n", err)
	}
	if err := rc.SetReadDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		fmt.Fprintf(os.Stderr, "sse: clear read deadline: %v\n", err)
	}
}

// streamSSE pumps queued responses and heartbeats onto the stream until either context
// ends or the event bound is reached.
func streamSSE(ctx, reqCtx context.Context, w http.ResponseWriter, flusher http.Flusher, session *sseSession) {
	ticker := time.NewTicker(sseHeartbeat)
	defer ticker.Stop()

	for i := 0; i < maxSSEEvents; i++ {
		select {
		case <-reqCtx.Done():
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !writeSSEComment(w, flusher) {
				return
			}
		case data := <-session.events:
			if !writeSSEEvent(w, flusher, "message", string(data)) {
				return
			}
		}
	}
}

// writeSSEEvent writes one event frame and flushes it; false means the peer is gone.
func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, event, data string) bool {
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

// writeSSEComment writes a keep-alive comment frame.
func writeSSEComment(w http.ResponseWriter, flusher http.Flusher) bool {
	if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
		return false
	}
	flusher.Flush()
	return true
}

// handleSSEMessages accepts a JSON-RPC message for an open session and routes the
// response onto that session's stream, answering the POST itself with 202 Accepted.
func (s *Server) handleSSEMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.admit(w, r, true) {
		return
	}
	sessionID := r.URL.Query().Get("sessionId")
	if sessionID == "" {
		http.Error(w, "Bad Request: sessionId query parameter is required", http.StatusBadRequest)
		return
	}
	session, ok := s.sessions.lookup(sessionID)
	if !ok {
		http.Error(w, "Not Found: unknown or expired session", http.StatusNotFound)
		return
	}
	req, ok := readJSONRPCBody(w, r)
	if !ok {
		return
	}

	if resp := s.HandleRequest(r.Context(), req); resp != nil {
		data, err := json.Marshal(resp)
		if err != nil {
			http.Error(w, "Internal Server Error: encode response", http.StatusInternalServerError)
			return
		}
		if !deliverSSE(session, data, sseDeliverTimeout) {
			http.Error(w, "Service Unavailable: session stream is not draining", http.StatusServiceUnavailable)
			return
		}
	}
	w.WriteHeader(http.StatusAccepted)
}

// deliverSSE queues data for the session's stream, giving up after timeout.
func deliverSSE(session *sseSession, data []byte, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case session.events <- data:
		return true
	case <-timer.C:
		return false
	}
}
