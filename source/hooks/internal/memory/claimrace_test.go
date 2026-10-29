package memory_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/memory"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// liveServer is the MCP server a harness's session runs, kept open between
// calls, so another session can act while it runs.
type liveServer struct {
	t       *testing.T
	in      *io.PipeWriter
	answers *bufio.Scanner
	next    int
	done    chan struct{}
}

func (h *harness) serve() *liveServer {
	h.t.Helper()

	requests, in := io.Pipe()
	out, answers := io.Pipe()

	server := &liveServer{t: h.t, in: in, answers: bufio.NewScanner(out), done: make(chan struct{})}
	server.answers.Buffer(make([]byte, 1<<20), 1<<20)

	go func() {
		defer close(server.done)
		defer func() { _ = answers.Close() }()

		var errs strings.Builder
		memory.Main(context.Background(), []string{"mcp"}, h.env(), hookio.Streams{In: requests, Out: answers, Err: &errs})
	}()

	h.t.Cleanup(func() {
		_ = in.Close()
		<-server.done
	})

	server.send(`"initialize","params":{"protocolVersion":"2025-06-18"}`)

	return server
}

func (server *liveServer) send(method string) string {
	server.t.Helper()

	server.next++

	_, err := fmt.Fprintf(server.in, `{"jsonrpc":"2.0","id":%d,"method":%s}`+"\n", server.next, method)
	if err != nil {
		server.t.Fatalf("sending to the server: %v", err)
	}

	if !server.answers.Scan() {
		server.t.Fatalf("the server should answer every request: %v", server.answers.Err())
	}

	return server.answers.Text()
}

func (server *liveServer) call(tool string, arguments string) string {
	server.t.Helper()

	return server.send(fmt.Sprintf(`"tools/call","params":{"name":%q,"arguments":%s}`, tool, arguments))
}

func TestAServerOlderThanASeatNeverTakesItFromItsHolder(t *testing.T) {
	h := newHarness(t)
	other := newHarnessSharing(h)
	other.must("", "claim", "idea")
	other.must("", "release")

	server := h.serve()
	server.call("who", `{}`)

	other.must("", "claim", "fresh")

	answer := server.call("claim", `{"name":"fresh"}`)
	if !strings.Contains(answer, `"isError":true`) || !strings.Contains(answer, "fresh is held by session") {
		t.Errorf("a server running since before a seat was made should be refused it while its holder runs: %s", answer)
	}

	view, code := h.holderJSON("fresh", "--json")
	if code != 0 || view["session_id"] != other.session {
		t.Errorf("the seat should stay with the session holding it: %v", view)
	}
}

func TestAServerSeesASeatRenamedAfterItStarted(t *testing.T) {
	h := newHarness(t)
	other := newHarnessSharing(h)
	other.must("", "claim", "idea")

	server := h.serve()
	server.call("who", `{}`)

	other.must("", "rename", "review")

	answer := server.call("claim", `{"name":"idea"}`)
	if strings.Contains(answer, `"isError":true`) {
		t.Fatalf("a name a rename freed should be free to claim: %s", answer)
	}

	view, code := h.holderJSON("review", "--json")
	if code != 0 || view["session_id"] != other.session {
		t.Errorf("claiming a freed name should leave the renamed seat with its holder: %v", view)
	}
}

func TestTwoSessionsClaimingOneSeatAtOnceAreNeverBothSeated(t *testing.T) {
	for round := range 40 {
		h := newHarness(t)
		other := newHarnessSharing(h)
		h.must("", "claim", "main")
		h.must("", "release")

		var wait sync.WaitGroup

		codes := make([]int, 2)

		for index, claimer := range []*harness{h, other} {
			wait.Add(1)

			go func() {
				defer wait.Done()

				codes[index] = claimer.run("", "claim", "main").code
			}()
		}

		wait.Wait()

		if codes[0] == 0 && codes[1] == 0 {
			t.Fatalf("round %d: two sessions claiming one free seat at once should never both be told they hold it", round)
		}

		if codes[0] != 0 && codes[1] != 0 {
			t.Fatalf("round %d: one of two sessions claiming one free seat at once should hold it", round)
		}
	}
}

func TestAServerSeesAShelfMadeAfterItStarted(t *testing.T) {
	h := newHarness(t)
	other := newHarnessSharing(h)
	other.must("", "claim", "idea")
	other.must("", "release")

	server := h.serve()
	server.call("who", `{}`)

	other.must("", "claim", "impl", "--shelf", "board")

	answer := server.call("who", `{}`)
	if !strings.Contains(answer, `"shelf":"board"`) {
		t.Errorf("a server should name a shelf made after it started: %s", answer)
	}
}

// atOnce runs every step at the same time and waits for all of them.
func atOnce(steps ...func()) {
	var wait sync.WaitGroup

	for _, step := range steps {
		wait.Add(1)

		go func() {
			defer wait.Done()

			step()
		}()
	}

	wait.Wait()
}

func TestTwoReservationsOfOneSeatAtOnceNeverBothLand(t *testing.T) {
	for round := range 40 {
		h := newHarness(t)
		other := newHarnessSharing(h)
		h.must("", "claim", "impl")
		h.must("", "release")

		var first, second int

		atOnce(
			func() { first = h.run("", "claim", "impl", "--session", "f00dbabe-0000-4000-8000-000000000001").code },
			func() { second = other.run("", "claim", "impl", "--session", "f00dbabe-0000-4000-8000-000000000002").code },
		)

		if first == 0 && second == 0 {
			t.Fatalf("round %d: two reservations of one free seat for two sessions should never both land", round)
		}
	}
}

func TestASweepNeverFreesAClaimWrittenSinceItJudged(t *testing.T) {
	for round := range 40 {
		h := newHarness(t)
		other := newHarnessSharing(h)
		h.must("", "claim", "main")
		h.must("", "release")
		test.ClaimDeadSeat(t, h.store(), seatID(t, h.store(), "main"), "deadbeef-0000-4000-8000-000000000000")

		var claimed int

		atOnce(
			func() { h.run("", "who") },
			func() { claimed = other.run("", "claim", "main").code },
		)

		view, code := h.holderJSON("main", "--json")
		if claimed == 0 && (code != 0 || view["session_id"] != other.session) {
			t.Fatalf("round %d: a session told it holds a seat should still hold it after a sweep: %v", round, view)
		}
	}
}

func TestAReleaseOfAGoneHoldersSeatNeverFreesANewClaim(t *testing.T) {
	for round := range 40 {
		h := newHarness(t)
		other := newHarnessSharing(h)
		h.must("", "claim", "main")
		h.must("", "release")
		test.ClaimDeadSeat(t, h.store(), seatID(t, h.store(), "main"), "deadbeef-0000-4000-8000-000000000000")

		var claimed int

		atOnce(
			func() { h.run("", "release", "main") },
			func() { claimed = other.run("", "claim", "main").code },
		)

		view, code := h.holderJSON("main", "--json")
		if claimed == 0 && (code != 0 || view["session_id"] != other.session) {
			t.Fatalf("round %d: a session told it holds a seat should still hold it after a release naming it: %v", round, view)
		}
	}
}

func TestAHookNeverRestoresASeatItsSessionReleased(t *testing.T) {
	cleared, _ := json.Marshal(map[string]string{"session_id": "c1ea4ed0-0000-4000-8000-000000000000", "prompt": "go on"})

	for round := range 40 {
		h := newHarness(t)
		h.must("", "claim", "main")

		var released int

		atOnce(
			func() { released = h.run("", "release").code },
			func() { h.run(string(cleared), "prompt") },
		)

		view, _ := h.holderJSON("main", "--json")
		if released == 0 && view["session_id"] != nil {
			t.Fatalf("round %d: a seat its session released should keep no claim after that session's hook: %v", round, view)
		}
	}
}

func TestAHookNeverRewritesAClaimAnotherSessionTook(t *testing.T) {
	cleared, _ := json.Marshal(map[string]string{"session_id": "c1ea4ed0-0000-4000-8000-000000000000", "prompt": "go on"})

	for round := range 60 {
		h := newHarness(t)
		other := newHarnessSharing(h)
		h.must("", "claim", "main")

		var claimed int

		atOnce(
			func() {
				h.run("", "release")
				claimed = other.run("", "claim", "main").code
			},
			func() { h.run(string(cleared), "prompt") },
		)

		view, code := h.holderJSON("main", "--json")
		if claimed == 0 && (code != 0 || view["session_id"] != other.session) {
			t.Fatalf("round %d: a claim another session took should keep naming that session: %v", round, view)
		}
	}
}
