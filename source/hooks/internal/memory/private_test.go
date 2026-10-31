package memory_test

import (
	"regexp"
	"strings"
	"testing"
)

// listsSeat is whether a who answer, text or JSON, names the seat anywhere.
func listsSeat(answer string, name string) bool {
	return regexp.MustCompile(`(?m)(^\s*` + name + `\s|"name":"` + name + `"|\\"name\\":\\"` + name + `\\")`).MatchString(answer)
}

func TestAPrivateSeatLeavesNoTraceInAnotherProjectsWho(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "secret", "--private")
	h.file("Gate finding of a private seat", "body")

	newHarnessSharing(h).must("", "claim", "open")

	elsewhere := otherProject(h)

	for name, answer := range map[string]string{
		"who --all":        elsewhere.must("", "who", "--all"),
		"who --all --json": elsewhere.must("", "who", "--all", "--json"),
		"MCP who all":      mcpMessage(t, elsewhere.mcp(`{"name":"who","arguments":{"all":true,"unused":true}}`)),
	} {
		if listsSeat(answer, "secret") || !listsSeat(answer, "open") {
			t.Errorf("%s should list the other project's seats but not its private one, got:\n%s", name, answer)
		}
	}

	own := h.must("", "who")
	if !regexp.MustCompile(`(?m)^secret\s+.+  filed 1 / used \d+  private$`).MatchString(own) {
		t.Errorf("its own project's who should list the private seat, its line ending private, got:\n%s", own)
	}

	if answer := h.must("", "who", "--json"); !strings.Contains(answer, `"private":true`) {
		t.Errorf("its own project's who --json should mark the private seat, got:\n%s", answer)
	}

	h.must("", "release")
	h.must("", "claim", "secret")

	if answer := elsewhere.must("", "who", "--all"); listsSeat(answer, "secret") {
		t.Errorf("the private mark should survive a release and a later claim, got:\n%s", answer)
	}

	if out := h.must("", "private", "off"); !strings.Contains(out, "secret is no longer private") {
		t.Errorf("private off should say the seat is listed again, got %q", out)
	}

	if answer := elsewhere.must("", "who", "--all"); !listsSeat(answer, "secret") {
		t.Errorf("private off should show the seat to other projects again, got:\n%s", answer)
	}

	if own := h.must("", "who"); strings.Contains(own, "private") {
		t.Errorf("a seat no longer private should carry no private mark, got:\n%s", own)
	}

	if out := h.must("", "private", "on"); !strings.Contains(out, "secret is private: no other project's who lists it") {
		t.Errorf("private on should say the seat is hidden, got %q", out)
	}

	if answer := elsewhere.must("", "who", "--all", "--json"); listsSeat(answer, "secret") {
		t.Errorf("private on should hide the seat from other projects again, got:\n%s", answer)
	}
}

func TestPrivateIsRefusedWithoutASeatOrAnOnOff(t *testing.T) {
	h := newHarness(t)

	if got := h.run("", "private", "on"); got.code != 1 || !strings.Contains(got.err, "this session holds no seat") {
		t.Errorf("private without a seat should be refused, got %d %q", got.code, got.err)
	}

	h.must("", "claim", "main")

	if got := h.run("", "private", "maybe"); got.code != 1 || !strings.Contains(got.err, "private takes on or off") {
		t.Errorf("private with neither on nor off should be refused, got %d %q", got.code, got.err)
	}
}

func TestPrivateIsNoMCPToolButAnMCPClaimMarksTheSeat(t *testing.T) {
	h := newHarness(t)

	if _, listed := h.listedTools()["private"]; listed {
		t.Error("private should not be listed as a tool")
	}

	answer := h.mcp(`{"name":"private","arguments":{"value":"on"}}`)
	if !strings.Contains(answer, `"isError":true`) ||
		!strings.Contains(answer, "agentic-memory: private is no MCP tool; run it through Bash: agentic-memory private on|off") {
		t.Errorf("an MCP call naming private should be refused with its command line, got %s", answer)
	}

	if answer := h.mcp(`{"name":"claim","arguments":{"name":"secret","private":true}}`); strings.Contains(answer, `"isError":true`) {
		t.Fatalf("an MCP claim should take the private key, got %s", answer)
	}

	elsewhere := otherProject(h)
	if answer := elsewhere.must("", "who", "--all"); listsSeat(answer, "secret") {
		t.Errorf("an MCP claim with private should hide the seat from other projects, got:\n%s", answer)
	}
}

func TestALauncherClaimForAnotherSessionMarksTheSeatPrivate(t *testing.T) {
	h := newHarness(t)
	h.must("", "claim", "main")

	if out := h.must("", "claim", "worker", "--session", "f00dbabe-0000-4000-8000-000000000000", "--private"); !strings.Contains(out,
		"worker is private") {
		t.Errorf("a claim for another session should say it marked the seat private, got:\n%s", out)
	}

	if answer := otherProject(h).must("", "who", "--all"); listsSeat(answer, "worker") || !listsSeat(answer, "main") {
		t.Errorf("a seat claimed --private for another session should be hidden elsewhere, got:\n%s", answer)
	}
}
