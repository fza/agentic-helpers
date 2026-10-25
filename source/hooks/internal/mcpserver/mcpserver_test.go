package mcpserver_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/mcpserver"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

const (
	serverName    = "agentic-test"
	serverVersion = "1.2.3"
)

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type callResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError *bool `json:"isError"`
}

func tools() []mcpserver.Tool {
	return []mcpserver.Tool{
		{
			Name:        "echo",
			Description: "Answers with its arguments.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`),
			Handle: func(_ context.Context, arguments json.RawMessage) mcpserver.Result {
				return mcpserver.Result{Text: string(arguments)}
			},
		},
		{
			Name:        "refuse",
			Description: "Always refuses.",
			Handle: func(context.Context, json.RawMessage) mcpserver.Result {
				return mcpserver.Result{Text: "refused: nothing to do", IsError: true}
			},
		},
	}
}

func serve(t *testing.T, input string) ([]rpcResponse, error) {
	t.Helper()

	var out bytes.Buffer
	err := mcpserver.Serve(t.Context(), strings.NewReader(input), &out, serverName, serverVersion, tools())

	return decodeLines(t, out.String()), err
}

func decodeLines(t *testing.T, output string) []rpcResponse {
	t.Helper()

	if output == "" {
		return nil
	}
	if !strings.HasSuffix(output, "\n") {
		t.Fatalf("output should end every message with a newline, got %q", output)
	}

	var responses []rpcResponse
	for raw := range strings.SplitSeq(strings.TrimSuffix(output, "\n"), "\n") {
		var decoded rpcResponse
		err := json.Unmarshal([]byte(raw), &decoded)
		if err != nil {
			t.Fatalf("every output line should be a JSON-RPC message, got %q: %v", raw, err)
		}
		if decoded.JSONRPC != "2.0" {
			t.Fatalf("every response should declare JSON-RPC 2.0, got %q", raw)
		}
		responses = append(responses, decoded)
	}

	return responses
}

func single(t *testing.T, input string) rpcResponse {
	t.Helper()

	responses, err := serve(t, input)
	if err != nil {
		t.Fatalf("serving should end cleanly at the end of input: %v", err)
	}
	if len(responses) != 1 {
		t.Fatalf("one request should yield exactly one response, got %d", len(responses))
	}

	return responses[0]
}

func decodeResult[Shape any](t *testing.T, response rpcResponse) Shape {
	t.Helper()

	if response.Error != nil {
		t.Fatalf("the request should succeed, got error %d %q", response.Error.Code, response.Error.Message)
	}

	var shaped Shape
	err := json.Unmarshal(response.Result, &shaped)
	if err != nil {
		t.Fatalf("the result should decode: %v", err)
	}

	return shaped
}

func TestServeInitializeNegotiatesTheProtocolVersion(t *testing.T) {
	cases := []struct {
		name      string
		params    string
		negotiate string
	}{
		{name: "latest supported version is echoed", params: `{"protocolVersion":"2025-11-25"}`, negotiate: "2025-11-25"},
		{name: "older supported version is echoed", params: `{"protocolVersion":"2025-06-18"}`, negotiate: "2025-06-18"},
		{name: "oldest supported version is echoed", params: `{"protocolVersion":"2024-11-05"}`, negotiate: "2024-11-05"},
		{name: "unsupported version gets the latest", params: `{"protocolVersion":"2099-01-01"}`, negotiate: "2025-11-25"},
		{name: "missing version gets the latest", params: `{}`, negotiate: "2025-11-25"},
		{name: "missing params get the latest", params: ``, negotiate: "2025-11-25"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			request := `{"jsonrpc":"2.0","id":1,"method":"initialize"}`
			if tc.params != "" {
				request = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":` + tc.params + `}`
			}

			response := single(t, request+"\n")
			if string(response.ID) != "1" {
				t.Errorf("the response should carry the request's id, got %s", response.ID)
			}

			result := decodeResult[struct {
				ProtocolVersion string `json:"protocolVersion"`
				Capabilities    struct {
					Tools *struct {
						ListChanged *bool `json:"listChanged"`
					} `json:"tools"`
				} `json:"capabilities"`
				ServerInfo struct {
					Name    string `json:"name"`
					Version string `json:"version"`
				} `json:"serverInfo"`
			}](t, response)

			if result.ProtocolVersion != tc.negotiate {
				t.Errorf("the negotiated version should follow the spec's rule, got %q", result.ProtocolVersion)
			}
			if result.Capabilities.Tools == nil || result.Capabilities.Tools.ListChanged == nil {
				t.Fatal("the tools capability should be declared with listChanged")
			}
			if *result.Capabilities.Tools.ListChanged {
				t.Error("the tool list should be declared as never changing")
			}
			if result.ServerInfo.Name != serverName || result.ServerInfo.Version != serverVersion {
				t.Errorf("serverInfo should name the server and its version, got %+v", result.ServerInfo)
			}
		})
	}
}

func TestServeNotificationsYieldNoOutput(t *testing.T) {
	cases := []struct {
		name    string
		message string
	}{
		{name: "initialized", message: `{"jsonrpc":"2.0","method":"notifications/initialized"}`},
		{name: "cancelled", message: `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":3}}`},
		{name: "unknown notification", message: `{"jsonrpc":"2.0","method":"notifications/whatever"}`},
		{name: "tools call sent as a notification", message: `{"jsonrpc":"2.0","method":"tools/call","params":{"name":"echo"}}`},
		{name: "response from the client", message: `{"jsonrpc":"2.0","id":7,"result":{}}`},
		{name: "error response from the client", message: `{"jsonrpc":"2.0","id":7,"error":{"code":-1,"message":"no"}}`},
		{name: "blank line", message: ``},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			responses, err := serve(t, tc.message+"\n")
			if err != nil {
				t.Fatalf("serving should end cleanly at the end of input: %v", err)
			}
			if len(responses) != 0 {
				t.Errorf("a message needing no answer should write nothing, got %d responses", len(responses))
			}
		})
	}
}

func TestServePingAnswersAnEmptyResult(t *testing.T) {
	response := single(t, `{"jsonrpc":"2.0","id":"p-1","method":"ping"}`+"\n")

	if string(response.ID) != `"p-1"` {
		t.Errorf("a string id should be echoed unchanged, got %s", response.ID)
	}
	if response.Error != nil || string(response.Result) != `{}` {
		t.Errorf("ping should answer an empty object, got result %s", response.Result)
	}
}

func TestServeToolsListDescribesEveryTool(t *testing.T) {
	response := single(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`+"\n")

	listing := decodeResult[struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}](t, response)

	registered := tools()
	if len(listing.Tools) != len(registered) {
		t.Fatalf("every registered tool should be listed, got %d", len(listing.Tools))
	}

	for index, listed := range listing.Tools {
		if listed.Name != registered[index].Name || listed.Description != registered[index].Description {
			t.Errorf("tools should be listed in registration order with their description, got %+v", listed)
		}

		var schema map[string]any
		err := json.Unmarshal(listed.InputSchema, &schema)
		if err != nil {
			t.Fatalf("an input schema should be a JSON object: %v", err)
		}
		if schema["type"] != "object" {
			t.Errorf("an input schema should describe an object, got %s", listed.InputSchema)
		}
	}
}

func TestServeToolsListOfNoToolsIsAnEmptyArray(t *testing.T) {
	var out bytes.Buffer
	input := strings.NewReader(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n")

	err := mcpserver.Serve(t.Context(), input, &out, serverName, serverVersion, nil)
	if err != nil {
		t.Fatalf("serving should end cleanly at the end of input: %v", err)
	}

	responses := decodeLines(t, out.String())
	if len(responses) != 1 || string(responses[0].Result) != `{"tools":[]}` {
		t.Errorf("an empty tool set should list as an empty array, got %s", out.String())
	}
}

func TestServeToolsCallWrapsTheHandlersResult(t *testing.T) {
	cases := []struct {
		name    string
		params  string
		text    string
		isError bool
	}{
		{name: "success", params: `{"name":"echo","arguments":{"text":"hi"}}`, text: `{"text":"hi"}`},
		{name: "missing arguments reach the handler as an empty object", params: `{"name":"echo"}`, text: `{}`},
		{name: "null arguments reach the handler as an empty object", params: `{"name":"echo","arguments":null}`, text: `{}`},
		{name: "refusal is a result flagged as an error", params: `{"name":"refuse","arguments":{}}`, text: "refused: nothing to do", isError: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := single(t, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":`+tc.params+`}`+"\n")
			result := decodeResult[callResult](t, response)

			if len(result.Content) != 1 || result.Content[0].Type != "text" {
				t.Fatalf("the result should hold one text block, got %+v", result.Content)
			}
			if result.Content[0].Text != tc.text {
				t.Errorf("the text block should carry the handler's text, got %q", result.Content[0].Text)
			}
			if result.IsError == nil {
				t.Fatal("the result should always state isError")
			}
			if *result.IsError != tc.isError {
				t.Errorf("isError should follow the handler's verdict, got %v", *result.IsError)
			}
		})
	}
}

func TestServeAnswersProtocolErrors(t *testing.T) {
	cases := []struct {
		name     string
		message  string
		id       string
		code     int
		mentions string
	}{
		{name: "unknown tool", message: `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"nope"}}`, id: "4", code: -32602, mentions: "nope"},
		{name: "tools call without a name", message: `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{}}`, id: "4", code: -32602, mentions: "tool name"},
		{name: "tools call with malformed params", message: `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":[1]}`, id: "4", code: -32602, mentions: "tool name"},
		{name: "unknown method", message: `{"jsonrpc":"2.0","id":5,"method":"resources/list"}`, id: "5", code: -32601, mentions: "resources/list"},
		{name: "stateless era discovery probe", message: `{"jsonrpc":"2.0","id":5,"method":"server/discover"}`, id: "5", code: -32601},
		{name: "malformed JSON", message: `{"jsonrpc":"2.0","id":6,"method":`, id: "null", code: -32700},
		{name: "JSON that is not an object", message: `[{"jsonrpc":"2.0","id":6,"method":"ping"}]`, id: "null", code: -32600},
		{name: "missing jsonrpc version", message: `{"id":6,"method":"ping"}`, id: "6", code: -32600},
		{name: "missing method", message: `{"jsonrpc":"2.0","id":6}`, id: "6", code: -32600},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := single(t, tc.message+"\n")

			if response.Error == nil {
				t.Fatalf("the request should be answered with an error, got result %s", response.Result)
			}
			if response.Result != nil {
				t.Error("an error response should carry no result")
			}
			if string(response.ID) != tc.id {
				t.Errorf("the error should carry the request's id, or null when unreadable, got %s", response.ID)
			}
			if response.Error.Code != tc.code {
				t.Errorf("the error code should classify the failure, got %d", response.Error.Code)
			}
			if !strings.Contains(response.Error.Message, tc.mentions) {
				t.Errorf("the error message should say what is wrong, got %q", response.Error.Message)
			}
		})
	}
}

func TestServeAnswersRequestsInOrderAndKeepsGoingAfterErrors(t *testing.T) {
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`not json`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		``,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"ping"}`,
	}, "\r\n") + "\r\n"

	responses, err := serve(t, input)
	if err != nil {
		t.Fatalf("serving should end cleanly at the end of input: %v", err)
	}

	ids := make([]string, 0, len(responses))
	for _, response := range responses {
		ids = append(ids, string(response.ID))
	}
	if strings.Join(ids, ",") != "1,null,2,3,4" {
		t.Errorf("every request should be answered once, in arrival order, got ids %v", ids)
	}
}

func TestServeHandlesLongLines(t *testing.T) {
	payload := strings.Repeat("x", 1<<20)
	request := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"echo","arguments":{"text":"` + payload + `"}}}`

	response := single(t, request+"\n")
	result := decodeResult[callResult](t, response)

	if len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, payload) {
		t.Error("a megabyte argument should round-trip through the handler unchanged")
	}
}

func TestServeRefusesALineBeyondTheMessageLimit(t *testing.T) {
	oversized := strings.Repeat("x", 16<<20+1) + "\n"

	_, err := serve(t, oversized)
	if !errors.Is(err, bufio.ErrTooLong) {
		t.Errorf("a line beyond the limit should end serving with a too-long error, got %v", err)
	}
}

func TestServeReturnsAReadError(t *testing.T) {
	readFailure := errors.New("input broke")
	var out bytes.Buffer

	err := mcpserver.Serve(t.Context(), iotest.ErrReader(readFailure), &out, serverName, serverVersion, tools())
	if !errors.Is(err, readFailure) {
		t.Errorf("a failing input should end serving with its error, got %v", err)
	}
}

func TestServeReturnsAWriteError(t *testing.T) {
	writeFailure := errors.New("output broke")
	input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n")

	err := mcpserver.Serve(t.Context(), input, test.FailingWriter{Err: writeFailure}, serverName, serverVersion, tools())
	if !errors.Is(err, writeFailure) {
		t.Errorf("a failing output should end serving with its error, got %v", err)
	}
}

func TestServeEndsCleanlyAtEndOfInput(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{name: "empty input", input: ``},
		{name: "last line without a newline", input: `{"jsonrpc":"2.0","id":1,"method":"ping"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := serve(t, tc.input)
			if err != nil {
				t.Errorf("the end of input should end serving without an error, got %v", err)
			}
		})
	}
}

func TestServeReturnsWhenTheContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	input, inputWriter := io.Pipe()
	t.Cleanup(func() { _ = inputWriter.Close() })
	output, outputWriter := io.Pipe()
	t.Cleanup(func() { _ = output.Close() })

	served := make(chan error, 1)
	go func() {
		served <- mcpserver.Serve(ctx, input, outputWriter, serverName, serverVersion, tools())
	}()

	// One answered ping proves the input is drained, so only the cancellation can wake the loop.
	_, writeErr := io.WriteString(inputWriter, `{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n")
	if writeErr != nil {
		t.Fatalf("the server should read a request: %v", writeErr)
	}
	_, readErr := bufio.NewReader(output).ReadString('\n')
	if readErr != nil {
		t.Fatalf("the server should answer a request: %v", readErr)
	}

	cancel()

	select {
	case err := <-served:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("cancelling should end serving with the context's error, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelling should end serving while the input is idle")
	}
}

func TestServeStopsAfterAHandlerSeesTheContextCancelled(t *testing.T) {
	// With a cancelled context and a queued line both ready, the loop's choice between them is
	// random, so one run can pass by luck; repetition makes a missing context check show.
	for range 50 {
		ctx, cancel := context.WithCancel(t.Context())
		cancelling := mcpserver.Tool{
			Name: "cancel",
			Handle: func(handlerCtx context.Context, _ json.RawMessage) mcpserver.Result {
				cancel()
				<-handlerCtx.Done()

				return mcpserver.Result{Text: "stopped", IsError: true}
			},
		}
		input := strings.NewReader(strings.Join([]string{
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"cancel"}}`,
			`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		}, "\n") + "\n")
		var out bytes.Buffer

		err := mcpserver.Serve(ctx, input, &out, serverName, serverVersion, []mcpserver.Tool{cancelling})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("a cancelled context should end serving with its error, got %v", err)
		}

		responses := decodeLines(t, out.String())
		if len(responses) != 1 || string(responses[0].ID) != "1" {
			t.Fatalf("the call in flight should be answered and nothing after it, got %s", out.String())
		}
	}
}

func TestServeRefusesAnInvalidToolSet(t *testing.T) {
	handle := func(context.Context, json.RawMessage) mcpserver.Result { return mcpserver.Result{} }
	cases := []struct {
		name  string
		tools []mcpserver.Tool
		want  error
	}{
		{
			name:  "duplicate name",
			tools: []mcpserver.Tool{{Name: "same", Handle: handle}, {Name: "same", Handle: handle}},
			want:  mcpserver.ErrDuplicateTool,
		},
		{
			name:  "schema that is not JSON",
			tools: []mcpserver.Tool{{Name: "broken", InputSchema: json.RawMessage(`{"type":`), Handle: handle}},
			want:  mcpserver.ErrInvalidInputSchema,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			input := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n")

			err := mcpserver.Serve(t.Context(), input, &out, serverName, serverVersion, tc.tools)
			if !errors.Is(err, tc.want) {
				t.Errorf("an invalid tool set should be refused before serving, got %v", err)
			}
			if out.Len() != 0 {
				t.Error("a refused tool set should write nothing")
			}
		})
	}
}

func TestServeNoneListsNoToolsAndCarriesItsInstructions(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"

	var out bytes.Buffer

	err := mcpserver.ServeNone(t.Context(), strings.NewReader(input), &out, serverName, serverVersion, "the piece is off")
	if err != nil {
		t.Fatalf("serving: %v", err)
	}

	if !strings.Contains(out.String(), `"instructions":"the piece is off"`) || !strings.Contains(out.String(), `"tools":[]`) {
		t.Errorf("a server of an off piece should hand over its instructions and list no tools, got %s", out.String())
	}

	served, _ := serve(t, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`+"\n")
	if len(served) != 1 || strings.Contains(string(served[0].Result), "instructions") {
		t.Errorf("a server of an on piece should carry no instructions, got %+v", served)
	}
}
