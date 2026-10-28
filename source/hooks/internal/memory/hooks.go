package memory

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"github.com/fza/agentic-helpers/source/hooks/internal/promptvector"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
	"github.com/fza/agentic-helpers/source/hooks/internal/seat"
	"github.com/fza/agentic-helpers/source/hooks/internal/transcript"
)

// Claude Code inlines a hook's output only up to 10,000 characters and swaps
// anything longer for a file path with a short preview, which a compaction
// summary then outranks. So the recent turns arrive in pieces under that
// limit, one per `session-log` hook.
const (
	pieceChars = 9000
	maxPieces  = 5
)

const (
	contextPercentThreshold  = 65
	rearmPercentMargin       = 10
	transcriptDeltaThreshold = 400 * 1024
	sensorMaxAge             = 5 * time.Minute
	nudgesBeforeBackstop     = 2
	staleAfter               = 7 * 24 * time.Hour
	recallFloorOwn           = 2.0
	recallFloorOther         = 3.0
	recallMinimum            = 20
	recallHits               = 3
	recallLimit              = 5 * time.Second
	waitPoll                 = 5 * time.Second
	waitDeadline             = 30 * time.Minute
)

// stateFirst is what both nudges ask for: where work stands goes into the
// state slot, and an entry is filed only for what outlives the moment.
const stateFirst = "Write where work stands first: " + self + " state < body. " +
	"File an entry only for a finding, an open question or a judgment call: " +
	self + " new --kind <kind> --title '<3-10 words>' --topic <name>... < body"

const nudgeText = "Context near compaction. " + stateFirst

const backstopText = "Memory overdue, two nudges unanswered: write where work stands with " + self +
	" state now, and file findings with " + self + " new, before ending the turn."

type hookEvent func(mem *memory, ctx context.Context, args []string) int

var hookEvents = map[string]hookEvent{
	"session-start": (*memory).sessionStart,
	"session-log":   (*memory).sessionLog,
	"prompt":        (*memory).prompt,
	"stop":          (*memory).stop,
	"session-end":   (*memory).sessionEnd,
	"tool":          (*memory).tool,
	"wait":          (*memory).wait,
}

func (mem *memory) payload() hookio.Payload {
	payload, err := hookio.ReadPayload(mem.streams.In)
	if err != nil {
		return hookio.Payload{}
	}

	return payload
}

// track keeps the claim pointing at the session and transcript its process
// runs now: a `/clear` starts a new session id and transcript in the same
// process, and a claim made from the Bash tool knew neither transcript.
func (mem *memory) track(ctx context.Context, seatName string, held seat.Claim, payload hookio.Payload) seat.Claim {
	changed := false

	if payload.SessionID != "" && payload.SessionID != held.SessionID {
		held.SessionID = payload.SessionID
		changed = true
	}

	if payload.TranscriptPath != "" && payload.TranscriptPath != held.TranscriptPath {
		held.TranscriptPath = payload.TranscriptPath
		changed = true
	}

	// Read again while empty: a claim made before the session's first prompt
	// finds no record yet, and a compaction empties it, since its boundary
	// record lands in the transcript only after the SessionStart hook it runs.
	if held.TranscriptPath != "" && (changed || held.ForkUUID == "") {
		start, _ := transcript.ForkStart(ctx, held.TranscriptPath)
		if start != "" && start != held.ForkUUID {
			held.ForkUUID = start
			changed = true
		}
	}

	if changed {
		mem.rewriteClaim(ctx, mem.seatKey(seatName), sameHolder(held), func(claim *seat.Claim) {
			claim.SessionID, claim.TranscriptPath, claim.ForkUUID = held.SessionID, held.TranscriptPath, held.ForkUUID
		})
	}

	return held
}

// sameHolder is whether a claim still names the process holding held: a
// claim another session wrote since stays as it is.
func sameHolder(held seat.Claim) func(seat.Claim) bool {
	return func(current seat.Claim) bool {
		return current.PID == held.PID && current.Started == held.Started
	}
}

// rewriteClaim changes the claim on the seat filed under key under the seats lock,
// where still holds for the claim found there.
func (mem *memory) rewriteClaim(ctx context.Context, key string, still func(seat.Claim) bool, change func(*seat.Claim)) {
	dir := mem.seatsDir()

	unlock, err := seat.Lock(ctx, dir)
	if err != nil {
		return
	}
	defer unlock()

	// A missing claim reads as the zero claim, which no caller's still accepts.
	current, _ := seat.Read(dir, key)
	if !still(current) {
		return
	}

	change(&current)
	_ = seat.Write(dir, key, current)
}

func (mem *memory) sessionStart(ctx context.Context, _ []string) int {
	mem.purgeEverywhere(ctx)

	if !mem.storeExists() {
		return 0
	}

	payload := mem.payload()
	mem.housekeep()
	mem.adopt(ctx, payload)

	name, held := mem.ownSeat(ctx)
	if name != "" {
		held = mem.track(ctx, name, held, payload)
		mem.say(mem.moved(ctx, name, held))

		if payload.Source == "compact" {
			mem.rewriteClaim(ctx, mem.seatKey(name), sameHolder(held), func(claim *seat.Claim) { claim.ForkUUID = "" })
		}

		if payload.Source == "compact" || payload.Source == "clear" {
			mem.resetWindow(held.SessionID)

			folded, err := mem.load(ctx)
			if err == nil {
				mem.say(mem.doNow(folded, name, onCompact, held.SessionID))
				mem.say(mem.brief(ctx, mem.checkout(ctx), folded, name))
			}
		}

		return 0
	}

	_ = mem.seatRows(ctx, true)

	inherited := mem.inherit(ctx, payload)
	if inherited != "" {
		mem.say(inherited)

		return 0
	}

	// Claude Code writes a fork's transcript only after its SessionStart hooks
	// return, so a fork is matched at its first prompt; told to claim now, the
	// agent would take a seat that prompt is about to give it.
	if payload.Source == "fork" && payload.SessionID != "" && !mem.transcriptStarted(ctx, payload) {
		_ = os.MkdirAll(filepath.Dir(mem.forkPath(payload.SessionID)), 0o700)
		_ = os.WriteFile(mem.forkPath(payload.SessionID), nil, 0o600)

		return 0
	}

	mem.say(mem.seatless(ctx))

	return 0
}

func (mem *memory) transcriptStarted(ctx context.Context, payload hookio.Payload) bool {
	if payload.TranscriptPath == "" {
		return false
	}

	first, _ := transcript.FirstUUID(ctx, payload.TranscriptPath)

	return first != ""
}

func (mem *memory) forkPath(session string) string {
	return filepath.Join(mem.cacheDir(), "forks", session)
}

// seatAtFirstPrompt matches a fork its SessionStart could not, now that its
// transcript holds its parent's records, once: the marker goes either way.
func (mem *memory) seatAtFirstPrompt(ctx context.Context, payload hookio.Payload, seated bool) string {
	if payload.SessionID == "" {
		return ""
	}

	marker := mem.forkPath(payload.SessionID)

	_, err := os.Stat(marker)
	if err != nil {
		return ""
	}

	removeIfThere(marker)

	if seated {
		return ""
	}

	inherited := mem.inherit(ctx, payload)
	if inherited != "" {
		return inherited
	}

	return mem.seatless(ctx)
}

// seatless is the notice a session holding no seat gets, naming every seat.
func (mem *memory) seatless(ctx context.Context) string {
	folded, err := mem.load(ctx)
	if err != nil {
		return ""
	}

	claims := seat.Held(mem.seatsDir())

	var named []string

	for _, key := range seat.Known(mem.seatsDir()) {
		seatName := mem.seatName(key)
		live := 0

		for _, entry := range folded.entryList() {
			if entry.seat == seatName && entry.status == statusLive {
				live++
			}
		}

		claim, found := claims[key]
		if found && mem.alive(ctx, claim) {
			named = append(named, fmt.Sprintf("%s (%s, %d live)", seatName, short(claim.SessionID), live))
		} else {
			named = append(named, fmt.Sprintf("%s (free, %d live)", seatName, live))
		}
	}

	seats := "none yet"
	if len(named) > 0 {
		seats = strings.Join(named, ", ")
	}

	return fmt.Sprintf("%s: this session holds no seat. Seats: %s. Claim one: %s claim <seat>", self, seats, self)
}

// housekeep removes per-session cache files nobody came back to.
func (mem *memory) housekeep() {
	cutoff := mem.now().Add(-staleAfter)

	for _, pattern := range []string{
		filepath.Join(mem.sensorDir(), "*.ctx"),
		filepath.Join(mem.cacheDir(), "shown", "*.json"),
		filepath.Join(mem.cacheDir(), "ladder", "*.json"),
		filepath.Join(mem.cacheDir(), "graph-seen", "*.json"),
		filepath.Join(mem.cacheDir(), "forks", "*"),
	} {
		paths, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}

		for _, path := range paths {
			info, err := os.Stat(path)
			if err == nil && info.ModTime().Before(cutoff) {
				removeIfThere(path)
			}
		}
	}
}

func (mem *memory) recentTurns(ctx context.Context, seatName string, transcriptPath string) string {
	folded, err := mem.load(ctx)
	if err != nil {
		return ""
	}

	turns, err := transcript.Since(ctx, transcriptPath, mem.newestOwn(folded, seatName))
	if err != nil {
		return ""
	}

	return transcript.Condense(turns)
}

func (mem *memory) sessionLog(ctx context.Context, args []string) int {
	if !mem.storeExists() {
		return 0
	}

	payload := mem.payload()
	if payload.Source != "compact" {
		return 0
	}

	name, _ := mem.ownSeat(ctx)
	if name == "" || len(args) != 1 {
		return 0
	}

	index, err := strconv.Atoi(args[0])
	if err != nil {
		return 0
	}

	pieces, cut := transcript.Pieces(mem.recentTurns(ctx, name, payload.TranscriptPath), pieceChars, maxPieces)
	if index >= 1 && index <= len(pieces) {
		switch {
		case index == 1 && cut > 0:
			mem.say(fmt.Sprintf("%s: the turns since seat %s's newest entry, in %d pieces, newest last; "+
				"%d older pieces cut, the full transcript at %s:", self, name, len(pieces), cut, payload.TranscriptPath))
		case index == 1:
			mem.say(fmt.Sprintf("%s: the turns since seat %s's newest entry, in %d pieces, newest last:",
				self, name, len(pieces)))
		}

		mem.say(pieces[index-1])
	}

	return 0
}

type ladder struct {
	Nudges         int       `json:"nudges"`
	Rung           int       `json:"rung"`
	TranscriptBase int64     `json:"transcript_base"`
	NudgedAt       time.Time `json:"nudged_at"`
	// ReplayNudgedFor is the newest own entry's time the replay nudge fired
	// for, and ReplayCheckedSize the transcript's size at its last check.
	ReplayNudgedFor   time.Time `json:"replay_nudged_for"`
	ReplayCheckedSize int64     `json:"replay_checked_size"`
	// ShipSince is when the window began measuring shipped work, ShipBase
	// HEAD then or the commit the seat's last state write stamps, StateSeen
	// that write's time, and Writes and Captured the file writes and graph
	// entries since. Held marks the shipped-work hold as spent until the next
	// state write.
	ShipSince time.Time `json:"ship_since"`
	ShipBase  string    `json:"ship_base,omitempty"`
	StateSeen time.Time `json:"state_seen"`
	Writes    int       `json:"writes,omitempty"`
	Captured  int       `json:"captured,omitempty"`
	Held      bool      `json:"held,omitempty"`
	// Wrote is the files those writes named and LastPrompt the session's last
	// prompt, both for a hook-written draft of the seat's state.
	Wrote      []string `json:"wrote,omitempty"`
	LastPrompt string   `json:"last_prompt,omitempty"`
}

const replayNudgeText = "Turns since your newest entry or state write no longer fit the post-compaction replay. " +
	stateFirst

// replayNudge asks the seat to file once its turns since its newest entry
// would no longer fit the replay a compaction brings back, once per newest
// entry. The transcript is read only after it grew by a piece since the last
// look. It counts toward neither Nudges nor the Stop backstop.
func (mem *memory) replayNudge(ctx context.Context, seatName string, transcriptPath string, held *ladder) string {
	folded, err := mem.load(ctx)
	if err != nil || transcriptPath == "" {
		return ""
	}

	newest := mem.newestOwn(folded, seatName)
	if !held.ReplayNudgedFor.IsZero() && held.ReplayNudgedFor.Equal(newest) {
		return ""
	}

	size := transcriptSize(transcriptPath)
	if size-held.ReplayCheckedSize < pieceChars {
		return ""
	}

	held.ReplayCheckedSize = size

	_, cut := transcript.Pieces(mem.recentTurns(ctx, seatName, transcriptPath), pieceChars, maxPieces)
	if cut == 0 {
		return ""
	}

	held.ReplayNudgedFor = newest

	return replayNudgeText
}

type sensor struct {
	UsedPercentage *float64   `json:"used_percentage"`
	At             sensorTime `json:"at"`
	Size           float64    `json:"size"`
}

// sensorTime is when the plugin wrote a reading: an RFC 3339 string, or the
// Unix seconds a plugin of an earlier release writes.
type sensorTime struct {
	time.Time
}

func (at *sensorTime) UnmarshalJSON(raw []byte) error {
	var seconds float64

	err := json.Unmarshal(raw, &seconds)
	if err == nil {
		at.Time = time.Unix(0, int64(seconds*float64(time.Second))).UTC()

		return nil
	}

	var text string

	err = json.Unmarshal(raw, &text)
	if err != nil {
		return fmt.Errorf("a reading time that is neither RFC 3339 nor Unix seconds: %s", raw)
	}

	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return fmt.Errorf("reading the reading time %q: %w", text, err)
	}

	at.Time = parsed.UTC()

	return nil
}

func (mem *memory) ladderPath(session string) string {
	return filepath.Join(mem.cacheDir(), "ladder", session+".json")
}

func (mem *memory) loadLadder(session string) ladder {
	var held ladder
	readJSON(mem.ladderPath(session), &held)

	if held.Rung == 0 {
		held.Rung = contextPercentThreshold
	}

	return held
}

// resetWindow starts a fresh context window: the ladder at its first rung, and
// nothing counted as shown.
func (mem *memory) resetWindow(session string) {
	removeIfThere(mem.ladderPath(session))
	removeIfThere(mem.shownPath(session))
}

// contextPercent is how full the session's context is, as a share of the
// window it compacts at, or false where no fresh reading exists.
func (mem *memory) contextPercent(session string) (float64, bool) {
	var reading sensor
	if !readJSON(filepath.Join(mem.sensorDir(), session+".ctx"), &reading) || reading.UsedPercentage == nil ||
		reading.At.IsZero() {
		return 0, false
	}

	if mem.now().Sub(reading.At.Time) > sensorMaxAge {
		return 0, false
	}

	percent := *reading.UsedPercentage

	window := float64(mem.env.CompactWindow)
	if reading.Size > 0 && window > 0 && window < reading.Size {
		return percent * reading.Size / window, true
	}

	return percent, true
}

func transcriptSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}

	return info.Size()
}

func (mem *memory) crossed(session string, transcriptPath string, held ladder) bool {
	percent, known := mem.contextPercent(session)
	if known {
		return percent >= float64(held.Rung)
	}

	return transcriptSize(transcriptPath)-held.TranscriptBase >= transcriptDeltaThreshold
}

// answer resets the ladder once the seat filed after the last nudge: any entry
// answers it. The rung climbs past where the context stands, since a rung
// already passed could never fire again inside this window.
func (mem *memory) answer(ctx context.Context, seatName string, session string, transcriptPath string, held ladder) ladder {
	if held.Nudges == 0 {
		return held
	}

	folded, err := mem.load(ctx)
	if err != nil || !mem.newestOwn(folded, seatName).After(held.NudgedAt) {
		return held
	}

	held.Nudges = 0
	held.TranscriptBase = transcriptSize(transcriptPath)
	held.Rung += rearmPercentMargin

	percent, known := mem.contextPercent(session)
	for known && float64(held.Rung) <= percent {
		held.Rung += rearmPercentMargin
	}

	return held
}

func (mem *memory) prompt(ctx context.Context, _ []string) int {
	if !mem.storeExists() {
		return 0
	}

	payload := mem.payload()
	mem.adopt(ctx, payload)

	name, held := mem.ownSeat(ctx)

	seated := mem.seatAtFirstPrompt(ctx, payload, name != "")
	if seated != "" {
		mem.say(seated)

		return 0
	}

	if name == "" {
		return 0
	}

	held = mem.track(ctx, name, held, payload)
	session := cmp.Or(held.SessionID, payload.SessionID)

	var out []string

	movedHere := mem.moved(ctx, name, held)
	if movedHere != "" {
		out = append(out, movedHere)
	}

	steps := mem.answer(ctx, name, session, payload.TranscriptPath, mem.loadLadder(session))
	if mem.crossed(session, payload.TranscriptPath, steps) {
		steps.Nudges++
		steps.NudgedAt = mem.now()
		out = append(out, nudgeText)
	}

	replay := mem.replayNudge(ctx, name, cmp.Or(payload.TranscriptPath, held.TranscriptPath), &steps)
	if replay != "" {
		out = append(out, replay)
	}

	// A prompt holding a credential never reaches a draft the log would keep.
	withheld := promptvector.Withheld(ctx, payload.Prompt)
	if withheld != "" {
		out = append(out, withheld)
	} else if strings.TrimSpace(payload.Prompt) != "" {
		steps.LastPrompt = strings.TrimSpace(payload.Prompt)
	}

	_ = writeJSON(mem.ladderPath(session), steps)

	recalled := mem.recall(ctx, name, session, payload.Prompt, withheld != "")
	if recalled != "" {
		out = append(out, recalled)
	}

	folded, err := mem.load(ctx)
	if err == nil {
		arrived := mem.announce(ctx, folded, name)
		if arrived != "" {
			out = append(out, arrived)
		}
	}

	mem.say(strings.Join(out, "\n"))

	return 0
}

// tool tells a working session, at its next tool call, about every entry that
// arrived for its seat. A call finding the log as it stood at the last look
// costs two stats and reads nothing more.
func (mem *memory) tool(ctx context.Context, _ []string) int {
	if !mem.storeExists() {
		return 0
	}

	payload := mem.payload()
	mem.adopt(ctx, payload)

	name, held := mem.ownSeat(ctx)
	if name == "" {
		return 0
	}

	var notes []string

	movedHere := mem.moved(ctx, name, held)
	if movedHere != "" {
		notes = append(notes, movedHere)
	}

	session := cmp.Or(payload.SessionID, held.SessionID)

	captured, gained := mem.captureNotice(ctx, name, session)
	if captured != "" {
		notes = append(notes, captured)
	}

	mem.countShipped(ctx, session, payload.ToolName, cmp.Or(payload.ToolInput.FilePath, payload.ToolInput.NotebookPath), gained)

	arrived, holdsTraps := mem.arrivalsAtTool(ctx, name)
	notes = append(notes, arrived...)

	// The call is read only where a trap or a pending do-now item needs it:
	// finding the work tree's root costs a git call.
	var call *toolCall

	callNow := func() toolCall {
		if call == nil {
			read := callOf(mem.checkout(ctx).root, payload)
			call = &read
		}

		return *call
	}

	if holdsTraps {
		notes = append(notes, mem.trapNotices(ctx, name, session, callNow())...)
	}

	mem.proveDoNow(ctx, name, session, callNow)

	if len(notes) > 0 {
		_ = hookio.AddContext(mem.streams.Out, "PostToolUse", strings.Join(notes, "\n"))
	}

	return 0
}

// arrivalsAtTool is what arrived for the seat since its last look, and
// whether the seat holds a trap; where the log stands as it stood then, two
// stats and nothing more. A changed log re-indexes the seat's traps.
func (mem *memory) arrivalsAtTool(ctx context.Context, name string) ([]string, bool) {
	signature := mem.logSignature()

	var mark seenMark
	if readJSON(mem.seenPath(name), &mark) && mark.Log == signature {
		return nil, mark.Traps > 0
	}

	folded, err := mem.load(ctx)
	if err != nil {
		return nil, false
	}

	arrived := mem.announce(ctx, folded, name)
	traps := mem.indexTraps(folded, name)
	mem.noteLog(name, signature, traps)

	if arrived == "" {
		return nil, traps > 0
	}

	return []string{arrived}, traps > 0
}

// logSignature changes whenever the log gains an event: the tail's size and
// time, and the manifest's, which a seal or a merge rewrites.
func (mem *memory) logSignature() string {
	parts := make([]string, 0, 2)

	for _, name := range []string{"tail.jsonl", "manifest.json"} {
		info, err := os.Stat(filepath.Join(mem.logDir(), name))
		if err != nil {
			parts = append(parts, "-")

			continue
		}

		parts = append(parts, fmt.Sprintf("%d@%d", info.Size(), info.ModTime().UnixNano()))
	}

	return strings.Join(parts, " ")
}

// wait is the waiter the Stop hook starts for an idle seated session: it
// looks for arrivals every poll, and on one exits 2 with the lines, which
// Claude Code hands the session as a new turn. It stops waking nothing once
// the seat no longer belongs to this session, its process ends, a later
// turn's waiter replaces it, or the deadline passes.
func (mem *memory) wait(ctx context.Context, _ []string) int {
	if !mem.storeExists() {
		return 0
	}

	payload := mem.payload()

	name, held := mem.ownSeat(ctx)
	if name == "" {
		return 0
	}

	session := cmp.Or(payload.SessionID, held.SessionID)
	key := mem.seatKey(name)
	poll := cmp.Or(mem.env.WaitPoll, waitPoll)
	polls := int(cmp.Or(mem.env.WaitDeadline, waitDeadline) / poll)

	// Each turn end starts a waiter; the newest one names itself here, and every
	// older one sees the name change and ends.
	me := fmt.Sprintf("%d %d", mem.env.Processes.Self(), time.Now().UnixNano())
	waiter := filepath.Join(mem.cacheDir(), "waiters", session)
	_ = os.MkdirAll(filepath.Dir(waiter), 0o700)
	_ = os.WriteFile(waiter, []byte(me), 0o600)

	for range polls + 1 {
		claim, found := seat.Read(mem.seatsDir(), key)
		if !found || claim.SessionID != session || !mem.alive(ctx, claim) {
			return 0
		}

		current, err := os.ReadFile(waiter)
		if err != nil || string(current) != me {
			return 0
		}

		mem.here = nil

		folded, err := mem.load(ctx)
		if err == nil {
			arrived := mem.announce(ctx, folded, name)
			if arrived != "" {
				_, _ = fmt.Fprintln(mem.streams.Err, arrived)

				return 2
			}
		}

		select {
		case <-ctx.Done():
			return 0
		case <-time.After(poll):
		}
	}

	return 0
}

func (mem *memory) stop(ctx context.Context, _ []string) int {
	if !mem.storeExists() {
		return 0
	}

	payload := mem.payload()
	mem.adopt(ctx, payload)

	name, held := mem.ownSeat(ctx)
	if name == "" {
		return 0
	}

	held = mem.track(ctx, name, held, payload)
	session := cmp.Or(held.SessionID, payload.SessionID)

	mem.draftState(ctx, name, held, session)

	pending := mem.holdForDoNow(name, session)
	if pending != "" {
		_, _ = fmt.Fprintln(mem.streams.Err, pending)

		return 2
	}

	steps := mem.answer(ctx, name, session, payload.TranscriptPath, mem.loadLadder(session))
	shipped := mem.shippedHold(ctx, name, &steps)
	_ = writeJSON(mem.ladderPath(session), steps)

	if shipped {
		_, _ = fmt.Fprintln(mem.streams.Err, shippedText)

		return 2
	}

	if steps.Nudges >= nudgesBeforeBackstop && mem.crossed(session, payload.TranscriptPath, steps) {
		_, _ = fmt.Fprintln(mem.streams.Err, backstopText)

		return 2
	}

	return 0
}

func (mem *memory) sessionEnd(ctx context.Context, _ []string) int {
	if !mem.storeExists() {
		return 0
	}

	payload := mem.payload()
	mem.adopt(ctx, payload)

	name, held := mem.ownSeat(ctx)
	if name == "" {
		return 0
	}

	held = mem.track(ctx, name, held, payload)
	mem.draftState(ctx, name, held, cmp.Or(held.SessionID, payload.SessionID))
	removeIfThere(mem.shownPath(held.SessionID))
	promptvector.Remove(promptvector.Dir(mem.env.CacheRoot), held.SessionID)

	text := mem.recentTurns(ctx, name, cmp.Or(payload.TranscriptPath, held.TranscriptPath))
	if text == "" {
		removeIfThere(mem.slicePath(name))
		mem.closeAtEnd(ctx, name, payload)

		return 0
	}

	content := "session " + held.SessionID + "\n" + transcript.Tail(text, claimBudget)
	_ = os.WriteFile(mem.slicePath(name), []byte(content), 0o600)

	mem.closeAtEnd(ctx, name, payload)

	return 0
}

// closeAtEnd closes the seat of a session that ends: its claim moves to the
// seat's last claim with the time it closed, which starts a temporary shelf's
// clock once no seat on it is in use. A `/clear` or `/resume` ends a session
// id but not the process, which keeps its seat.
func (mem *memory) closeAtEnd(ctx context.Context, seatName string, payload hookio.Payload) {
	if payload.Reason == "clear" || payload.Reason == "resume" {
		return
	}

	mem.skipAtEnd(seatName)

	owner := seat.Owner(ctx, mem.env.Processes, mem.env.OwnerName)
	_, _, _ = mem.closeSeat(ctx, seatName, mem.seatKey(seatName), owner)
	_ = mem.purgeExpired(ctx)
}

// shownSet is what a session was shown since its last compaction: recalled
// entries, and the traps a tool call told it of.
type shownSet struct {
	IDs      []string `json:"ids"`
	Notified bool     `json:"notified"`
	Traps    []string `json:"traps,omitempty"`
	Notes    []string `json:"notes,omitempty"`
}

func (mem *memory) shownPath(session string) string {
	return filepath.Join(mem.cacheDir(), "shown", session+".json")
}

// recall is up to three entries the prompt touches that stand out from the
// rest: the own seat's two standard deviations above the mean, another seat's
// three, none already shown since the last compaction.
// A prompt withheld from the embedding endpoint is scored by its words.
func (mem *memory) recall(ctx context.Context, seatName string, session string, prompt string, withheld bool) string {
	if strings.TrimSpace(prompt) == "" {
		return ""
	}

	folded, err := mem.load(ctx)
	if err != nil {
		return ""
	}

	var live []*entry

	for _, held := range folded.entryList() {
		if held.status == statusLive {
			live = append(live, held)
		}
	}

	if len(live) < recallMinimum {
		return ""
	}

	var shown shownSet
	readJSON(mem.shownPath(session), &shown)

	bounded, cancel := context.WithTimeout(ctx, recallLimit)
	defer cancel()

	scores, err := mem.recallScores(bounded, folded, live, prompt, session, withheld)
	if err != nil {
		if shown.Notified {
			return ""
		}

		shown.Notified = true
		_ = writeJSON(mem.shownPath(session), shown)

		return fmt.Sprintf("%s: prompt recall is off this session: %v", self, err)
	}

	values := make([]float64, 0, len(live))
	for _, held := range live {
		values = append(values, float64(scores[held.id]))
	}

	centre, spread := mean(values), deviation(values)

	var hits []*entry

	for _, held := range live {
		if slices.Contains(shown.IDs, held.id) {
			continue
		}

		floor := recallFloorOther
		if mem.ownOrReceived(folded, held, seatName) {
			floor = recallFloorOwn
		}

		if float64(scores[held.id]) >= centre+floor*spread && spread > 0 {
			hits = append(hits, held)
		}
	}

	if len(hits) == 0 {
		return ""
	}

	slices.SortStableFunc(hits, func(a, b *entry) int { return cmp.Compare(scores[b.id], scores[a.id]) })
	hits = hits[:min(recallHits, len(hits))]

	here := mem.checkout(ctx)
	lines := []string{self + ": related"}

	var ids []string

	listed := map[string]bool{}
	for _, held := range hits {
		listed[held.id] = true
	}

	for _, held := range hits {
		lines = append(lines, "  "+shortLine(here, held, seatName, mem.now()))
		lines = append(lines, citingLines(held, listed, "    ")...)
		ids = append(ids, held.id)
	}

	shown.IDs = append(shown.IDs, ids...)
	_ = writeJSON(mem.shownPath(session), shown)

	return strings.Join(lines, "\n")
}

// inherit seats a process holding no seat on the seat its session held before
// a resume or a fork: the seat itself where its holder is gone, a new seat on
// its shelf where the holder still runs, since a running holder keeps its
// seat. A resume keeps its session id; a fork starts its transcript with the
// claim's fork uuid. It answers what the claim printed, or nothing where no
// claim matches.
func (mem *memory) inherit(ctx context.Context, payload hookio.Payload) string {
	first := ""
	if payload.TranscriptPath != "" {
		first, _ = transcript.FirstUUID(ctx, payload.TranscriptPath)
	}

	// A session id names the session itself, where a fork uuid is shared by
	// every fork of one parent, so a resumed fork finds its own seat first.
	match := func(held seat.Claim) int {
		switch {
		case payload.SessionID != "" && held.SessionID == payload.SessionID:
			return 2
		case first != "" && held.ForkUUID == first:
			return 1
		default:
			return 0
		}
	}

	dir := mem.seatsDir()

	var (
		gone, running string
		goneMatch     int
		goneAt        string
	)

	for _, key := range seat.Known(dir) {
		current, found := seat.Read(dir, key)
		if found && match(current) > 0 && mem.alive(ctx, current) {
			if running == "" {
				running = key
			}

			continue
		}

		last, _ := seat.ReadLast(dir, key)

		for _, held := range []seat.Claim{current, last} {
			matched := match(held)
			if matched > goneMatch || (matched > 0 && matched == goneMatch && held.Claimed > goneAt) {
				gone, goneMatch, goneAt = key, matched, held.Claimed
			}
		}
	}

	if payload.SessionID != "" && mem.env.SessionID == "" {
		mem.env.SessionID = payload.SessionID
	}

	req := request{}

	switch {
	case gone != "":
		req.Name = mem.seatName(gone)
	case running != "":
		req.Shelf = mem.shelfName(mem.shelfOfKey(running))
	default:
		return ""
	}

	claimed := mem.claim(ctx, req)
	if claimed.failed {
		return ""
	}

	name, held := mem.ownSeat(ctx)
	if name != "" {
		mem.track(ctx, name, held, payload)
	}

	return claimed.text
}
