package memory

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
	"github.com/fza/agentic-helpers/source/hooks/internal/transcript"
)

// A subagent's spawner names how far the subagent reaches into agentic-helpers
// with one line in the Agent prompt, `agentic-helpers: <level>`; no line names
// none. Each level adds to the one below: none the scratch folder and the
// machine line, rules the parent seat's rules, silent read-only memory,
// shared the shelf's notices at its tool calls, full a seat of its own on the
// parent's shelf. A subagent never gets more than its spawner holds.
type level int

const (
	levelNone level = iota
	levelRules
	levelSilent
	levelShared
	levelFull
)

var levelNames = []string{"none", "rules", "silent", "shared", "full"}

func (held level) String() string {
	return levelNames[held]
}

func levelNamed(name string) (level, bool) {
	index := slices.Index(levelNames, name)
	if index < 0 {
		return levelNone, false
	}

	return level(index), true
}

// agentPending is what a seat held for a full subagent names as its agent
// until the subagent's first hook says its id.
const agentPending = "pending"

// rulesChars bounds the rules a subagent's prompt gains, a third of the brief,
// as the brief bounds its own rules section.
const rulesChars = briefBudget / 3

var (
	markerLine = regexp.MustCompile(`^\s*agentic-helpers:\s*(none|rules|silent|shared|full)\s*$`)
	// levelLine is the first line of the block a prompt gains, which the
	// subagent's hooks read its level back from; the seat is a full one's.
	levelLine = regexp.MustCompile(`^agentic-helpers level: (none|rules|silent|shared|full)(?:, seat (\S+))?(?:\s|$)`)
	// agentIDShape is what an agent id may hold before it names a file.
	agentIDShape = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	sessionShape = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// requestedLevel is the level the prompt's first marker line names, none where
// it carries none.
func requestedLevel(prompt string) (level, bool) {
	for _, line := range strings.Split(prompt, "\n") {
		found := markerLine.FindStringSubmatch(line)
		if found != nil {
			named, _ := levelNamed(found[1])

			return named, true
		}
	}

	return levelNone, false
}

// levelIn is the level and seat the block's level line in a prompt names;
// found is false where the prompt holds no such line, as a none block does not.
func levelIn(prompt string) (level, string, bool) {
	for _, line := range strings.Split(prompt, "\n") {
		found := levelLine.FindStringSubmatch(line)
		if found != nil {
			named, _ := levelNamed(found[1])

			return named, found[2], true
		}
	}

	return levelNone, "", false
}

// rewritePrompt puts the block where the prompt's first marker line stood,
// or above the prompt where none did. A later marker line stays as text, so
// a spawner can hand a level on for a nested Agent call, which the cap at
// the subagent's own level still bounds. Any line in the shape of a level
// line goes: only the hook names a level the subagent's hooks read back.
// The block a prompt already carries, line for line, stands where it is: an
// Agent call re-issued with the input the hook produced must reach the
// permission review unchanged, or auto mode never judges it.
func rewritePrompt(prompt string, block []string) string {
	var kept []string

	placed := false

	lines := strings.Split(prompt, "\n")

	for index := 0; index < len(lines); index++ {
		line := lines[index]

		switch {
		case !placed && len(block) > 0 && slices.Equal(lines[index:min(index+len(block), len(lines))], block):
			kept = append(kept, block...)
			placed = true
			index += len(block) - 1
		case !placed && markerLine.MatchString(line):
			kept = append(kept, block...)
			placed = true
		case strings.HasPrefix(strings.TrimSpace(line), "agentic-helpers level:"):
		default:
			kept = append(kept, line)
		}
	}

	if !placed && len(block) > 0 {
		kept = append(append(slices.Clone(block), ""), kept...)
	}

	return strings.Join(kept, "\n")
}

// subagentRecord is what a session keeps of one of its subagents: its level
// and, at full, the key of the seat it holds.
type subagentRecord struct {
	Level string `json:"level"`
	Seat  string `json:"seat,omitempty"`
}

func (record subagentRecord) level() level {
	named, _ := levelNamed(record.Level)

	return named
}

type subagentTable struct {
	Agents map[string]subagentRecord `json:"agents"`
}

func (mem *memory) subagentsKey(session string) string { return mem.sessionKey("agents/", session) }

// recordable is whether the store can keep a session's subagents: the table
// lives in its slot file.
func (mem *memory) recordable() bool {
	return mem.key != "" && !mem.outsideGit && mem.unreadable == "" && mem.storeExists()
}

func (mem *memory) subagentTable(session string) subagentTable {
	var table subagentTable
	if mem.recordable() && session != "" {
		mem.readSlot(mem.subagentsKey(session), &table)
	}

	return table
}

// changeSubagent rewrites one subagent's record under the slot file's lock.
func (mem *memory) changeSubagent(ctx context.Context, session string, agentID string,
	change func(record *subagentRecord),
) {
	if !mem.recordable() || session == "" {
		return
	}

	mem.updateSlot(ctx, mem.subagentsKey(session), func(value []byte, found bool) (slots.Fitted, error) {
		var table subagentTable
		if found {
			_ = json.Unmarshal(value, &table)
		}

		if table.Agents == nil {
			table.Agents = map[string]subagentRecord{}
		}

		record := table.Agents[agentID]
		change(&record)
		table.Agents[agentID] = record

		return &table, nil
	})
}

func subagentTranscript(payload hookio.Payload) string {
	return transcript.OfSubagent(payload.TranscriptPath, payload.AgentTranscriptPath, payload.AgentID)
}

// subagent is the calling subagent's record: as the session keeps it, else
// learnt from the level line its prompt carries, which its own transcript
// opens with, and kept. A prompt not written yet learns nothing and keeps
// nothing, so a later call learns it again.
func (mem *memory) subagent(ctx context.Context, payload hookio.Payload) subagentRecord {
	held, found := mem.subagentTable(payload.SessionID).Agents[payload.AgentID]
	if found && held.Level != "" {
		return held
	}

	path := subagentTranscript(payload)
	if path == "" {
		return subagentRecord{Level: levelNone.String()}
	}

	prompt, err := transcript.FirstPrompt(ctx, path)
	if err == nil {
		_, _, carries := levelIn(prompt)
		if carries {
			return mem.learnSubagent(ctx, payload.SessionID, payload.AgentID, prompt)
		}
	}

	// A fork's transcript opens with a reference to its spawner's context,
	// not with its prompt: its level is the one its spawn kept.
	spawned, found := mem.spawnOf(payload)
	if found {
		line := "agentic-helpers level: " + spawned.Level
		if spawned.Seat != "" {
			line += ", seat " + spawned.Seat
		}

		return mem.learnSubagent(ctx, payload.SessionID, payload.AgentID, line)
	}

	if err != nil || prompt == "" {
		return subagentRecord{Level: levelNone.String()}
	}

	return mem.learnSubagent(ctx, payload.SessionID, payload.AgentID, prompt)
}

// forkSpawn is what an Agent call spawning a fork keeps for it under the
// call's id: the level granted, a full one's seat, and its spawner's shown
// set as the spawn found it, since a fork's context is its spawner's.
type forkSpawn struct {
	Level string   `json:"level"`
	Seat  string   `json:"seat,omitempty"`
	Shown shownSet `json:"shown"`
}

func spawnCall(payload hookio.Payload) string {
	return transcript.SpawnCall(subagentTranscript(payload))
}

// spawnOf is what the subagent's spawn kept for it, where it is a fork.
func (mem *memory) spawnOf(payload hookio.Payload) (forkSpawn, bool) {
	var spawned forkSpawn

	call := spawnCall(payload)
	if call == "" || !mem.recordable() {
		return spawned, false
	}

	found := mem.readSlot(mem.spawnKey(payload.SessionID, call), &spawned)

	return spawned, found && spawned.Level != ""
}

// keepForFork keeps what a fork the Agent call spawns takes over at its
// first hook: the spawner's shown set as it stands, the main thread's or the
// spawning subagent's own. A fork at every level takes it, since trap and
// note notices reach a fork below silent too.
func (mem *memory) keepForFork(payload hookio.Payload, granted level, seatName string) {
	if payload.ToolInput.SubagentType != "fork" || payload.ToolUseID == "" || !mem.recordable() ||
		payload.SessionID == "" {
		return
	}

	spawned := forkSpawn{Level: granted.String(), Seat: seatName}

	key := mem.shownKey(payload.SessionID)
	if payload.AgentID != "" {
		key = mem.agentShownKey(payload.SessionID, payload.AgentID)
	}

	mem.readSlot(key, &spawned.Shown)
	mem.writeSlot(mem.spawnKey(payload.SessionID, payload.ToolUseID), &spawned)
}

// adoptSpawn starts the calling subagent's shown set, once: a fork's from
// the set its spawn kept, which is then spent; a fresh one's stays empty.
func (mem *memory) adoptSpawn(payload hookio.Payload) {
	if !mem.recordable() || payload.SessionID == "" || mem.hasSlot(mem.agentShownKey(payload.SessionID, payload.AgentID)) {
		return
	}

	spawned, found := mem.spawnOf(payload)
	if !found {
		return
	}

	mem.writeSlot(mem.agentShownKey(payload.SessionID, payload.AgentID), &spawned.Shown)
	mem.clearSlot(mem.spawnKey(payload.SessionID, spawnCall(payload)))
}

// learnSubagent keeps the level the prompt's level line names for the agent,
// binding a full one's seat to it; a seat no longer waiting for the session's
// subagent leaves the subagent at shared.
func (mem *memory) learnSubagent(ctx context.Context, session string, agentID string, prompt string) subagentRecord {
	named, seatName, _ := levelIn(prompt)
	learnt := subagentRecord{Level: named.String()}

	if named == levelFull {
		learnt.Seat = mem.bindSubagentSeat(ctx, session, agentID, seatName)
		if learnt.Seat == "" {
			learnt.Level = levelShared.String()
		}
	}

	mem.changeSubagent(ctx, session, agentID, func(record *subagentRecord) {
		if record.Level == "" {
			record.Level, record.Seat = learnt.Level, learnt.Seat
		}

		learnt = *record
	})

	return learnt
}

// bindSubagentSeat names the agent on the seat held for a subagent of the
// session, answering the seat's key; nothing where the seat holds another.
func (mem *memory) bindSubagentSeat(ctx context.Context, session string, agentID string, seatName string) string {
	if seatName == "" || !mem.recordable() {
		return ""
	}

	key := mem.seatKey(seatName)
	if key == "" {
		return ""
	}

	bound := false

	mem.rewriteClaim(ctx, key, func(current seat.Claim) bool {
		return current.SessionID == session && (current.Agent == agentPending || current.Agent == agentID)
	}, func(claim *seat.Claim) {
		claim.Agent = agentID
		bound = true
	})

	if !bound {
		return ""
	}

	return key
}

func (mem *memory) subagentSeenKey(agentID string) string { return "seen/agent/" + agentID }

// spawnerLevel is the most a spawn may grant: everything for the main thread,
// which names no agent, else the spawning subagent's own level.
func (mem *memory) spawnerLevel(ctx context.Context, payload hookio.Payload) level {
	if payload.AgentID == "" {
		return levelFull
	}

	return mem.subagent(ctx, payload).level()
}

// spawn answers the PreToolUse hook on the Agent tool: the prompt's marker
// line gives way to the block of the level granted, capped at the spawner's
// and at what the session can give. usable says memory may be read here.
func (mem *memory) spawn(ctx context.Context, payload hookio.Payload, usable bool) int {
	prompt := payload.ToolInput.Prompt
	requested, marked := requestedLevel(prompt)
	granted := min(requested, mem.spawnerLevel(ctx, payload))

	parent := ""
	if usable && mem.storeExists() {
		parent, _ = mem.ownSeat(ctx)
	} else {
		granted = min(granted, levelRules)
	}

	if parent == "" {
		granted = min(granted, levelSilent)
	}

	held := ""

	if granted == levelFull {
		name, err := mem.holdForSubagent(ctx, parent, payload.SessionID)
		if err != nil {
			granted = levelShared
		}

		held = name
	}

	mem.keepForFork(payload, granted, held)

	block := mem.subagentBlock(ctx, payload, granted, parent, held)

	_, _, spoofed := levelIn(prompt)
	if len(block) == 0 && !marked && !spoofed {
		return 0
	}

	rewritten := rewritePrompt(prompt, block)
	if rewritten == prompt {
		return 0
	}

	updated, err := payload.ToolInput.WithField("prompt", rewritten)
	if err != nil {
		return 0
	}

	_ = hookio.Rewrite(mem.streams.Out, updated)

	return 0
}

// subagentBlock is what the prompt gains at the level granted: the level line
// above none, the scratch folder, the machine line, the parent seat's rules
// from rules, and the memory line from silent.
func (mem *memory) subagentBlock(ctx context.Context, payload hookio.Payload, granted level, parent string,
	held string,
) []string {
	var block []string

	if granted > levelNone {
		line := "agentic-helpers level: " + granted.String()
		if held != "" {
			line += ", seat " + held
		}

		block = append(block, line+" (set by the spawner; agentic-helpers:agent-memory says what each level holds)")
	}

	scratchLine := mem.subagentScratch(ctx, parent, payload.SessionID)
	if scratchLine != "" {
		block = append(block, scratchLine)
	}

	machine := pieces.MachineNeeds(ctx, mem.options(), mem.getenv("PATH"), mem.env.Roots.Cache)
	if machine != "" {
		block = append(block, "agentic-helpers: "+machine)
	}

	if granted >= levelRules && parent != "" {
		block = append(block, mem.subagentRules(ctx, parent)...)
	}

	if granted >= levelSilent {
		block = append(block, mem.subagentMemoryLine(granted, parent, held))
	}

	return block
}

func (mem *memory) getenv(name string) string {
	if mem.env.Getenv == nil {
		return ""
	}

	return mem.env.Getenv(name)
}

// subagentScratch is the folder a subagent writes its throwaway to: its
// spawner's, the parent seat's or the session's, so what it leaves stays
// where the spawner looks.
func (mem *memory) subagentScratch(ctx context.Context, parent string, session string) string {
	if !pieces.Scratch.On(mem.options()) {
		return ""
	}

	if parent != "" {
		return mem.seatScratch(parent)
	}

	root := mem.scratchRoot()
	if mem.facts.Root == "" && mem.facts.CommonDir == "" {
		root = mem.mainRoot(ctx)
	}

	info, err := os.Stat(filepath.Join(root, ".tmp"))
	if err != nil || !info.IsDir() || !sessionShape.MatchString(session) {
		return ""
	}

	return "scratch: " + filepath.ToSlash(filepath.Join(".tmp", "seats", session)) + "/"
}

// subagentRules is the parent seat's live rules as the brief lists them,
// each with its body, within a third of the brief.
func (mem *memory) subagentRules(ctx context.Context, parent string) []string {
	folded, err := mem.load(ctx)
	if err != nil {
		return nil
	}

	here := mem.checkout(ctx)
	scope := &viewScope{mem: mem, ctx: ctx, folded: folded, here: here, reader: parent}

	sections, err := scope.compileLayout(fmt.Sprintf(`active:mine:kind(%s):rank(heat):name(%q):brief:as-list`,
		kindRule, rulesHeading))
	if err != nil || len(sections) == 0 {
		return nil
	}

	page, _ := scope.run(sections[0])
	if len(page) == 0 {
		return nil
	}

	lines := []string{rulesHeading + ":"}
	used := 0

	for index, held := range page {
		rule := []string{"  " + shortLine(here, held, parent, mem.now())}

		for _, text := range strings.Split(strings.TrimSpace(held.body), "\n") {
			if strings.TrimSpace(text) != "" {
				rule = append(rule, "    "+text)
			}
		}

		size := len(strings.Join(rule, "\n"))
		if used+size > rulesChars && index > 0 {
			lines = append(lines, fmt.Sprintf("  +%d more rules on seat %s", len(page)-index, parent))

			break
		}

		used += size
		lines = append(lines, rule...)
	}

	return lines
}

// subagentSkills names the skills a subagent with memory loads, and when.
const subagentSkills = "Load agentic-helpers:agent-memory before reading or filing memory, " +
	"agentic-helpers:agent-scratch before writing a probe or a draft, and agentic-helpers:agent-shell-guard " +
	"when a hook refuses a call."

func (mem *memory) subagentMemoryLine(granted level, parent string, held string) string {
	if parent == "" {
		return fmt.Sprintf("%s: read-only (search, show, brief, topics, view, who); every write is refused. %s",
			self, subagentSkills)
	}

	shelf := mem.shelfName(mem.shelfOf(parent))

	switch granted {
	case levelFull:
		return fmt.Sprintf("%s: the subagent holds seat %s on shelf %s while it runs: file what outlives the task "+
			"with %s new, write where it stands with %s state; its shelf mates are told of its entries and it of "+
			"theirs. The seat is released when it stops. %s", self, held, shelf, self, self, subagentSkills)
	case levelShared:
		return fmt.Sprintf("%s: read-only on seat %s (search, show, brief, topics, view, who); every write is "+
			"refused. A line at its tool calls names what other seats on shelf %s file or change. %s", self, parent,
			shelf, subagentSkills)
	}

	return fmt.Sprintf("%s: read-only on seat %s (search, show, brief, topics, view, who); every write is refused. %s",
		self, parent, subagentSkills)
}

// holdForSubagent mints the next free `<shelf>-<n>` seat on the parent's
// shelf and holds it for a subagent of the session, pending until the
// subagent's first hook names its agent id.
func (mem *memory) holdForSubagent(ctx context.Context, parent string, session string) (string, error) {
	name, err := mem.mintShared(ctx, mem.shelfName(mem.shelfOf(parent)))
	if err != nil {
		return "", err
	}

	owner := seat.Owner(ctx, mem.env.Processes, mem.env.OwnerName)
	held := seat.Claim{
		SessionID: session, PID: owner, Claimed: mem.now().Format(time.RFC3339Nano),
		Commit: mem.checkout(ctx).commit, Agent: agentPending,
	}

	started, running := mem.env.Processes.StartTime(ctx, owner)
	if running {
		held.Started = seat.FormatStarted(started)
	}

	dir := mem.seatsDir()

	unlock, err := seat.Lock(ctx, mem.env.Roots.Data, dir)
	if err != nil {
		return "", err
	}
	defer unlock()

	err = seat.Write(mem.env.Roots.Data, dir, mem.seatKey(name), held)
	if err != nil {
		return "", fmt.Errorf("holding %s for the subagent: %w", name, err)
	}

	mem.recordStore(ctx)

	return name, nil
}

// subagentReads are the subcommands a subagent reading memory may run.
var subagentReads = []string{"search", "show", "brief", "topics", "view", "who", "help", "--help", "-h"}

// keptFromSubagents stay with the session: a subagent claiming, releasing or
// moving a seat would change which seat its spawner's own calls reach.
var keptFromSubagents = []string{
	"claim", "release", "rename", "private", "lifetime", "drop", "bind", "merge", "follow", "unfollow", "mcp",
}

// subagentRefusal is why a subagent at the level may not run the subcommand,
// or nothing where it may.
func subagentRefusal(verb string, at level) string {
	switch {
	case at < levelSilent:
		return fmt.Sprintf("%s: the subagent runs at agentic-helpers level %s, which gives it no memory access; "+
			"its spawner names the level with the line `agentic-helpers: <level>` in the Agent prompt", self, at)
	case slices.Contains(keptFromSubagents, verb):
		return fmt.Sprintf("%s: %s stays with the session that spawned the subagent; a subagent never claims, "+
			"releases or moves a seat", self, cmp.Or(verb, "the call"))
	case at < levelFull && !slices.Contains(subagentReads, verb):
		return fmt.Sprintf("%s: the subagent runs at agentic-helpers level %s, which reads memory but never "+
			"writes it; report what is worth keeping to the spawner instead", self, at)
	}

	return ""
}

// memoryToolPrefix opens the name of every tool the plugin's memory server
// offers.
const memoryToolPrefix = "mcp__plugin_agentic-helpers_memory__"

// agentKey is the key every memory MCP tool takes for the subagent a call
// comes from, which the PreToolUse hook sets.
const agentKey = "agent"

// guardTool answers the PreToolUse hook on a memory MCP tool: an `agent` the
// main thread sends is refused; a subagent's call its level does not allow is
// refused, and every other call of one gains `agent` naming it, any it
// carried replaced.
func (mem *memory) guardTool(ctx context.Context, payload hookio.Payload) int {
	if payload.AgentID == "" {
		var sent map[string]json.RawMessage

		_ = json.Unmarshal(payload.ToolInput.Raw, &sent)
		if _, named := sent[agentKey]; named {
			_ = hookio.Deny(mem.streams.Out, self+": agent is set by the hook for a subagent reading memory, never "+
				"sent; call the tool without it")
		}

		return 0
	}

	record := mem.subagent(ctx, payload)

	refusal := subagentRefusal(strings.TrimPrefix(payload.ToolName, memoryToolPrefix), record.level())
	if refusal != "" {
		_ = hookio.Deny(mem.streams.Out, refusal)

		return 0
	}

	mem.adoptSpawn(payload)

	updated, err := payload.ToolInput.WithField(agentKey, payload.AgentID)
	if err != nil {
		return 0
	}

	_ = hookio.Rewrite(mem.streams.Out, updated)

	return 0
}

// actAs points the run at the session's subagent agentID, which the PreToolUse
// hook sets under name (`--agent` or the MCP key `agent`): its reads mark its
// own shown set, and a full one's calls work on its seat. It answers why not
// where the session keeps no such subagent, a full one's seat is gone, or the
// subagent's level does not allow the verb, which refuses every verb below
// silent.
func (mem *memory) actAs(session string, agentID string, verb string, name string) string {
	record, found := mem.subagentTable(session).Agents[agentID]
	if session == "" || !found || (record.level() == levelFull && record.Seat == "") {
		return fmt.Sprintf("%s names no subagent of the calling session reading memory; the PreToolUse hook "+
			"sets it for one, and it is never typed: run %s without it", name, self)
	}

	refusal := subagentRefusal(verb, record.level())
	if refusal != "" {
		return strings.TrimPrefix(refusal, self+": ")
	}

	mem.reader = agentID
	if record.level() == levelFull {
		mem.acting = record.Seat
	}

	return ""
}

// actingSeat is the seat a run acting for a full subagent works on, while its
// claim still names that subagent.
func (mem *memory) actingSeat() (string, seat.Claim) {
	held, found := seat.Read(mem.seatsDir(), mem.acting)
	if !found || held.Agent == "" || held.Agent == agentPending {
		return "", seat.Claim{}
	}

	return mem.seatName(mem.acting), held
}

// subagentStop removes the subagent's shown set, and what its spawn kept for
// a fork that never called memory, as SubagentStop fires for it, and
// releases the seat a full one held: its claim closes and its end conditions
// are reached, as a release does.
func (mem *memory) subagentStop(ctx context.Context, _ []string) int {
	if !mem.storeExists() {
		return 0
	}

	payload := mem.payload()
	if payload.AgentID == "" {
		return 0
	}

	if mem.recordable() && payload.SessionID != "" {
		mem.clearSlot(mem.agentShownKey(payload.SessionID, payload.AgentID))

		call := spawnCall(payload)
		if call != "" {
			mem.clearSlot(mem.spawnKey(payload.SessionID, call))
		}
	}

	record := mem.subagent(ctx, payload)
	if record.Seat == "" {
		return 0
	}

	mem.releaseSubagentSeat(ctx, record.Seat)
	mem.changeSubagent(ctx, payload.SessionID, payload.AgentID, func(held *subagentRecord) {
		held.Seat = ""
	})

	return 0
}

func (mem *memory) releaseSubagentSeat(ctx context.Context, key string) {
	name := mem.seatName(key)

	released, _, err := mem.closeSeat(ctx, name, key, seat.Owner(ctx, mem.env.Processes, mem.env.OwnerName))
	if err != nil || !released {
		return
	}

	mem.endSession(ctx, name)
	_ = mem.purgeExpired(ctx)
}

// endSubagents releases every seat still held for a subagent of the session
// that ends, as one whose SubagentStop never fired, and forgets its
// subagents, their shown sets and what spawns kept for forks.
func (mem *memory) endSubagents(ctx context.Context, session string) {
	if session == "" {
		return
	}

	held := seat.Held(mem.seatsDir())
	for _, key := range seat.Names(held) {
		if held[key].Agent != "" && held[key].SessionID == session {
			mem.releaseSubagentSeat(ctx, key)
		}
	}

	mem.clearSlot(mem.subagentsKey(session))

	if mem.recordable() {
		err := mem.slotFile().Reclaim(ctx, func(slot slots.Slot) bool {
			return strings.HasPrefix(slot.Key, mem.agentShownKey(session, "")) ||
				strings.HasPrefix(slot.Key, mem.spawnKey(session, ""))
		})
		mem.reportSlot("clearing the subagents of", session, err)
	}
}

// subagentNotices are the lines a subagent's tool call gains: at shared what
// the parent's shelf filed or changed since its last look, against a mark of
// its own, so the seat's mark stays where it was; at full what its own seat
// is told; nothing below.
func (mem *memory) subagentNotices(ctx context.Context, payload hookio.Payload, parent string) []string {
	record := mem.subagent(ctx, payload)

	reader, markKey := parent, mem.subagentSeenKey(payload.AgentID)

	switch {
	case record.level() == levelFull && record.Seat != "":
		reader = mem.seatName(record.Seat)
		markKey = mem.seenKey(reader)
	case record.level() < levelShared || parent == "":
		return nil
	}

	folded, err := mem.load(ctx)
	if err != nil {
		return nil
	}

	comeDue := func() string { return "" }
	if record.level() == levelFull {
		comeDue = func() string { return mem.comeDue(ctx, folded, reader) }
	}

	told := mem.announceTo(ctx, folded, reader, markKey, comeDue)
	if told == "" {
		return nil
	}

	return []string{told}
}

// learnBackground keeps the level of a subagent the Agent call started in the
// background, whose PostToolUse names its agent id before the subagent's
// first tool call; the input it reads is the rewritten one where it carries
// the level line.
func (mem *memory) learnBackground(ctx context.Context, payload hookio.Payload) {
	if !isAgentTool(payload.ToolName) {
		return
	}

	_, _, carries := levelIn(payload.ToolInput.Prompt)
	if !carries {
		return
	}

	raw := payload.ToolResponse

	var text string

	err := json.Unmarshal(raw, &text)
	if err == nil {
		raw = json.RawMessage(text)
	}

	var response struct {
		AgentID string `json:"agentId"`
	}

	err = json.Unmarshal(raw, &response)
	if err != nil || !agentIDShape.MatchString(response.AgentID) {
		return
	}

	mem.learnSubagent(ctx, payload.SessionID, response.AgentID, payload.ToolInput.Prompt)
}

func isAgentTool(name string) bool {
	return name == "Agent" || name == "Task"
}

// preToolHook is the PreToolUse hook on the Agent tool, on Bash and on the
// memory MCP tools.
const preToolHook = "pre-tool"

// preTool answers the PreToolUse hook. usable is false where memory is off
// or cannot be read here: a spawn then still gains the scratch folder and the
// machine line, and nothing is guarded, since no memory call can run.
func (mem *memory) preTool(ctx context.Context, usable bool) int {
	payload := mem.payload()

	switch {
	case isAgentTool(payload.ToolName):
		return mem.spawn(ctx, payload, usable)
	case !usable:
		return 0
	case payload.ToolName == "Bash":
		return mem.guardShell(ctx, payload)
	case strings.HasPrefix(payload.ToolName, memoryToolPrefix):
		return mem.guardTool(ctx, payload)
	}

	return 0
}

// preToolEvent runs the subagent checks, then hands the main thread the line
// its last Stop owed where the checks gave the call no answer: a rewrite or a
// refusal is the call's one answer, and the call's PostToolUse carries the
// line instead.
func (mem *memory) preToolEvent(ctx context.Context, _ []string) int {
	out := mem.streams.Out

	var answer bytes.Buffer

	mem.streams.Out = &answer
	code := mem.preTool(ctx, true)
	mem.streams.Out = out

	payload := mem.payload()
	if answer.Len() > 0 || payload.AgentID != "" {
		_, _ = out.Write(answer.Bytes())

		return code
	}

	owed := mem.owedLine(payload.SessionID)
	if owed != "" {
		_ = hookio.AddContext(out, "PreToolUse", owed)
	}

	return code
}

// agentArgs takes a leading `--agent <id>` off the arguments, answering the id.
func agentArgs(args []string) (string, []string, error) {
	switch {
	case args[0] == agentFlag && len(args) > 2:
		return args[1], args[2:], nil
	case strings.HasPrefix(args[0], agentFlag+"=") && len(args) > 1:
		return strings.TrimPrefix(args[0], agentFlag+"="), args[1:], nil
	case args[0] == agentFlag || strings.HasPrefix(args[0], agentFlag+"="):
		return "", nil, ErrUsage
	}

	return "", args, nil
}
