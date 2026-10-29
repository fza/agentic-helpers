package mcpserver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/mcpserver"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

type filed struct {
	ID      string           `json:"id"`
	Topics  []string         `json:"topics"`
	At      time.Time        `json:"at"`
	Counts  map[string]int   `json:"counts"`
	Parent  *filed           `json:"parent"`
	Note    string           `json:"note,omitempty"`
	Body    *json.RawMessage `json:"body,omitempty"`
	Hidden  string           `json:"-"`
	private string
	Labels  map[string]string `json:"labels,omitempty"`
	Data    []byte            `json:"data,omitempty"`
	Seen    time.Time         `json:"seen,omitzero"`
	Weight  float64           `json:"weight"`
	Plain   string
}

type answered struct {
	Message string `json:"message"`
	filed
}

func structuredTools() []mcpserver.Tool {
	return []mcpserver.Tool{
		{
			Name:         "file",
			OutputSchema: mcpserver.SchemaOf(answered{}),
			Handle: func(context.Context, json.RawMessage) mcpserver.Result {
				return mcpserver.Result{Text: "ignored", Structured: answered{Message: "filed m-1",
					filed: filed{ID: "m-1", Topics: []string{"hooks"}, At: time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)}}}
			},
		},
		{
			Name: "refuse",
			Handle: func(context.Context, json.RawMessage) mcpserver.Result {
				return mcpserver.Refusal("m-1 is on shelf idea")
			},
		},
		{
			Name: "unencodable",
			Handle: func(context.Context, json.RawMessage) mcpserver.Result {
				return mcpserver.Result{Structured: map[string]any{"bad": make(chan int)}}
			},
		},
	}
}

type structuredResult struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent"`
	IsError           bool            `json:"isError"`
}

func callStructured(t *testing.T, name string) structuredResult {
	t.Helper()

	var out bytes.Buffer

	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `"}}` + "\n"

	err := mcpserver.Serve(t.Context(), strings.NewReader(input), &out, serverName, serverVersion, structuredTools())
	if err != nil {
		t.Fatalf("serving should end cleanly: %v", err)
	}

	return decodeResult[structuredResult](t, decodeLines(t, out.String())[0])
}

func TestAStructuredAnswerIsItsTextSerialized(t *testing.T) {
	result := callStructured(t, "file")

	if len(result.Content) != 1 || result.Content[0].Text != string(result.StructuredContent) {
		t.Fatalf("the text block should be the structured content serialized, got %+v", result)
	}

	err := test.Conforms(mcpserver.SchemaOf(answered{}), result.StructuredContent)
	if err != nil || result.IsError {
		t.Errorf("a structured answer should fit the schema derived from its type: %v", err)
	}
}

func TestARefusalCarriesItsTextAsTheErrorKey(t *testing.T) {
	result := callStructured(t, "refuse")

	if !result.IsError || string(result.StructuredContent) != `{"error":"m-1 is on shelf idea"}` {
		t.Errorf("a refusal should be an error result whose object names the refusal, got %+v", result)
	}
}

func TestAnAnswerThatCannotEncodeBecomesARefusal(t *testing.T) {
	result := callStructured(t, "unencodable")

	if !result.IsError || !strings.Contains(string(result.StructuredContent), "encoding the unencodable answer") {
		t.Errorf("an answer that does not encode should come back as a refusal saying so, got %+v", result)
	}
}

func TestTheListingCarriesEachOutputSchema(t *testing.T) {
	var out bytes.Buffer

	input := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n"

	err := mcpserver.Serve(t.Context(), strings.NewReader(input), &out, serverName, serverVersion, structuredTools())
	if err != nil {
		t.Fatalf("serving should end cleanly: %v", err)
	}

	listing := decodeResult[struct {
		Tools []struct {
			Name         string          `json:"name"`
			OutputSchema json.RawMessage `json:"outputSchema"`
		} `json:"tools"`
	}](t, decodeLines(t, out.String())[0])

	if string(listing.Tools[0].OutputSchema) != string(mcpserver.SchemaOf(answered{})) || listing.Tools[1].OutputSchema != nil {
		t.Errorf("a tool should list its output schema, and one without none, got %+v", listing.Tools)
	}
}

func TestAnUnlistedToolAnswersButStaysOffTheListing(t *testing.T) {
	var out bytes.Buffer

	tools := append(structuredTools(), mcpserver.Tool{
		Name:     "gone",
		Unlisted: true,
		Handle: func(context.Context, json.RawMessage) mcpserver.Result {
			return mcpserver.Refusal("gone is no MCP tool")
		},
	})

	input := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"gone"}}` + "\n"

	err := mcpserver.Serve(t.Context(), strings.NewReader(input), &out, serverName, serverVersion, tools)
	if err != nil {
		t.Fatalf("serving should end cleanly: %v", err)
	}

	responses := decodeLines(t, out.String())

	listing := decodeResult[struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}](t, responses[0])

	for _, listed := range listing.Tools {
		if listed.Name == "gone" {
			t.Errorf("an unlisted tool should stay off the listing")
		}
	}

	if len(listing.Tools) != len(structuredTools()) {
		t.Errorf("every listed tool should stay on the listing, got %+v", listing.Tools)
	}

	called := decodeResult[structuredResult](t, responses[1])
	if !called.IsError || !strings.Contains(string(called.StructuredContent), "gone is no MCP tool") {
		t.Errorf("a call naming an unlisted tool should get the tool's own answer, got %+v", called)
	}
}

func TestASchemaFollowsWhatTheEncoderWrites(t *testing.T) {
	var schema struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}

	err := json.Unmarshal(mcpserver.SchemaOf(answered{}), &schema)
	if err != nil {
		t.Fatalf("the schema should decode: %v", err)
	}

	if schema.Type != "object" || strings.Join(schema.Required, ",") != "message,id,topics,at,counts,parent,weight,Plain" {
		t.Errorf("every field the encoder always writes should be required, an embedded struct's promoted, got %+v", schema)
	}

	for _, gone := range []string{"Hidden", "private", "filed"} {
		if _, listed := schema.Properties[gone]; listed {
			t.Errorf("a field the encoder never writes should not be listed: %s", gone)
		}
	}

	raw := json.RawMessage(`{"any":["shape"]}`)

	for _, value := range []answered{
		{},
		{Message: "m", filed: filed{Note: "n", Labels: map[string]string{"a": "b"}, Parent: &filed{ID: "p"},
			Counts: map[string]int{"x": 1}, Body: &raw, Data: []byte("bytes"), Seen: time.Unix(1, 0).UTC(), Weight: 2}},
	} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("encoding: %v", err)
		}

		err = test.Conforms(mcpserver.SchemaOf(answered{}), data)
		if err != nil {
			t.Errorf("every value of a type should fit the schema derived from it: %v", err)
		}
	}

	for _, misfit := range []string{
		`{"id":"m","topics":null,"at":"x","counts":null,"parent":null,"weight":1,"Plain":"p"}`,
		`{"message":1,"id":"m","topics":null,"at":"x","counts":null,"parent":null,"weight":1,"Plain":"p"}`,
		`{"message":"m","id":"m","topics":[1],"at":"x","counts":null,"parent":null,"weight":1,"Plain":"p"}`,
		`{"message":"m","id":"m","topics":null,"at":"x","counts":{"x":"y"},"parent":null,"weight":1,"Plain":"p"}`,
		`{"message":"m","id":"m","topics":null,"at":"x","counts":null,"parent":{"id":2},"weight":1,"Plain":"p"}`,
	} {
		if test.Conforms(mcpserver.SchemaOf(answered{}), []byte(misfit)) == nil {
			t.Errorf("an answer the type cannot produce should not fit: %s", misfit)
		}
	}
}

type shadowingFirst struct {
	Note int    `json:"note"`
	ID   string `json:"id,omitempty"`
	filed
}

type shadowingLast struct {
	filed
	Note int    `json:"note"`
	ID   string `json:"id,omitempty"`
}

func TestAnOuterFieldShadowsAnEmbeddedOne(t *testing.T) {
	for _, value := range []any{shadowingFirst{}, shadowingLast{}} {
		var schema struct {
			Required []string `json:"required"`
		}

		err := json.Unmarshal(mcpserver.SchemaOf(value), &schema)
		if err != nil {
			t.Fatalf("the schema should decode: %v", err)
		}

		if !slices.Contains(schema.Required, "note") || slices.Contains(schema.Required, "id") {
			t.Errorf("the outer field should decide whether a shadowed key is required, wherever declared: %T %v",
				value, schema.Required)
		}

		data, _ := json.Marshal(value)

		err = test.Conforms(mcpserver.SchemaOf(value), data)
		if err != nil {
			t.Errorf("a value should fit the schema of its type: %T %v", value, err)
		}
	}
}

func TestARefusalCarryingMoreThanItsErrorDoesNotFit(t *testing.T) {
	line := `{"jsonrpc":"2.0","id":2,"result":{"content":[{"type":"text","text":"{\"error\":\"x\",\"extra\":1}"}],` +
		`"structuredContent":{"error":"x","extra":1},"isError":true}}`

	if test.AnswerFits(nil, "any", line) == nil {
		t.Error("a refusal should carry its error key alone")
	}
}

func TestASchemaWithNothingRequiredListsNoRequiredKey(t *testing.T) {
	schema := mcpserver.SchemaOf(struct {
		Note string `json:"note,omitempty"`
	}{})

	if strings.Contains(string(schema), `"required"`) {
		t.Errorf("an object with no required key should list none, since older JSON Schema drafts refuse an empty list: %s",
			schema)
	}
}

type produced struct {
	ID  string   `json:"id"`
	IDs []string `json:"ids,omitempty"`
}

func TestAMessagedAnswerCarriesItsTextBesideTheFields(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields any
		want   string
	}{
		{name: "fields", fields: produced{ID: "m-1"}, want: `{"id":"m-1","message":"filed m-1"}`},
		{name: "no fields", want: `{"message":"filed m-1"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(mcpserver.Messaged("filed m-1", tc.fields))
			if err != nil || string(data) != tc.want {
				t.Fatalf("the message should sit beside the fields at the top level, got %s, %v", data, err)
			}

			err = test.Conforms(mcpserver.MessageSchema(tc.fields), data)
			if err != nil {
				t.Errorf("a messaged answer should fit its schema: %v", err)
			}
		})
	}

	if test.Conforms(mcpserver.MessageSchema(produced{}), []byte(`{"id":"m-1"}`)) == nil {
		t.Error("the schema should require the message")
	}

	for _, tc := range []struct {
		fields any
		says   string
	}{
		{fields: 1, says: "the answer's fields are not an object"},
		{fields: map[string]any{"bad": make(chan int)}, says: "encoding the answer's fields"},
	} {
		_, err := json.Marshal(mcpserver.Messaged("x", tc.fields))
		if err == nil || !strings.Contains(err.Error(), tc.says) {
			t.Errorf("fields that do not encode as an object should fail to encode saying why: %v", err)
		}
	}
}

type shortened struct {
	Name  string `json:"name"`
	Short bool   `json:"short"`
}

type lengthy struct {
	Name  string `json:"name"`
	Notes string `json:"notes"`
	Short bool   `json:"-"`
}

func (held lengthy) MarshalJSON() ([]byte, error) {
	if held.Short {
		return json.Marshal(shortened{Name: held.Name, Short: true})
	}

	type plain lengthy

	return json.Marshal(plain(held))
}

func (lengthy) SchemaAlternative() any {
	return shortened{}
}

func TestASchemaAdmitsTheOtherShapeATypeMayWrite(t *testing.T) {
	schema := mcpserver.SchemaOf([]lengthy{})

	for _, held := range []lengthy{{Name: "a", Notes: "b"}, {Name: "a", Short: true}} {
		data, _ := json.Marshal([]lengthy{held})

		err := test.Conforms(schema, data)
		if err != nil {
			t.Errorf("the schema should admit both shapes the type writes: %v (%s)", err, data)
		}
	}

	err := test.Conforms(schema, []byte(`[{"name":"a"}]`))
	if err == nil {
		t.Error("the schema should still require each shape's own keys")
	}
}
