package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"strconv"
	"strings"
	"sync"
)

const (
	maxLSPMessageSize = 4 * 1024 * 1024 // 4 MB: bounds every frame, header line and body
	maxHeaderLines    = 100
	// defaultMaxASTNodes bounds the nodes inspected per document (HISS-02). A document
	// that exceeds it is reported as truncated instead of silently passing.
	defaultMaxASTNodes = 1 << 20
	maxFuncLOC         = 75
	maxFuncStatements  = 50
)

var (
	errLineTooLong          = errors.New("header line exceeds the maximum message size")
	errMissingContentLength = errors.New("missing Content-Length header")
	errTooManyHeaders       = errors.New("too many header lines")
)

// Position in a text document (0-indexed).
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range in a text document.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Diagnostic represents an LSP diagnostic item.
type Diagnostic struct {
	Range    Range  `json:"range"`
	Severity int    `json:"severity"` // 1: Error, 2: Warning
	Code     string `json:"code,omitempty"`
	Source   string `json:"source,omitempty"`
	Message  string `json:"message"`
}

// PublishDiagnosticsParams represents textDocument/publishDiagnostics payload.
type PublishDiagnosticsParams struct {
	URI         string       `json:"uri"`
	Version     *int         `json:"version,omitempty"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// JSONRPCRequest represents an incoming LSP message.
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// JSONRPCResponse represents an outgoing LSP response.
type JSONRPCResponse struct {
	JSONRPC string
	ID      any
	Result  any
	Error   *JSONRPCError
}

// MarshalJSON emits a JSON-RPC 2.0 response frame: `id` is always present (null for a
// parse error), a success carries `result` even when it is null, and an error carries
// `error` and never `result`. An omitted `result` is not a valid response and
// vscode-jsonrpc discards it, leaving the client's request pending.
func (r JSONRPCResponse) MarshalJSON() ([]byte, error) {
	if r.Error != nil {
		return json.Marshal(struct {
			JSONRPC string        `json:"jsonrpc"`
			ID      any           `json:"id"`
			Error   *JSONRPCError `json:"error"`
		}{r.JSONRPC, r.ID, r.Error})
	}
	return json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		ID      any    `json:"id"`
		Result  any    `json:"result"`
	}{r.JSONRPC, r.ID, r.Result})
}

// JSONRPCNotification represents a notification without ID.
type JSONRPCNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// JSONRPCError holds structured error details.
type JSONRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Server is the JSON-RPC 2.0 LSP daemon.
type Server struct {
	in          *bufio.Reader
	out         io.Writer
	outMu       sync.Mutex
	docMu       sync.RWMutex
	documents   map[string]string
	versions    map[string]int
	isShutdown  bool
	isExited    bool
	version     string
	maxASTNodes int
}

// NewServer instantiates an LSP server instance.
func NewServer(in io.Reader, out io.Writer, version string) *Server {
	if version == "" {
		version = "v1.0.0"
	}
	return &Server{
		in:          bufio.NewReaderSize(in, 64*1024),
		out:         out,
		documents:   make(map[string]string),
		versions:    make(map[string]int),
		version:     version,
		maxASTNodes: defaultMaxASTNodes,
	}
}

// HandleMessage handles an unmarshaled JSON-RPC 2.0 request or notification.
func (s *Server) HandleMessage(ctx context.Context, raw []byte) (*JSONRPCResponse, []JSONRPCNotification, error) {
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	default:
	}

	var req JSONRPCRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      nil,
			Error: &JSONRPCError{
				Code:    -32700,
				Message: fmt.Sprintf("Parse error: %v", err),
			},
		}, nil, nil
	}

	return s.dispatchRequest(ctx, req)
}

func (s *Server) dispatchRequest(ctx context.Context, req JSONRPCRequest) (*JSONRPCResponse, []JSONRPCNotification, error) {
	if s.isShutdown && req.Method != "exit" {
		// A notification carries no id and must never be answered.
		if req.ID == nil {
			return nil, nil, nil
		}
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &JSONRPCError{Code: -32600, Message: "Server is shutdown"},
		}, nil, nil
	}

	switch req.Method {
	case "initialize":
		return s.handleInitialize(req), nil, nil
	case "initialized":
		return nil, nil, nil
	case "shutdown":
		s.isShutdown = true
		return &JSONRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: nil}, nil, nil
	case "exit":
		s.isExited = true
		return nil, nil, nil
	default:
		return s.dispatchDocumentRequest(ctx, req)
	}
}

// dispatchDocumentRequest routes textDocument notifications. Malformed params never
// end the session: a request receives -32602, a notification is dropped, and only a
// cancelled context is returned as an error.
func (s *Server) dispatchDocumentRequest(ctx context.Context, req JSONRPCRequest) (*JSONRPCResponse, []JSONRPCNotification, error) {
	var notifs []JSONRPCNotification
	var err error
	switch req.Method {
	case "textDocument/didOpen":
		notifs, err = s.handleDidOpen(ctx, req.Params)
	case "textDocument/didChange":
		notifs, err = s.handleDidChange(ctx, req.Params)
	case "textDocument/didSave":
		notifs, err = s.handleDidSave(ctx, req.Params)
	case "textDocument/didClose":
		notifs, err = s.handleDidClose(req.Params)
	default:
		return s.methodNotFound(req), nil, nil
	}
	if err == nil {
		return nil, notifs, nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, nil, err
	}
	if req.ID == nil {
		return nil, nil, nil
	}
	return &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Error:   &JSONRPCError{Code: -32602, Message: fmt.Sprintf("Invalid params: %v", err)},
	}, nil, nil
}

func (s *Server) methodNotFound(req JSONRPCRequest) *JSONRPCResponse {
	if req.ID == nil {
		return nil
	}
	return &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Error:   &JSONRPCError{Code: -32601, Message: fmt.Sprintf("Method not found: %s", req.Method)},
	}
}

func (s *Server) handleInitialize(req JSONRPCRequest) *JSONRPCResponse {
	return &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"capabilities": map[string]any{
				"textDocumentSync": 1, // Full synchronization
			},
			"serverInfo": map[string]string{
				"name":    "standards-lsp",
				"version": s.version,
			},
		},
	}
}

func (s *Server) handleDidOpen(ctx context.Context, paramsRaw json.RawMessage) ([]JSONRPCNotification, error) {
	var params struct {
		TextDocument struct {
			URI        string `json:"uri"`
			LanguageID string `json:"languageId"`
			Version    int    `json:"version"`
			Text       string `json:"text"`
		} `json:"textDocument"`
	}
	if err := json.Unmarshal(paramsRaw, &params); err != nil {
		return nil, fmt.Errorf("invalid didOpen params: %w", err)
	}

	s.storeDocument(params.TextDocument.URI, params.TextDocument.Text, params.TextDocument.Version)
	return s.publishDiagnosticsFor(ctx, params.TextDocument.URI, params.TextDocument.Text, params.TextDocument.Version)
}

func (s *Server) handleDidChange(ctx context.Context, paramsRaw json.RawMessage) ([]JSONRPCNotification, error) {
	var params struct {
		TextDocument struct {
			URI     string `json:"uri"`
			Version int    `json:"version"`
		} `json:"textDocument"`
		ContentChanges []struct {
			Text string `json:"text"`
		} `json:"contentChanges"`
	}
	if err := json.Unmarshal(paramsRaw, &params); err != nil {
		return nil, fmt.Errorf("invalid didChange params: %w", err)
	}
	if len(params.ContentChanges) == 0 {
		return nil, nil
	}

	newText := params.ContentChanges[len(params.ContentChanges)-1].Text
	s.storeDocument(params.TextDocument.URI, newText, params.TextDocument.Version)
	return s.publishDiagnosticsFor(ctx, params.TextDocument.URI, newText, params.TextDocument.Version)
}

func (s *Server) handleDidSave(ctx context.Context, paramsRaw json.RawMessage) ([]JSONRPCNotification, error) {
	var params struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	if err := json.Unmarshal(paramsRaw, &params); err != nil {
		return nil, fmt.Errorf("invalid didSave params: %w", err)
	}

	s.docMu.RLock()
	text := s.documents[params.TextDocument.URI]
	ver := s.versions[params.TextDocument.URI]
	s.docMu.RUnlock()

	return s.publishDiagnosticsFor(ctx, params.TextDocument.URI, text, ver)
}

// handleDidClose forgets the document and clears its diagnostics in the editor, so the
// daemon's memory is bounded by the set of open documents rather than by every
// document ever opened.
func (s *Server) handleDidClose(paramsRaw json.RawMessage) ([]JSONRPCNotification, error) {
	var params struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	if err := json.Unmarshal(paramsRaw, &params); err != nil {
		return nil, fmt.Errorf("invalid didClose params: %w", err)
	}

	s.docMu.Lock()
	delete(s.documents, params.TextDocument.URI)
	delete(s.versions, params.TextDocument.URI)
	s.docMu.Unlock()

	return []JSONRPCNotification{{
		JSONRPC: "2.0",
		Method:  "textDocument/publishDiagnostics",
		Params: PublishDiagnosticsParams{
			URI:         params.TextDocument.URI,
			Diagnostics: []Diagnostic{},
		},
	}}, nil
}

func (s *Server) storeDocument(uri, text string, version int) {
	s.docMu.Lock()
	s.documents[uri] = text
	s.versions[uri] = version
	s.docMu.Unlock()
}

func (s *Server) publishDiagnosticsFor(ctx context.Context, uri, text string, ver int) ([]JSONRPCNotification, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	diags, err := s.AnalyzeGoSource(uri, text)
	if err != nil {
		return nil, err
	}

	notif := JSONRPCNotification{
		JSONRPC: "2.0",
		Method:  "textDocument/publishDiagnostics",
		Params: PublishDiagnosticsParams{
			URI:         uri,
			Version:     &ver,
			Diagnostics: diags,
		},
	}
	return []JSONRPCNotification{notif}, nil
}

// AnalyzeGoSource parses Go code and checks for HISS violations. Test files keep the
// structural checks (HISS-01, HISS-02, HISS-04) but are exempt from the HISS-07
// error-handling rules, matching internal/hiss.
func (s *Server) AnalyzeGoSource(uri, code string) ([]Diagnostic, error) {
	if strings.TrimSpace(code) == "" {
		return []Diagnostic{}, nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, uri, code, parser.ParseComments)
	if err != nil {
		return s.diagnosticsFromParseError(err), nil
	}

	nodes, truncated := s.collectASTNodes(file)
	diags := make([]Diagnostic, 0, 8)
	if truncated {
		diags = append(diags, s.truncationDiagnostic(len(nodes)))
	}
	diags = append(diags, s.checkHISS04Complexity(fset, nodes)...)
	diags = append(diags, s.checkHISS01DAG(fset, nodes)...)
	diags = append(diags, s.checkHISS02BoundedLoops(fset, nodes)...)
	if !strings.HasSuffix(uri, "_test.go") {
		diags = append(diags, s.checkHISS07Errors(fset, nodes)...)
	}

	return diags, nil
}

func (s *Server) diagnosticsFromParseError(err error) []Diagnostic {
	return []Diagnostic{
		{
			Range: Range{
				Start: Position{Line: 0, Character: 0},
				End:   Position{Line: 0, Character: 1},
			},
			Severity: 1,
			Code:     "SYNTAX",
			Source:   "standards-lsp",
			Message:  fmt.Sprintf("Syntax error: %v", err),
		},
	}
}

func (s *Server) truncationDiagnostic(inspected int) Diagnostic {
	return Diagnostic{
		Range: Range{
			Start: Position{Line: 0, Character: 0},
			End:   Position{Line: 0, Character: 1},
		},
		Severity: 2,
		Code:     "TRUNCATED",
		Source:   "standards-lsp",
		Message:  fmt.Sprintf("Analysis truncated after %d AST nodes; HISS diagnostics for the rest of this document are incomplete", inspected),
	}
}

func (s *Server) newDiagnostic(fset *token.FileSet, node ast.Node, severity int, code, msg string) Diagnostic {
	start := fset.Position(node.Pos())
	end := fset.Position(node.End())
	return Diagnostic{
		Range: Range{
			Start: Position{Line: start.Line - 1, Character: start.Column - 1},
			End:   Position{Line: end.Line - 1, Character: end.Column - 1},
		},
		Severity: severity,
		Code:     code,
		Source:   "standards-lsp",
		Message:  msg,
	}
}

// checkHISS04Complexity verifies function LOC <= 75 and statements <= 50.
func (s *Server) checkHISS04Complexity(fset *token.FileSet, nodes []ast.Node) []Diagnostic {
	var diags []Diagnostic
	for i := 0; i < len(nodes); i++ {
		fn, ok := nodes[i].(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		loc := fset.Position(fn.End()).Line - fset.Position(fn.Pos()).Line + 1
		if loc > maxFuncLOC {
			diags = append(diags, s.newDiagnostic(fset, fn, 1, "HISS-04",
				fmt.Sprintf("Function %q length (%d LOC) exceeds HISS-04 limit of %d LOC", fn.Name.Name, loc, maxFuncLOC)))
		}

		if stmts := len(fn.Body.List); stmts > maxFuncStatements {
			diags = append(diags, s.newDiagnostic(fset, fn, 1, "HISS-04",
				fmt.Sprintf("Function %q statement count (%d) exceeds HISS-04 limit of %d", fn.Name.Name, stmts, maxFuncStatements)))
		}
	}
	return diags
}

// checkHISS01DAG verifies acyclic control flow and bans direct recursion.
func (s *Server) checkHISS01DAG(fset *token.FileSet, nodes []ast.Node) []Diagnostic {
	var diags []Diagnostic
	for i := 0; i < len(nodes); i++ {
		fn, ok := nodes[i].(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		recv := receiverName(fn)
		bodyNodes, _ := s.collectASTNodes(fn.Body)
		for j := 0; j < len(bodyNodes); j++ {
			call, isCall := bodyNodes[j].(*ast.CallExpr)
			if !isCall || !isCallToFunction(call.Fun, fn.Name.Name, recv) {
				continue
			}
			diags = append(diags, s.newDiagnostic(fset, call, 1, "HISS-01",
				fmt.Sprintf("Direct recursion detected in function %q; call graph must form an acyclic DAG (HISS-01)", fn.Name.Name)))
		}
	}
	return diags
}

// receiverName returns the bound receiver identifier of a method, or "" for a plain
// function or an unnamed receiver.
func receiverName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 || len(fn.Recv.List[0].Names) == 0 {
		return ""
	}
	return fn.Recv.List[0].Names[0].Name
}

// isCallToFunction reports whether a call targets the enclosing function itself: a bare
// identifier for a plain function, or recv.method for a method. A same-named method on
// any other value (the delegation idiom `return x.inner.Close()`) is not recursion.
func isCallToFunction(fun ast.Expr, fnName, recv string) bool {
	switch expr := fun.(type) {
	case *ast.Ident:
		return recv == "" && expr.Name == fnName
	case *ast.SelectorExpr:
		if recv == "" || expr.Sel.Name != fnName {
			return false
		}
		x, isIdent := expr.X.(*ast.Ident)
		return isIdent && x.Name == recv
	default:
		return false
	}
}

// checkHISS02BoundedLoops verifies loops have bounds and that loop bodies carry no
// deadline-free I/O; both for and range loops are inspected.
func (s *Server) checkHISS02BoundedLoops(fset *token.FileSet, nodes []ast.Node) []Diagnostic {
	var diags []Diagnostic
	for i := 0; i < len(nodes); i++ {
		switch loop := nodes[i].(type) {
		case *ast.ForStmt:
			if loop.Cond == nil {
				diags = append(diags, s.newDiagnostic(fset, loop, 1, "HISS-02",
					"Unbounded loop construct detected without statically verifiable scalar bound (HISS-02)"))
			}
			diags = append(diags, s.checkLoopIOCalls(fset, loop.Body)...)
		case *ast.RangeStmt:
			diags = append(diags, s.checkLoopIOCalls(fset, loop.Body)...)
		}
	}
	return diags
}

func (s *Server) checkLoopIOCalls(fset *token.FileSet, body *ast.BlockStmt) []Diagnostic {
	var diags []Diagnostic
	bNodes, _ := s.collectASTNodes(body)
	for i := 0; i < len(bNodes); i++ {
		call, ok := bNodes[i].(*ast.CallExpr)
		if !ok {
			continue
		}
		sel, isSel := call.Fun.(*ast.SelectorExpr)
		if !isSel {
			continue
		}
		pkgIdent, isPkg := sel.X.(*ast.Ident)
		if !isPkg || !isUnboundedIOCall(pkgIdent.Name, sel.Sel.Name) {
			continue
		}
		diags = append(diags, s.newDiagnostic(fset, call, 1, "HISS-02",
			fmt.Sprintf("I/O call %s.%s inside loop lacks mandatory context deadline (HISS-02)", pkgIdent.Name, sel.Sel.Name)))
	}
	return diags
}

func isUnboundedIOCall(pkg, method string) bool {
	if pkg == "http" && (method == "Get" || method == "Post" || method == "Head" || method == "PostForm") {
		return true
	}
	if pkg == "net" && (method == "Dial" || method == "DialTCP") {
		return true
	}
	return false
}

// checkHISS07Errors flags results discarded wholesale (`_ = f()`), empty error
// branches and panic calls. A partial discard such as `_, err := f()` keeps the checked
// value and is not reported.
func (s *Server) checkHISS07Errors(fset *token.FileSet, nodes []ast.Node) []Diagnostic {
	var diags []Diagnostic
	for i := 0; i < len(nodes); i++ {
		switch node := nodes[i].(type) {
		case *ast.AssignStmt:
			if allBlank(node.Lhs) {
				diags = append(diags, s.newDiagnostic(fset, node, 1, "HISS-07",
					"Unchecked return value assigned to blank identifier '_' (HISS-07)"))
			}
		case *ast.IfStmt:
			if node.Body != nil && len(node.Body.List) == 0 && isErrNotNil(node.Cond) {
				diags = append(diags, s.newDiagnostic(fset, node, 1, "HISS-07",
					"Empty error branch silently swallows the error (HISS-07)"))
			}
		case *ast.CallExpr:
			if ident, ok := node.Fun.(*ast.Ident); ok && ident.Name == "panic" {
				diags = append(diags, s.newDiagnostic(fset, node, 1, "HISS-07",
					"Direct panic invocation detected in code; error handling mandatory (HISS-07)"))
			}
		}
	}
	return diags
}

func allBlank(lhs []ast.Expr) bool {
	if len(lhs) == 0 {
		return false
	}
	for _, expr := range lhs {
		ident, ok := expr.(*ast.Ident)
		if !ok || ident.Name != "_" {
			return false
		}
	}
	return true
}

func isErrNotNil(cond ast.Expr) bool {
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok || bin.Op != token.NEQ {
		return false
	}
	lhs, lok := bin.X.(*ast.Ident)
	rhs, rok := bin.Y.(*ast.Ident)
	return lok && rok && strings.HasSuffix(strings.ToLower(lhs.Name), "err") && rhs.Name == "nil"
}

// collectASTNodes flattens the subtree under root in depth-first order, bounded by
// maxASTNodes (HISS-02). truncated reports that the bound was hit, so a caller never
// mistakes a partial inspection for a clean one.
func (s *Server) collectASTNodes(root ast.Node) (nodes []ast.Node, truncated bool) {
	if root == nil {
		return nil, false
	}
	limit := s.maxASTNodes
	if limit <= 0 {
		limit = defaultMaxASTNodes
	}
	nodes = make([]ast.Node, 0, 64)
	ast.Inspect(root, func(n ast.Node) bool {
		if n == nil {
			return true
		}
		if len(nodes) >= limit {
			truncated = true
			return false
		}
		nodes = append(nodes, n)
		return true
	})
	return nodes, truncated
}

// frame is one message read from the client, or the read error that ended the stream.
type frame struct {
	payload []byte
	err     error
}

// Run executes the stdio loop, processing framed JSON-RPC 2.0 messages until EOF,
// exit, or context cancellation. The blocking reads run in their own goroutine so a
// cancelled context (SIGINT/SIGTERM in main) ends the daemon even while it idles on
// stdin; the reader itself unblocks when the client closes the pipe.
func (s *Server) Run(ctx context.Context) error {
	frames := make(chan frame)
	go s.readFrames(ctx, frames)

	for !s.isExited && ctx.Err() == nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case f := <-frames:
			if f.err != nil {
				if errors.Is(f.err, io.EOF) {
					return nil
				}
				return fmt.Errorf("read error: %w", f.err)
			}
			if err := s.process(ctx, f.payload); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

// readFrames reads frames until the stream ends or ctx is cancelled and delivers them
// to the run loop; every frame is bounded by maxLSPMessageSize.
func (s *Server) readFrames(ctx context.Context, frames chan<- frame) {
	for ctx.Err() == nil {
		payload, err := s.readFramedMessage()
		select {
		case frames <- frame{payload: payload, err: err}:
		case <-ctx.Done():
			return
		}
		if err != nil {
			return
		}
	}
}

// process dispatches one message and writes its response and notifications.
func (s *Server) process(ctx context.Context, payload []byte) error {
	resp, notifs, err := s.HandleMessage(ctx, payload)
	if err != nil {
		return err
	}
	if resp != nil {
		if err := s.writeFramedMessage(resp); err != nil {
			return fmt.Errorf("failed writing response: %w", err)
		}
	}
	for _, n := range notifs {
		if err := s.writeFramedMessage(n); err != nil {
			return fmt.Errorf("failed writing notification: %w", err)
		}
	}
	return nil
}

// readFramedMessage reads one Content-Length framed message. A bare `{...}` line is
// accepted as a line-delimited message; both forms are bounded by maxLSPMessageSize
// and any oversize or malformed Content-Length is an error rather than a silent
// fall-through that would desynchronise the stream.
func (s *Server) readFramedMessage() ([]byte, error) {
	h := frameHeader{contentLength: -1}
	for headerLines := 0; headerLines < maxHeaderLines; headerLines++ {
		line, err := s.readLine()
		if err != nil {
			return nil, err
		}
		done, err := h.consume(strings.TrimRight(line, "\r\n"))
		if err != nil {
			return nil, err
		}
		if !done {
			continue
		}
		if h.inline != nil {
			return h.inline, nil
		}
		return s.readBody(h.contentLength)
	}
	if h.contentLength < 0 {
		return nil, errMissingContentLength
	}
	return nil, errTooManyHeaders
}

// frameHeader accumulates the header block of one message.
type frameHeader struct {
	contentLength int
	sawHeader     bool
	inline        []byte
}

// consume feeds one header line and reports whether the header block is complete.
func (h *frameHeader) consume(line string) (bool, error) {
	if line == "" {
		if h.contentLength >= 0 {
			return true, nil
		}
		if h.sawHeader {
			return false, errMissingContentLength
		}
		return false, nil // blank separator lines between messages are tolerated
	}
	if strings.HasPrefix(line, "{") {
		h.inline = []byte(line)
		return true, nil
	}
	h.sawHeader = true
	if !strings.HasPrefix(strings.ToLower(line), "content-length:") {
		return false, nil
	}
	val, err := parseContentLength(line)
	if err != nil {
		return false, err
	}
	h.contentLength = val
	return false, nil
}

func parseContentLength(line string) (int, error) {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return 0, fmt.Errorf("malformed Content-Length header %q", line)
	}
	val, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil || val < 0 {
		return 0, fmt.Errorf("malformed Content-Length header %q", line)
	}
	if val > maxLSPMessageSize {
		return 0, fmt.Errorf("Content-Length %d exceeds the maximum message size %d", val, maxLSPMessageSize)
	}
	return val, nil
}

// readLine reads one line bounded by maxLSPMessageSize, however large the underlying
// buffer is.
func (s *Server) readLine() (string, error) {
	var buf []byte
	for len(buf) <= maxLSPMessageSize {
		chunk, err := s.in.ReadSlice('\n')
		buf = append(buf, chunk...)
		if len(buf) > maxLSPMessageSize+1 { // payload plus its newline
			return "", errLineTooLong
		}
		if err == nil {
			return string(buf), nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return "", err
		}
	}
	return "", errLineTooLong
}

func (s *Server) readBody(contentLength int) ([]byte, error) {
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(s.in, body); err != nil {
		return nil, fmt.Errorf("failed to read full body: %w", err)
	}
	return body, nil
}

func (s *Server) writeFramedMessage(v any) error {
	s.outMu.Lock()
	defer s.outMu.Unlock()

	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("failed marshaling message: %w", err)
	}

	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	if _, err := io.WriteString(s.out, header); err != nil {
		return fmt.Errorf("failed writing header: %w", err)
	}
	if _, err := s.out.Write(data); err != nil {
		return fmt.Errorf("failed writing body: %w", err)
	}
	return nil
}
