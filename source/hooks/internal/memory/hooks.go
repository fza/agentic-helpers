package memory

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
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

const nudgeText = "Context near compaction. File what the summary would lose: findings, open questions, " +
	"judgment calls, where work stands. " + self + " new --title '<3-10 words>' --topic <name>... < body"

const backstopText = "Memory overdue, two nudges unanswered: file what the summary would lose now, " +
	"with " + self + " new, before ending the turn."

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
func (mem *memory) track(seatName string, held seat.Claim, payload hookio.Payload) seat.Claim {
	changed := false

	if payload.SessionID != "" && payload.SessionID != held.SessionID {
		held.SessionID = payload.SessionID
		changed = true
	}

	if payload.TranscriptPath != "" && payload.TranscriptPath != held.TranscriptPath {
		held.TranscriptPath = payload.TranscriptPath
		changed = true
	}

	if changed {
		_ = seat.Write(mem.seatsDir(), mem.seatKey(seatName), held)
	}

	return held
}

func (mem *memory) sessionStart(ctx context.Context, _ []string) int {
	if !mem.storeExists() {
		return 0
	}

	payload := mem.payload()
	mem.housekeep()

	name, held := mem.ownSeat(ctx)
	if name != "" {
		held = mem.track(name, held, payload)

		if payload.Source == "compact" || payload.Source == "clear" {
			mem.resetWindow(held.SessionID)

			folded, err := mem.load(ctx)
			if err == nil {
				mem.say(mem.brief(ctx, mem.checkout(ctx), folded, name))
			}
		}

		return 0
	}

	mem.sweep(ctx)

	folded, err := mem.load(ctx)
	if err != nil {
		return 0
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

	mem.say(fmt.Sprintf("%s: this session holds no seat. Seats: %s. Claim one: %s claim <seat>", self, seats, self))

	return 0
}

// housekeep removes per-session cache files nobody came back to.
func (mem *memory) housekeep() {
	cutoff := mem.now().Add(-staleAfter)

	for _, pattern := range []string{
		filepath.Join(mem.sensorDir(), "*.ctx"),
		filepath.Join(mem.cacheDir(), "shown", "*.json"),
		filepath.Join(mem.cacheDir(), "ladder", "*.json"),
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

	pieces := transcript.Pieces(mem.recentTurns(ctx, name, payload.TranscriptPath), pieceChars, maxPieces)
	if index >= 1 && index <= len(pieces) {
		if index == 1 {
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

	name, held := mem.ownSeat(ctx)
	if name == "" {
		return 0
	}

	held = mem.track(name, held, payload)
	session := cmp.Or(held.SessionID, payload.SessionID)

	var out []string

	steps := mem.answer(ctx, name, session, payload.TranscriptPath, mem.loadLadder(session))
	if mem.crossed(session, payload.TranscriptPath, steps) {
		steps.Nudges++
		steps.NudgedAt = mem.now()
		out = append(out, nudgeText)
	}

	_ = writeJSON(mem.ladderPath(session), steps)

	recalled := mem.recall(ctx, name, session, payload.Prompt)
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

	_ = mem.payload()

	name, _ := mem.ownSeat(ctx)
	if name == "" {
		return 0
	}

	signature := mem.logSignature()

	var mark seenMark
	if readJSON(mem.seenPath(name), &mark) && mark.Log == signature {
		return 0
	}

	folded, err := mem.load(ctx)
	if err != nil {
		return 0
	}

	arrived := mem.announce(ctx, folded, name)
	mem.noteLog(name, signature)

	if arrived != "" {
		_ = hookio.AddContext(mem.streams.Out, "PostToolUse", arrived)
	}

	return 0
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

	name, held := mem.ownSeat(ctx)
	if name == "" {
		return 0
	}

	held = mem.track(name, held, payload)
	session := cmp.Or(held.SessionID, payload.SessionID)

	steps := mem.answer(ctx, name, session, payload.TranscriptPath, mem.loadLadder(session))
	_ = writeJSON(mem.ladderPath(session), steps)

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

	name, held := mem.ownSeat(ctx)
	if name == "" {
		return 0
	}

	held = mem.track(name, held, payload)
	removeIfThere(mem.shownPath(held.SessionID))

	text := mem.recentTurns(ctx, name, cmp.Or(payload.TranscriptPath, held.TranscriptPath))
	if text == "" {
		removeIfThere(mem.slicePath(name))

		return 0
	}

	content := "session " + held.SessionID + "\n" + transcript.Tail(text, claimBudget)
	_ = os.WriteFile(mem.slicePath(name), []byte(content), 0o600)

	return 0
}

type shownSet struct {
	IDs      []string `json:"ids"`
	Notified bool     `json:"notified"`
}

func (mem *memory) shownPath(session string) string {
	return filepath.Join(mem.cacheDir(), "shown", session+".json")
}

// recall is up to three entries the prompt touches that stand out from the
// rest: the own seat's two standard deviations above the mean, another seat's
// three, none already shown since the last compaction.
func (mem *memory) recall(ctx context.Context, seatName string, session string, prompt string) string {
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

	scores, err := mem.semantic(bounded, folded, prompt)
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

	for _, held := range hits {
		lines = append(lines, "  "+line(here, held))
		ids = append(ids, held.id)
	}

	shown.IDs = append(shown.IDs, ids...)
	_ = writeJSON(mem.shownPath(session), shown)
	mem.warm(folded, heatRecall, ids...)

	return strings.Join(lines, "\n")
}
