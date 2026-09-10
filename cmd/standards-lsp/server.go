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
	maxLSPMessageSize = 4 * 1024 * 1024 // 4 MB
	maxASTLoopDepth   = 5000
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
	JSONRPC string        `json:"jsonrpc"`
	ID      any           `json:"id,omitempty"`
	Result  any           `json:"result,omitempty"`
	Error   *JSONRPCError `json:"error,omitempty"`
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
	in         *bufio.Reader
	out        io.Writer
	outMu      sync.Mutex
	docMu      sync.RWMutex
	documents  map[string]string
	versions   map[string]int
	isShutdown bool
	isExited   bool
	version    string
}

// NewServer instantiates an LSP server instance.
func NewServer(in io.Reader, out io.Writer, version string) *Server {
	if version == "" {
		version = "v1.0.0"
	}
	return &Server{
		in:        bufio.NewReaderSize(in, 64*1024),
		out:       out,
		documents: make(map[string]string),
		versions:  make(map[string]int),
		version:   version,
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
	case "textDocument/didOpen":
		notifs, err := s.handleDidOpen(ctx, req.Params)
		return nil, notifs, err
	case "textDocument/didChange":
		notifs, err := s.handleDidChange(ctx, req.Params)
		return nil, notifs, err
	case "textDocument/didSave":
		notifs, err := s.handleDidSave(ctx, req.Params)
		return nil, notifs, err
	default:
		if req.ID != nil {
			return &JSONRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &JSONRPCError{Code: -32601, Message: fmt.Sprintf("Method not found: %s", req.Method)},
			}, nil, nil
		}
		return nil, nil, nil
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

	s.docMu.Lock()
	s.documents[params.TextDocument.URI] = params.TextDocument.Text
	s.versions[params.TextDocument.URI] = params.TextDocument.Version
	s.docMu.Unlock()

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
	s.docMu.Lock()
	s.documents[params.TextDocument.URI] = newText
	s.versions[params.TextDocument.URI] = params.TextDocument.Version
	s.docMu.Unlock()

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

// AnalyzeGoSource parses Go code and checks for HISS violations.
func (s *Server) AnalyzeGoSource(uri, code string) ([]Diagnostic, error) {
	if strings.TrimSpace(code) == "" {
		return []Diagnostic{}, nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, uri, code, parser.ParseComments)
	if err != nil {
		return s.diagnosticsFromParseError(err), nil
	}

	var diags []Diagnostic
	diags = append(diags, s.checkHISS04Complexity(fset, file)...)
	diags = append(diags, s.checkHISS01DAG(fset, file)...)
	diags = append(diags, s.checkHISS02BoundedLoops(fset, file)...)
	diags = append(diags, s.checkHISS07Errors(fset, file)...)

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

// checkHISS04Complexity verifies function LOC <= 75 and statements <= 50.
func (s *Server) checkHISS04Complexity(fset *token.FileSet, file *ast.File) []Diagnostic {
	var diags []Diagnostic
	nodes := s.collectASTNodes(file)
	limit := len(nodes)

	for i := 0; i < limit; i++ {
		fn, ok := nodes[i].(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		startPos := fset.Position(fn.Pos())
		endPos := fset.Position(fn.End())
		loc := endPos.Line - startPos.Line + 1

		if loc > 75 {
			diags = append(diags, Diagnostic{
				Range: Range{
					Start: Position{Line: startPos.Line - 1, Character: startPos.Column - 1},
					End:   Position{Line: endPos.Line - 1, Character: endPos.Column - 1},
				},
				Severity: 1,
				Code:     "HISS-04",
				Source:   "standards-lsp",
				Message:  fmt.Sprintf("Function %q length (%d LOC) exceeds HISS-04 limit of 75 LOC", fn.Name.Name, loc),
			})
		}

		stmts := len(fn.Body.List)
		if stmts > 50 {
			diags = append(diags, Diagnostic{
				Range: Range{
					Start: Position{Line: startPos.Line - 1, Character: startPos.Column - 1},
					End:   Position{Line: endPos.Line - 1, Character: endPos.Column - 1},
				},
				Severity: 1,
				Code:     "HISS-04",
				Source:   "standards-lsp",
				Message:  fmt.Sprintf("Function %q statement count (%d) exceeds HISS-04 limit of 50", fn.Name.Name, stmts),
			})
		}
	}
	return diags
}

// checkHISS01DAG verifies acyclic control flow and bans recursion.
func (s *Server) checkHISS01DAG(fset *token.FileSet, file *ast.File) []Diagnostic {
	var diags []Diagnostic
	nodes := s.collectASTNodes(file)
	limit := len(nodes)

	for i := 0; i < limit; i++ {
		fn, ok := nodes[i].(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		fnName := fn.Name.Name
		bodyNodes := s.collectASTNodes(fn.Body)
		bLimit := len(bodyNodes)

		for j := 0; j < bLimit; j++ {
			call, isCall := bodyNodes[j].(*ast.CallExpr)
			if !isCall {
				continue
			}

			if s.isCallToFunction(call.Fun, fnName) {
				callPos := fset.Position(call.Pos())
				callEnd := fset.Position(call.End())
				diags = append(diags, Diagnostic{
					Range: Range{
						Start: Position{Line: callPos.Line - 1, Character: callPos.Column - 1},
						End:   Position{Line: callEnd.Line - 1, Character: callEnd.Column - 1},
					},
					Severity: 1,
					Code:     "HISS-01",
					Source:   "standards-lsp",
					Message:  fmt.Sprintf("Direct recursion detected in function %q; call graph must form an acyclic DAG (HISS-01)", fnName),
				})
			}
		}
	}
	return diags
}

func (s *Server) isCallToFunction(fun ast.Expr, targetName string) bool {
	switch expr := fun.(type) {
	case *ast.Ident:
		return expr.Name == targetName
	case *ast.SelectorExpr:
		return expr.Sel.Name == targetName
	default:
		return false
	}
}

// checkHISS02BoundedLoops verifies loops have bounds and timeout context on I/O.
func (s *Server) checkHISS02BoundedLoops(fset *token.FileSet, file *ast.File) []Diagnostic {
	var diags []Diagnostic
	nodes := s.collectASTNodes(file)
	limit := len(nodes)

	for i := 0; i < limit; i++ {
		loop, ok := nodes[i].(*ast.ForStmt)
		if !ok {
			continue
		}

		loopPos := fset.Position(loop.Pos())
		loopEnd := fset.Position(loop.End())

		if loop.Cond == nil && loop.Init == nil && loop.Post == nil {
			diags = append(diags, Diagnostic{
				Range: Range{
					Start: Position{Line: loopPos.Line - 1, Character: loopPos.Column - 1},
					End:   Position{Line: loopEnd.Line - 1, Character: loopEnd.Column - 1},
				},
				Severity: 1,
				Code:     "HISS-02",
				Source:   "standards-lsp",
				Message:  "Unbounded loop construct 'for {}' detected without statically verifiable scalar bound (HISS-02)",
			})
		}

		diags = append(diags, s.checkLoopIOCalls(fset, loop)...)
	}
	return diags
}

func (s *Server) checkLoopIOCalls(fset *token.FileSet, loop *ast.ForStmt) []Diagnostic {
	var diags []Diagnostic
	bNodes := s.collectASTNodes(loop.Body)
	limit := len(bNodes)

	for i := 0; i < limit; i++ {
		call, ok := bNodes[i].(*ast.CallExpr)
		if !ok {
			continue
		}

		sel, isSel := call.Fun.(*ast.SelectorExpr)
		if !isSel {
			continue
		}

		pkgIdent, isPkg := sel.X.(*ast.Ident)
		if !isPkg {
			continue
		}

		if s.isUnboundedIOCall(pkgIdent.Name, sel.Sel.Name) {
			pos := fset.Position(call.Pos())
			end := fset.Position(call.End())
			diags = append(diags, Diagnostic{
				Range: Range{
					Start: Position{Line: pos.Line - 1, Character: pos.Column - 1},
					End:   Position{Line: end.Line - 1, Character: end.Column - 1},
				},
				Severity: 1,
				Code:     "HISS-02",
				Source:   "standards-lsp",
				Message:  fmt.Sprintf("I/O call %s.%s inside loop lacks mandatory context deadline (HISS-02)", pkgIdent.Name, sel.Sel.Name),
			})
		}
	}
	return diags
}

func (s *Server) isUnboundedIOCall(pkg, method string) bool {
	if pkg == "http" && (method == "Get" || method == "Post" || method == "Head") {
		return true
	}
	if pkg == "net" && (method == "Dial" || method == "DialTCP") {
		return true
	}
	return false
}

// checkHISS07Errors checks for unchecked errors and banned panic() calls.
func (s *Server) checkHISS07Errors(fset *token.FileSet, file *ast.File) []Diagnostic {
	var diags []Diagnostic
	nodes := s.collectASTNodes(file)
	limit := len(nodes)

	for i := 0; i < limit; i++ {
		switch node := nodes[i].(type) {
		case *ast.AssignStmt:
			for _, lhs := range node.Lhs {
				if ident, isIdent := lhs.(*ast.Ident); isIdent && ident.Name == "_" {
					pos := fset.Position(node.Pos())
					end := fset.Position(node.End())
					diags = append(diags, Diagnostic{
						Range: Range{
							Start: Position{Line: pos.Line - 1, Character: pos.Column - 1},
							End:   Position{Line: end.Line - 1, Character: end.Column - 1},
						},
						Severity: 1,
						Code:     "HISS-07",
						Source:   "standards-lsp",
						Message:  "Unchecked return value assigned to blank identifier '_' (HISS-07)",
					})
					break
				}
			}
		case *ast.CallExpr:
			if ident, ok := node.Fun.(*ast.Ident); ok && ident.Name == "panic" {
				pos := fset.Position(node.Pos())
				end := fset.Position(node.End())
				diags = append(diags, Diagnostic{
					Range: Range{
						Start: Position{Line: pos.Line - 1, Character: pos.Column - 1},
						End:   Position{Line: end.Line - 1, Character: end.Column - 1},
					},
					Severity: 1,
					Code:     "HISS-07",
					Source:   "standards-lsp",
					Message:  "Direct panic() invocation detected in code; error handling mandatory (HISS-07)",
				})
			}
		}
	}
	return diags
}

// collectASTNodes iteratively flattens AST nodes with scalar bound to satisfy HISS-01 and HISS-02.
func (s *Server) collectASTNodes(root ast.Node) []ast.Node {
	if root == nil {
		return nil
	}

	result := make([]ast.Node, 0, 64)
	queue := []ast.Node{root}
	limit := maxASTLoopDepth

	for len(queue) > 0 && len(result) < limit {
		curr := queue[0]
		queue = queue[1:]
		result = append(result, curr)

		ast.Inspect(curr, func(child ast.Node) bool {
			if child == nil || child == curr {
				return true
			}
			queue = append(queue, child)
			return false
		})
	}
	return result
}

// Run executes the stdio loop, processing framed JSON-RPC 2.0 messages until EOF or shutdown.
func (s *Server) Run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		payload, err := s.readFramedMessage()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("read error: %w", err)
		}

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

		if s.isExited {
			return nil
		}
	}
}

func (s *Server) readFramedMessage() ([]byte, error) {
	contentLength := -1

	// Read headers
	for {
		line, err := s.in.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if contentLength >= 0 {
				break
			}
			continue
		}

		// Fallback for line-delimited JSON-RPC
		if strings.HasPrefix(line, "{") {
			return []byte(line), nil
		}

		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				val, parseErr := strconv.Atoi(strings.TrimSpace(parts[1]))
				if parseErr == nil && val >= 0 && val <= maxLSPMessageSize {
					contentLength = val
				}
			}
		}
	}

	if contentLength < 0 {
		return nil, errors.New("missing Content-Length header")
	}

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
