package memory_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/memorylog"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

var credentialRefusal = regexp.MustCompile(`agentic-memory: (the title|the body|the state|the reason) looks like a credential ` +
	`\((?:[a-z0-9-]+, )*github-pat(?:, [a-z0-9-]+)*, line \d+\); memory never holds one`)

func TestWritesRefuseACredential(t *testing.T) {
	credential := test.SecretFixture(t, "positive/github-pat.txt")
	token := credential[strings.Index(credential, "ghp_"):strings.LastIndex(credential, `"`)]

	h := newHarness(t)
	h.must("", "claim", "main")
	id := h.file("Gate finding kept clean", "clean")
	owed := h.file("Read the board on claim", "body", "--kind", "rule", "--from", "user", "--on", "claim")
	h.must("", "claim", "main")

	cases := []struct {
		name  string
		stdin string
		args  []string
		where string
	}{
		{name: "a title", stdin: "clean", args: []string{"new", "--title", "Token " + token + " here", "--topic", "gate"}, where: "the title"},
		{name: "a body", stdin: credential, args: []string{"new", "--title", "Gate token found today", "--topic", "gate"}, where: "the body"},
		{name: "a state", stdin: credential, args: []string{"state"}, where: "the state"},
		{name: "a retitle", args: []string{"retitle", id, "Token " + token}, where: "the title"},
		{name: "a retire reason", args: []string{"retire", id, "--because", token}, where: "the reason"},
		{name: "a supersede reason", args: []string{"supersede", id, "--because", token}, where: "the reason"},
		{name: "a decline reason", args: []string{"ack", owed, "--decline", token}, where: "the reason"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := h.logBytes()

			got := h.run(tc.stdin, tc.args...)
			if got.code != 1 || !credentialRefusal.MatchString(got.err) || !strings.Contains(got.err, tc.where) {
				t.Errorf("a credential in %s should be refused naming the rule and line, got %d %q", tc.where, got.code, got.err)
			}

			if !h.logUnchanged(before) {
				t.Error("a refused write should leave the log byte-identical")
			}
		})
	}

	for _, call := range []string{
		`{"name":"new","arguments":{"title":"Gate token found today","body":"` + token + `"}}`,
		`{"name":"state","arguments":{"body":"` + token + `"}}`,
	} {
		if answer := h.mcp(call); !strings.Contains(answer, `"isError":true`) || !credentialRefusal.MatchString(answer) {
			t.Errorf("a credential over MCP should be refused the same way: %s", answer)
		}
	}
}

func TestTheMigrationScanTellsTheOwningSeatOnce(t *testing.T) {
	credential := test.SecretFixture(t, "positive/github-pat.txt")

	h := newHarness(t)
	format3Store(t, h)

	main := seatID(t, h.store(), "main")
	body, _ := json.Marshal(map[string]any{
		"title": "Deploy notes with a token", "body": credential, "topics": []string{"deploy"}, "retention": "normal", "from": "agent",
	})

	log, err := memorylog.Open(filepath.Join(h.store(), "log"))
	if err != nil {
		t.Fatal(err)
	}

	token := credential[strings.Index(credential, "ghp_"):strings.LastIndex(credential, `"`)]
	dropped, _ := json.Marshal(map[string]any{
		"title": "Old token " + token, "body": "", "dropped": true, "topics": []string{"deploy"}, "retention": "normal",
	})

	err = log.Append(context.Background(), memorylog.Event{
		Kind: memorylog.KindNew, Entry: "m-leak01", Time: h.clock.Now(), Seat: main, Payload: body,
	}, memorylog.Event{
		Kind: memorylog.KindNew, Entry: "m-gone01", Time: h.clock.Now(), Seat: main, Payload: dropped,
	})
	if err != nil {
		t.Fatal(err)
	}

	first := h.must("", "brief")
	if !strings.Contains(first, "possible credential: m-leak01 Deploy notes with a token (generic-api-key, github-pat); supersede it --because 'credential'") {
		t.Errorf("the owning seat's next brief should list what the migration scan found:\n%s", first)
	}

	if strings.Contains(first, "possible credential: m-rule01") || strings.Contains(first, "possible credential: m-gone01") {
		t.Errorf("a clean entry, or one no longer holding its body, should not be listed:\n%s", first)
	}

	if second := h.must("", "brief"); strings.Contains(second, "possible credential") {
		t.Errorf("a finding should be listed once:\n%s", second)
	}
}
