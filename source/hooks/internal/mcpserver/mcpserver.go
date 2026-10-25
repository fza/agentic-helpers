// Package mcpserver serves a set of tools to an MCP client over the stdio transport: newline-delimited
// JSON-RPC 2.0, one message per line, nothing on the output stream but protocol messages.
//
// It speaks the initialize-handshake era of the protocol. A client of the stateless era probes with
// server/discover first, receives method-not-found, and falls back to initialize, as the 2026-07-28
// revision prescribes for a legacy server on stdio.
package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
)

type Tool struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	Handle      func(ctx context.Context, arguments json.RawMessage) Result
}

type Result struct {
	Text    string
	IsError bool
}

var (
	ErrDuplicateTool      = errors.New("duplicate tool name")
	ErrInvalidInputSchema = errors.New("input schema is not valid JSON")
)

// The first entry is the latest, answered to a client asking for a version not in the list.
var supportedVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

const maxMessageBytes = 16 << 20

const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

var defaultInputSchema = json.RawMessage(`{"type":"object"}`)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type implementation struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type initializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    capabilities   `json:"capabilities"`
	ServerInfo      implementation `json:"serverInfo"`
	Instructions    string         `json:"instructions,omitempty"`
}

type capabilities struct {
	Tools toolsCapability `json:"tools"`
}

type toolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

type listedTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type toolsListResult struct {
	Tools []listedTool `json:"tools"`
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type textContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type callResult struct {
	Content []textContent `json:"content"`
	IsError bool          `json:"isError"`
}

type server struct {
	info         implementation
	instructions string
	tools        map[string]Tool
	listing      toolsListResult
	encoder      *json.Encoder
}

type line struct {
	content []byte
	err     error
}

func Serve(ctx context.Context, in io.Reader, out io.Writer, name string, version string, tools []Tool) error {
	return serve(ctx, in, out, name, version, "", tools)
}

// ServeNone answers the handshake with instructions and lists no tools, for a
// server whose piece is off: its tools would only invite calls.
func ServeNone(ctx context.Context, in io.Reader, out io.Writer, name string, version string, instructions string) error {
	return serve(ctx, in, out, name, version, instructions, nil)
}

func serve(ctx context.Context, in io.Reader, out io.Writer, name string, version string, instructions string,
	tools []Tool,
) error {
	served, err := newServer(out, name, version, tools)
	if err != nil {
		return err
	}

	served.instructions = instructions

	lines := make(chan line)
	readerCtx, stopReader := context.WithCancel(ctx)
	defer stopReader()

	// A read blocked on the input cannot be interrupted, so the reader runs apart from the loop and a
	// cancelled context returns at once; the reader exits on its next line or when the input closes.
	go readLines(readerCtx, in, lines)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case next, open := <-lines:
			// A select with both cases ready picks at random, so a line queued behind a cancellation
			// would otherwise still be handled.
			ctxErr := ctx.Err()
			if ctxErr != nil {
				return ctxErr
			}
			if !open {
				return nil
			}
			if next.err != nil {
				return fmt.Errorf("reading a message: %w", next.err)
			}

			handleErr := served.handle(ctx, next.content)
			if handleErr != nil {
				return handleErr
			}
		}
	}
}

func newServer(out io.Writer, name string, version string, tools []Tool) (*server, error) {
	served := &server{
		info:    implementation{Name: name, Version: version},
		tools:   make(map[string]Tool, len(tools)),
		listing: toolsListResult{Tools: make([]listedTool, 0, len(tools))},
		encoder: json.NewEncoder(out),
	}

	for _, tool := range tools {
		_, taken := served.tools[tool.Name]
		if taken {
			return nil, fmt.Errorf("registering tool %q: %w", tool.Name, ErrDuplicateTool)
		}

		schema := tool.InputSchema
		if len(schema) == 0 {
			schema = defaultInputSchema
		}
		if !json.Valid(schema) {
			return nil, fmt.Errorf("registering tool %q: %w", tool.Name, ErrInvalidInputSchema)
		}

		served.tools[tool.Name] = tool
		served.listing.Tools = append(served.listing.Tools, listedTool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: schema,
		})
	}

	return served, nil
}

func readLines(ctx context.Context, in io.Reader, lines chan<- line) {
	defer close(lines)

	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 0, 64<<10), maxMessageBytes)

	for scanner.Scan() {
		content := slices.Clone(scanner.Bytes())
		if len(content) == 0 {
			continue
		}

		select {
		case lines <- line{content: content}:
		case <-ctx.Done():
			return
		}
	}

	scanErr := scanner.Err()
	if scanErr == nil {
		return
	}

	select {
	case lines <- line{err: scanErr}:
	case <-ctx.Done():
	}
}

func (served *server) handle(ctx context.Context, message []byte) error {
	var incoming request
	decodeErr := json.Unmarshal(message, &incoming)
	if decodeErr != nil {
		var syntaxErr *json.SyntaxError
		if errors.As(decodeErr, &syntaxErr) {
			return served.fail(nil, codeParseError, "parse error")
		}

		return served.fail(nil, codeInvalidRequest, "invalid request")
	}

	if incoming.Method == "" {
		if len(incoming.Result) > 0 || len(incoming.Error) > 0 {
			return nil
		}

		return served.fail(incoming.ID, codeInvalidRequest, "invalid request")
	}

	if incoming.JSONRPC != "2.0" {
		return served.fail(incoming.ID, codeInvalidRequest, `invalid request: jsonrpc must be "2.0"`)
	}

	if len(incoming.ID) == 0 {
		return nil
	}

	switch incoming.Method {
	case "initialize":
		return served.answer(incoming.ID, served.initialize(incoming.Params))
	case "ping":
		return served.answer(incoming.ID, struct{}{})
	case "tools/list":
		return served.answer(incoming.ID, served.listing)
	case "tools/call":
		return served.call(ctx, incoming.ID, incoming.Params)
	default:
		return served.fail(incoming.ID, codeMethodNotFound, fmt.Sprintf("method not found: %s", incoming.Method))
	}
}

func (served *server) initialize(params json.RawMessage) initializeResult {
	var requested struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	// A client whose params do not decode still gets the latest version, which it may then decline.
	_ = json.Unmarshal(params, &requested)

	negotiated := supportedVersions[0]
	if slices.Contains(supportedVersions, requested.ProtocolVersion) {
		negotiated = requested.ProtocolVersion
	}

	return initializeResult{
		ProtocolVersion: negotiated,
		Capabilities:    capabilities{Tools: toolsCapability{ListChanged: false}},
		ServerInfo:      served.info,
		Instructions:    served.instructions,
	}
}

func (served *server) call(ctx context.Context, id json.RawMessage, params json.RawMessage) error {
	var called callParams
	// Params that do not decode as an object leave the name empty, which the check below refuses.
	_ = json.Unmarshal(params, &called)
	if called.Name == "" {
		return served.fail(id, codeInvalidParams, "invalid params: tools/call needs a tool name")
	}

	tool, known := served.tools[called.Name]
	if !known {
		return served.fail(id, codeInvalidParams, fmt.Sprintf("unknown tool: %s", called.Name))
	}

	arguments := called.Arguments
	if len(arguments) == 0 || string(arguments) == "null" {
		arguments = json.RawMessage(`{}`)
	}

	outcome := tool.Handle(ctx, arguments)

	return served.answer(id, callResult{
		Content: []textContent{{Type: "text", Text: outcome.Text}},
		IsError: outcome.IsError,
	})
}

func (served *server) answer(id json.RawMessage, result any) error {
	return served.write(response{JSONRPC: "2.0", ID: id, Result: result})
}

func (served *server) fail(id json.RawMessage, code int, message string) error {
	return served.write(response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}})
}

func (served *server) write(outgoing response) error {
	encodeErr := served.encoder.Encode(outgoing)
	if encodeErr != nil {
		return fmt.Errorf("writing a response: %w", encodeErr)
	}

	return nil
}
