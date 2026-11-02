package mcpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/mcpserver"
)

const listSchema = `{"type":"object","properties":{` +
	`"term":{"type":"array","items":{"type":"string"},"minItems":1,"description":"patterns, all must match"},` +
	`"query":{"type":"string","description":"a phrase"},` +
	`"ids":{"description":"entry ids","type":"array","items":{"type":"string","pattern":"^m-"}}},` +
	`"required":["term"],"additionalProperties":false}`

func listTools() []mcpserver.Tool {
	return []mcpserver.Tool{{
		Name:        "search",
		Description: "Answers with its arguments.",
		InputSchema: json.RawMessage(listSchema),
		Handle: func(_ context.Context, arguments json.RawMessage) mcpserver.Result {
			return mcpserver.Result{Text: string(arguments)}
		},
	}}
}

func serveLists(t *testing.T, input string) rpcResponse {
	t.Helper()

	var out bytes.Buffer

	err := mcpserver.Serve(t.Context(), strings.NewReader(input), &out, serverName, serverVersion, listTools())
	if err != nil {
		t.Fatalf("serving should end cleanly at the end of input: %v", err)
	}

	responses := decodeLines(t, out.String())
	if len(responses) != 1 {
		t.Fatalf("one request should yield exactly one response, got %d", len(responses))
	}

	return responses[0]
}

func TestAListKeyIsListedAsOneValueOrAnArray(t *testing.T) {
	response := serveLists(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`+"\n")

	listing := decodeResult[struct {
		Tools []struct {
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}](t, response)

	want := `{"type":"object","properties":{` +
		`"term":{"anyOf":[{"type":"string"},{"type":"array","items":{"type":"string"},"minItems":1}],"description":"patterns, all must match"},` +
		`"query":{"type":"string","description":"a phrase"},` +
		`"ids":{"anyOf":[{"type":"string","pattern":"^m-"},{"type":"array","items":{"type":"string","pattern":"^m-"}}],"description":"entry ids"}},` +
		`"required":["term"],"additionalProperties":false}`

	if len(listing.Tools) != 1 || string(listing.Tools[0].InputSchema) != want {
		t.Errorf("every array key should be listed as its item or an array, everything else kept in order, got %s",
			listing.Tools[0].InputSchema)
	}
}

func TestOneValueForAListKeyReachesTheToolAsAOneItemArray(t *testing.T) {
	cases := []struct {
		name      string
		arguments string
		handed    string
	}{
		{name: "one string", arguments: `{"term":"a|b","query":"x"}`, handed: `{"term":["a|b"],"query":"x"}`},
		{name: "one string on each list key", arguments: `{"ids":"m-ab12","term":"x"}`, handed: `{"ids":["m-ab12"],"term":["x"]}`},
		{name: "an array as sent", arguments: `{"term":["a","b"]}`, handed: `{"term":["a","b"]}`},
		{name: "a string key left alone", arguments: `{"query":"x"}`, handed: `{"query":"x"}`},
		{name: "null left alone", arguments: `{"term":null}`, handed: `{"term":null}`},
		{name: "a number made a one-item array for the tool to refuse", arguments: `{"term":3}`, handed: `{"term":[3]}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := serveLists(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search","arguments":`+
				tc.arguments+`}}`+"\n")
			result := decodeResult[callResult](t, response)

			if len(result.Content) != 1 || result.Content[0].Text != tc.handed {
				t.Errorf("the tool should receive every list key as an array, got %+v", result.Content)
			}
		})
	}
}

func TestAToolWithAnUnreadableSchemaShapeIsRefused(t *testing.T) {
	tools := []mcpserver.Tool{{
		Name:        "odd",
		InputSchema: json.RawMessage(`{"type":"object","properties":["term"]}`),
		Handle:      func(context.Context, json.RawMessage) mcpserver.Result { return mcpserver.Result{} },
	}}

	err := mcpserver.Serve(t.Context(), strings.NewReader(""), &bytes.Buffer{}, serverName, serverVersion, tools)
	if err == nil {
		t.Error("a schema whose properties are no object should refuse the tool set")
	}
}
