package memory

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
	"github.com/fza/agentic-helpers/source/hooks/internal/secrets"
	"github.com/fza/agentic-helpers/source/hooks/internal/slots"
)

// The landed-commit scan reads the checkout's history for commits that may
// settle a live todo, todo item, trap or open state line, and keeps each match in the store's
// slot file until the entry or item ends or the agent dismisses the match:
// a hint delivered just before a compaction is as much owed after it. Nothing is
// retired by a match: a similar subject is evidence, not proof. A round runs
// in a process of its own, `agentic-memory landed-round`, so no hook waits on
// git: a hook only starts a due round and reads what earlier rounds stored.
const (
	landedKey     = "landed"
	branchTipsKey = "branch-tips"
	scanMarkKey   = "landed-scan/"
	roundEvent    = "landed-round"
	// landedBatch bounds the commits one round processes, oldest first; the
	// rest wait for the next round.
	landedBatch = 200
	// roundStale is the age past which a round's lock counts as left by a
	// round that died.
	roundStale = 10 * time.Minute
	// landedSimilarity is the cosine at or above which a commit subject counts
	// as naming an entry's or item's work.
	landedSimilarity = 0.75
	landedTextRunes  = 2000
	landedSubject    = 72
)

const (
	landedByID      = "id"
	landedByBranch  = "branch"
	landedByPath    = "path"
	landedBySubject = "subject"
)

// todoEndHint answers a todo filed with nothing tying it to its work.
func todoEndHint(at channel) string {
	return "no end declared: " + hintArgs(at, hintFlag("until", []string{"merged:<branch>"})) +
		" ends it at its merge; or split the work into items, such as " +
		hintArgs(at, hintFlag("item", []string{"build", "capture"}))
}

// landingCall is a Bash command that may land commits in the checkout.
var landingCall = regexp.MustCompile(`\bgit\b[^;&|\n]*?\s(?:merge|commit|pull|rebase|cherry-pick)\b`)

// mergeSubjects are the subjects git, GitHub and GitLab give a merge, each
// naming the merged branch.
var mergeSubjects = []*regexp.Regexp{
	regexp.MustCompile(`^Merge branch '([^']+)'`),
	regexp.MustCompile(`^Merge remote-tracking branch '(?:[^/']+/)?([^']+)'`),
	regexp.MustCompile(`^Merge pull request #\d+ from [^/\s]+/(\S+)`),
}

// defaultMerge is git's own merge subject, whose second parent is the merged
// branch's tip at the merge.
var defaultMerge = regexp.MustCompile(`^Merge branch '([^']+)'(?: into \S+)?$`)

// trunkBranches are never taken for the branch of an entry's work: a merge
// of the trunk into a feature branch names it.
var trunkBranches = []string{"main", "master", "trunk", "develop", "HEAD"}

// commitFormat opens each commit with a record separator and parts its
// fields with a unit separator; `--name-only` lists its paths after it.
const commitFormat = "--format=%x1e%H%x1f%P%x1f%cI%x1f%B%x1f"

type landedMapping struct {
	Target  string `json:"target"`
	Commit  string `json:"commit"`
	Subject string `json:"subject"`
	Test    string `json:"test"`
	// Merge marks a mapping to a merge commit; Score ranks it against a later
	// round's commit for the same entry or item.
	Merge bool    `json:"merge,omitempty"`
	Score float64 `json:"score"`
}

type landedSet struct {
	Mappings []landedMapping `json:"mappings"`
}

// scanMark is where a checkout's scan stands: the commit its last finished
// round examined last, that commit's committer date, and when the round
// finished.
type scanMark struct {
	At   time.Time `json:"at"`
	Head string    `json:"head"`
	Date time.Time `json:"date"`
}

type landedCommit struct {
	sha     string
	parents []string
	at      time.Time
	message string
	subject string
	paths   []string
	merged  string
}

// landedTarget is what one live todo, open item or trap is matched by; floor
// is its filing time, an item's the time it was added, before which no
// commit matches it.
type landedTarget struct {
	address  string
	held     *entry
	floor    time.Time
	text     string
	links    []string
	branches []string
	paths    []string
}

// scanKey names a checkout's scan mark by the directory its sessions work
// in, which a hook knows without asking git.
func (mem *memory) scanKey(base string) string {
	sum := sha256.Sum256([]byte(base))

	return scanMarkKey + hex.EncodeToString(sum[:])[:16]
}

func (mem *memory) roundLock(key string) string {
	return filepath.Join(mem.cacheDir(), strings.ReplaceAll(key, "/", "-")+".lock")
}

// startLandedRound starts a round in a process of its own where one is due:
// landed_scan_interval passed since the last finished round, or forced by a
// git call that may have landed commits. The lock it takes first lets one
// round run per checkout; the round releases it.
func (mem *memory) startLandedRound(ctx context.Context, forced bool) {
	if mem.outsideGit || mem.unreadable != "" || mem.env.Starter == nil || mem.env.Executable == "" ||
		!mem.storeExists() {
		return
	}

	key := mem.scanKey(mem.env.Base)

	var mark scanMark
	mem.readSlot(key, &mark)

	if !forced && !mark.At.IsZero() && mem.now().Sub(mark.At) < mem.options().LandedScanInterval {
		return
	}

	token, taken := mem.takeRoundLock(key)
	if !taken {
		return
	}

	err := mem.env.Starter.Start(ctx, mem.env.Executable, roundEvent, token)
	if err != nil {
		mem.releaseRoundLock(key, token)
	}
}

// takeRoundLock creates the round's lock file holding a fresh token, where no
// live round holds it; a lock older than roundStale was left by a round that
// died, and is taken over.
func (mem *memory) takeRoundLock(key string) (string, bool) {
	path := mem.roundLock(key)

	err := os.MkdirAll(filepath.Dir(path), 0o700)
	if err != nil {
		return "", false
	}

	drawn := make([]byte, 8)

	_, err = mem.env.Random.Read(drawn)
	if err != nil {
		return "", false
	}

	token := hex.EncodeToString(drawn)

	for range 2 {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_, err = file.WriteString(token)
			closeErr := file.Close()

			if err != nil || closeErr != nil {
				_ = os.Remove(path)

				return "", false
			}

			return token, true
		}

		info, statErr := os.Stat(path)
		if !errors.Is(err, os.ErrExist) || statErr != nil || time.Since(info.ModTime()) < roundStale {
			return "", false
		}

		_ = os.Remove(path)
	}

	return "", false
}

func (mem *memory) holdsRoundLock(key string, token string) bool {
	held, err := os.ReadFile(mem.roundLock(key))

	return err == nil && token != "" && string(held) == token
}

func (mem *memory) releaseRoundLock(key string, token string) {
	if mem.holdsRoundLock(key, token) {
		_ = os.Remove(mem.roundLock(key))
	}
}

// landedRound is the round a hook started: it runs only while the lock holds
// the token it was handed, and releases the lock when done.
func (mem *memory) landedRound(ctx context.Context, args []string) int {
	if len(args) != 1 || !mem.storeExists() {
		return 0
	}

	key := mem.scanKey(mem.env.Base)
	if !mem.holdsRoundLock(key, args[0]) {
		return 0
	}

	defer mem.releaseRoundLock(key, args[0])

	mem.runRound(ctx, key)

	return 0
}

// runRound reads the commits reachable from HEAD and not from the mark, in
// one git log stream, oldest first, and stores a mapping for every commit
// matching a live todo, open item or trap. A mark HEAD no longer descends
// from gives way to the commits dated after it; no mark, to those dated at
// or after the oldest floor. A round past the batch moves the mark to the
// last commit it processed.
func (mem *memory) runRound(ctx context.Context, key string) {
	here := mem.checkout(ctx)
	if !here.workTree {
		return
	}

	answer, err := mem.env.Git.Output(ctx, here.root, "log", "-1", "--format=%H%x1f%cI", "HEAD")
	if err != nil {
		return
	}

	headSHA, headDate, _ := strings.Cut(strings.TrimSpace(answer), "\x1f")
	headAt, _ := time.Parse(time.RFC3339, headDate)
	head := scanMark{Head: headSHA, Date: headAt}

	if headSHA == "" {
		return
	}

	var mark scanMark
	mem.readSlot(key, &mark)

	finish := func(reached scanMark) {
		reached.At = mem.now()
		mem.writeSlot(key, fixedMark(reached))
	}

	folded, err := mem.load(ctx)
	if err != nil {
		return
	}

	probe := mem.probe(ctx)
	watched := probe.watchBranches(ctx, folded)
	targets := append(landedTargets(folded), ledgerTargets(folded)...)
	floor := oldestFloor(folded, targets)

	if mark.Head == headSHA || (mark.Head == "" && floor.IsZero()) {
		finish(head)

		return
	}

	// Topological order puts every commit after its parents, so a merge in
	// the batch finds the commits it brings in before it.
	read := []string{"log", "--topo-order", "--reverse", commitFormat, "--name-only", "--diff-merges=first-parent"}
	byDate := true

	switch {
	case mark.Head == "":
		read = append(read, "--since="+floor.UTC().Format(time.RFC3339), "HEAD")
	case mem.descends(ctx, here.root, mark.Head):
		read = append(read, mark.Head+"..HEAD")
		byDate = false
	default:
		read = append(read, "--since="+mark.Date.UTC().Format(time.RFC3339), "HEAD")
	}

	answer, err = mem.env.Git.Output(ctx, here.root, read...)
	if err != nil {
		return
	}

	commits := parseCommits(answer)
	reached := head

	if len(commits) > landedBatch {
		commits = commits[:landedBatch]
		last := commits[len(commits)-1]
		reached = scanMark{Head: last.sha, Date: last.at}
	}

	probe.learnTips(commits, watched)

	stored := mem.storeMappings(folded, mem.matchLandings(ctx, targets, mem.landings(ctx, here.root, commits, byDate)), commits)
	finish(reachedAfter(stored, commits, reached, mark))
}

// reachedAfter is where a round's scan stands once it kept the mappings of
// its commits up to the one at stored: where it reached with all of them,
// at that commit with some, where it started with none. A round meeting the
// cap of stored mappings leaves the rest of its commits to the next round.
func reachedAfter(stored int, commits []landedCommit, reached scanMark, mark scanMark) scanMark {
	switch {
	case stored >= len(commits)-1:
		return reached
	case stored < 0:
		return mark
	}

	return scanMark{Head: commits[stored].sha, Date: commits[stored].at}
}

func (mem *memory) descends(ctx context.Context, root string, mark string) bool {
	_, err := mem.env.Git.Output(ctx, root, "merge-base", "--is-ancestor", mark, "HEAD")

	return err == nil
}

// storeMappings keeps the mappings a round found, commit by commit in the
// order the round read them, and answers the index of the last commit whose
// mappings it kept: a commit whose mappings the set has no room for, by
// count or by its ceiling, ends the round there, -1 where the first does. A
// mapping to a commit the round did not read counts as the last one's.
func (mem *memory) storeMappings(folded *state, found []landedMapping, commits []landedCommit) int {
	if len(found) == 0 {
		return len(commits) - 1
	}

	var held landedSet
	mem.readSlot(landedKey, &held)

	held.Mappings = pendingMappings(folded, held.Mappings)

	through := func(last int) landedSet {
		next := landedSet{Mappings: slices.Clone(held.Mappings)}

		for _, mapping := range found {
			at := slices.IndexFunc(commits, func(commit landedCommit) bool { return commit.sha == mapping.Commit })
			if at < 0 {
				at = len(commits) - 1
			}

			if at <= last {
				next.add(mapping)
			}
		}

		return next
	}

	for last := len(commits) - 1; last >= 0; last-- {
		next := through(last)
		if next.fits() {
			mem.writeSlot(landedKey, &next)

			return last
		}
	}

	return -1
}

// add keeps mapping where it may stand: one mapping per entry or item, a
// later round's commit replacing a stored one only by scoring higher. A
// mapping past its caps stands nowhere.
func (held *landedSet) add(mapping landedMapping) {
	mapping, keep := limitMapping(mapping)
	if !keep {
		return
	}

	index := slices.IndexFunc(held.Mappings, func(kept landedMapping) bool { return kept.Target == mapping.Target })

	switch {
	case index < 0:
		held.Mappings = append(held.Mappings, mapping)
	case mapping.Score > held.Mappings[index].Score:
		held.Mappings[index] = mapping
	}
}

// fits is whether the set stays within its caps, by count and by ceiling.
func (held *landedSet) fits() bool {
	return len(held.Mappings) <= landedMappings && slots.EncodedLen(held) <= landedCeiling
}

// oldestFloor is the oldest floor of every target, and the filing time of
// every live entry carrying a `merged:` end, whose branch tip a merge in the
// range may give: no commit before it can settle any of them.
func oldestFloor(folded *state, targets []landedTarget) time.Time {
	var oldest time.Time

	earlier := func(at time.Time) {
		if !at.IsZero() && (oldest.IsZero() || at.Before(oldest)) {
			oldest = at
		}
	}

	for _, target := range targets {
		earlier(target.floor)
	}

	for _, held := range folded.entryList() {
		if held.status == statusLive &&
			slices.ContainsFunc(conditionsOf(held), func(end condition) bool { return end.form == "merged" }) {
			earlier(held.filed)
		}
	}

	return oldest
}

func parseCommits(answer string) []landedCommit {
	var commits []landedCommit

	for record := range strings.SplitSeq(answer, "\x1e") {
		fields := strings.Split(record, "\x1f")
		if len(fields) < 5 || strings.TrimSpace(fields[0]) == "" {
			continue
		}

		message := strings.TrimSpace(fields[3])
		subject, _, _ := strings.Cut(message, "\n")
		at, _ := time.Parse(time.RFC3339, strings.TrimSpace(fields[2]))

		commit := landedCommit{
			sha: strings.TrimSpace(fields[0]), parents: strings.Fields(fields[1]), at: at, message: message,
			subject: strings.TrimSpace(subject),
		}

		for line := range strings.SplitSeq(fields[4], "\n") {
			if path := strings.TrimSpace(line); path != "" {
				commit.paths = append(commit.paths, path)
			}
		}

		for _, pattern := range mergeSubjects {
			named := pattern.FindStringSubmatch(commit.subject)
			if named != nil {
				commit.merged = named[1]

				break
			}
		}

		commits = append(commits, commit)
	}

	return commits
}

// landedTargets is every live todo and trap of the store, and every open item
// of a live todo, each with what a commit is matched by.
func landedTargets(folded *state) []landedTarget {
	var targets []landedTarget

	for _, held := range folded.entryList() {
		if held.status != statusLive || (held.kind != kindTodo && held.kind != kindTrap) {
			continue
		}

		target := landedTarget{address: held.id, held: held, floor: held.filed, text: held.title + "\n" + held.body}

		for _, link := range held.links() {
			if graphLink(link) {
				target.links = append(target.links, link)
			}
		}

		for _, end := range conditionsOf(held) {
			if end.form == "merged" {
				branch, _, _ := strings.Cut(end.value, "@")
				target.branches = append(target.branches, branch)
			}
		}

		for _, anchored := range held.anchors {
			target.paths = append(target.paths, anchored.Path)
		}

		target.paths = append(target.paths, held.about...)
		targets = append(targets, target)

		for _, item := range held.items {
			if item.State != itemOpen {
				continue
			}

			itemTarget := landedTarget{address: held.id + "#" + item.ID, held: held, floor: cmp.Or(item.added, held.filed), text: item.Text}
			if graphLink(item.Link) {
				itemTarget.links = []string{item.Link}
			}

			targets = append(targets, itemTarget)
		}
	}

	return targets
}

func graphLink(link string) bool {
	_, _, qualified := splitQualified(link)

	return qualified || isGraphID(link)
}

func bareGraphID(link string) string {
	_, bare, qualified := splitQualified(link)
	if qualified {
		return bare
	}

	return link
}

// shortGraphID is a graph id without its time stamp: `d-tac-pu1`.
func shortGraphID(id string) string {
	if len(id) < 16 {
		return id
	}

	return id[16:]
}

// landing is where work reached HEAD: a normal commit judged by itself, or a
// merge judged together with every commit it brings in, its own message
// first. Merge messages often say nothing beyond `Merge branch '<branch>'`,
// so the work a merge lands is described by the commits it merged.
type landing struct {
	commit   landedCommit
	evidence []landedCommit
	merge    bool
}

// landings groups a round's commits, oldest first, into landings: each merge
// with the commits reachable from its later parents and not from its first,
// found by parent links in the stream; a commit a merge brought in is judged
// through that merge alone. Where a merged commit lies outside the stream,
// which a round reading by date can leave, the merge's side is read once with
// `git log <first parent>..<merge>`.
func (mem *memory) landings(ctx context.Context, root string, commits []landedCommit, byDate bool) []landing {
	bySHA := make(map[string]landedCommit, len(commits))
	for _, commit := range commits {
		bySHA[commit.sha] = commit
	}

	judged := map[string]bool{}

	var found []landing

	for _, commit := range commits {
		if len(commit.parents) < 2 {
			continue
		}

		side, complete := sideOf(bySHA, commit)
		if !complete && byDate {
			side = mem.readSide(ctx, root, commit, side)
		}

		evidence := []landedCommit{commit}

		for _, merged := range side {
			if !judged[merged.sha] {
				judged[merged.sha] = true
				evidence = append(evidence, merged)
			}
		}

		judged[commit.sha] = true
		found = append(found, landing{commit: commit, evidence: evidence, merge: true})
	}

	for _, commit := range commits {
		if !judged[commit.sha] {
			found = append(found, landing{commit: commit, evidence: []landedCommit{commit}})
		}
	}

	return found
}

// sideOf is the commits of the stream reachable from merge's later parents
// and not from its first, and whether every one of them lay in the stream.
func sideOf(bySHA map[string]landedCommit, merge landedCommit) ([]landedCommit, bool) {
	first := map[string]bool{}
	queue := []string{merge.parents[0]}

	for len(queue) > 0 {
		sha := queue[0]
		queue = queue[1:]

		commit, inStream := bySHA[sha]
		if first[sha] || !inStream {
			first[sha] = true

			continue
		}

		first[sha] = true
		queue = append(queue, commit.parents...)
	}

	var side []landedCommit

	complete := true
	seen := map[string]bool{}
	queue = slices.Clone(merge.parents[1:])

	for len(queue) > 0 {
		sha := queue[0]
		queue = queue[1:]

		if seen[sha] || first[sha] {
			continue
		}

		seen[sha] = true

		commit, inStream := bySHA[sha]
		if !inStream {
			complete = false

			continue
		}

		side = append(side, commit)
		queue = append(queue, commit.parents...)
	}

	return side, complete
}

// readSide reads a merge's side whole, keeping what the stream gave where
// git fails.
func (mem *memory) readSide(ctx context.Context, root string, merge landedCommit, fallback []landedCommit) []landedCommit {
	answer, err := mem.env.Git.Output(ctx, root, "log", "--topo-order", "--reverse", commitFormat, "--name-only",
		"--diff-merges=first-parent", merge.parents[0]+".."+merge.sha)
	if err != nil {
		return fallback
	}

	return slices.DeleteFunc(parseCommits(answer), func(commit landedCommit) bool { return commit.sha == merge.sha })
}

// A match's strength runs from 0 to 1: a named id or branch is exact, a
// changed path alone weaker evidence, a similar subject its cosine stretched
// above the threshold. A landing's score adds a merge's share: a direct commit
// is reported over a merge only when its strength exceeds the merge's by
// more than mergeWeight / strengthWeight (0.25), since merges land most work.
const (
	strengthNamed  = 1.0
	strengthPath   = 0.5
	strengthWeight = 0.8
	mergeWeight    = 0.2
)

// landedScore ranks a landing that passed a test for a target.
func landedScore(strength float64, merge bool) float64 {
	if merge {
		return strengthWeight*strength + mergeWeight
	}

	return strengthWeight * strength
}

// similarityStrength stretches a cosine at or above the threshold to 0..1:
// raw scores cluster in a narrow band where the merge share would decide
// alone.
func similarityStrength(cosine float32) float64 {
	return (float64(cosine) - landedSimilarity) / (1 - landedSimilarity)
}

// evidenceMatch is the strongest test one landing passed for one target,
// and the commit among its evidence that passed it.
type evidenceMatch struct {
	strength float64
	test     string
	evidence int
}

// matchLandings judges every landing against every target, the floor read at
// the landing's committer date, before anything is stored. A landing passes
// for a target where any of its commits passes a test, at the strength of
// the strongest; of the landings passing for a target, the highest score is
// reported, the newer landing winning a tie.
func (mem *memory) matchLandings(ctx context.Context, targets []landedTarget, landings []landing) []landedMapping {
	shortOwners := map[string][]string{}

	for _, target := range targets {
		for _, link := range target.links {
			bare := bareGraphID(link)
			short := shortGraphID(bare)

			if !slices.Contains(shortOwners[short], bare) {
				shortOwners[short] = append(shortOwners[short], bare)
			}
		}
	}

	matched := map[[2]int]evidenceMatch{}

	var judged [][2]int

	for landingIndex, landed := range landings {
		for targetIndex, target := range targets {
			if landed.commit.at.IsZero() || landed.commit.at.Before(target.floor) {
				continue
			}

			pair := [2]int{landingIndex, targetIndex}
			judged = append(judged, pair)

			for index, evidence := range landed.evidence {
				strength, test := commitStrength(evidence, target, shortOwners)

				best, hit := matched[pair]
				if test != "" && (!hit || strength > best.strength) {
					matched[pair] = evidenceMatch{strength: strength, test: test, evidence: index}
				}
			}
		}
	}

	mem.similarLandings(ctx, targets, landings, judged, matched)

	var found []landedMapping

	for targetIndex, target := range targets {
		chosen := -1

		var chosenScore float64

		for landingIndex, landed := range landings {
			match, hit := matched[[2]int{landingIndex, targetIndex}]
			if !hit {
				continue
			}

			score := landedScore(match.strength, landed.merge)
			if chosen < 0 || score > chosenScore || (score == chosenScore && !landed.commit.at.Before(landings[chosen].commit.at)) {
				chosen, chosenScore = landingIndex, score
			}
		}

		if chosen >= 0 {
			match := matched[[2]int{chosen, targetIndex}]
			landed := landings[chosen]
			found = append(found, mappingOf(target, landed, landed.evidence[match.evidence], match.test, chosenScore))
		}
	}

	return found
}

func mappingOf(target landedTarget, landed landing, evidence landedCommit, test string, score float64) landedMapping {
	subject := []rune(evidence.subject)
	if len(subject) > landedSubject {
		subject = append(subject[:landedSubject-1], '…')
	}

	return landedMapping{
		Target: target.address, Commit: landed.commit.sha, Subject: string(subject), Test: test, Merge: landed.merge,
		Score: score,
	}
}

// commitStrength is the strongest test commit passes for target, other than
// subject similarity: a linked graph id the message names, or the target's
// branch, at full strength; a changed path it is anchored to or about, at
// strengthPath. Empty test where none passes.
func commitStrength(commit landedCommit, target landedTarget, shortOwners map[string][]string) (float64, string) {
	for _, link := range target.links {
		bare := bareGraphID(link)
		short := shortGraphID(bare)

		if strings.Contains(commit.message, bare) ||
			(len(shortOwners[short]) == 1 && namesToken(commit.message, short, idChars)) {
			return strengthNamed, landedByID
		}
	}

	for _, branch := range target.branches {
		if commit.merged == branch || namesToken(commit.subject, branch, branchChars) {
			return strengthNamed, landedByBranch
		}
	}

	if commit.merged != "" && !slices.Contains(trunkBranches, commit.merged) &&
		namesToken(target.text, commit.merged, branchChars) {
		return strengthNamed, landedByBranch
	}

	for _, changed := range commit.paths {
		for _, path := range target.paths {
			if changed == path || strings.HasPrefix(changed, strings.TrimSuffix(path, "/")+"/") {
				return strengthPath, landedByPath
			}
		}
	}

	return 0, ""
}

// similarLandings raises each judged pair to the strength of its landing's
// subject closest to the target's text, where the store's embedder places it
// at or above the threshold and above what another test gave. One embedder
// call covers the round, holding only the subjects of landings at or after
// some target's floor; an outage or no embedder skips this test alone. A
// subject holding a credential never reaches the endpoint.
func (mem *memory) similarLandings(ctx context.Context, targets []landedTarget, landings []landing, judged [][2]int,
	matched map[[2]int]evidenceMatch,
) {
	if len(judged) == 0 {
		return
	}

	embedder, err := mem.embedder(ctx)
	if err != nil {
		return
	}

	subjectRow, targetRow := map[string]int{}, map[int]int{}

	var texts []string

	for _, pair := range judged {
		for _, evidence := range landings[pair[0]].evidence {
			_, known := subjectRow[evidence.sha]
			if known {
				continue
			}

			if evidence.subject == "" || len(secrets.Scan(ctx, evidence.subject, "")) > 0 {
				subjectRow[evidence.sha] = -1

				continue
			}

			subjectRow[evidence.sha] = len(texts)
			texts = append(texts, evidence.subject)
		}

		_, known := targetRow[pair[1]]
		if !known {
			targetRow[pair[1]] = len(texts)
			texts = append(texts, embed.Cut(targets[pair[1]].text, landedTextRunes))
		}
	}

	bounded, cancel := context.WithTimeout(ctx, mem.embedLimitFor())
	defer cancel()

	vectors, err := embedder.Documents(bounded, texts)
	if err != nil || len(vectors) != len(texts) {
		return
	}

	for _, pair := range judged {
		for index, evidence := range landings[pair[0]].evidence {
			row := subjectRow[evidence.sha]
			if row < 0 {
				continue
			}

			cosine := embed.Cosine(vectors[row], vectors[targetRow[pair[1]]])
			if cosine < landedSimilarity {
				continue
			}

			strength := similarityStrength(cosine)

			best, hit := matched[pair]
			if !hit || strength > best.strength {
				matched[pair] = evidenceMatch{strength: strength, test: landedBySubject, evidence: index}
			}
		}
	}
}

// idChars and branchChars are what a graph id and a branch name are spelled
// with, inside a regexp character class.
const (
	idChars     = `0-9A-Za-z-`
	branchChars = `\w./-`
)

// namesToken is whether text holds word as a whole token: neither side
// touches a character of class, a sentence's closing dot aside.
func namesToken(text string, word string, class string) bool {
	if word == "" {
		return false
	}

	pattern, err := regexp.Compile(`(?:^|[^` + class + `])` + regexp.QuoteMeta(word) +
		`(?:$|\.(?:\s|$)|[^` + strings.ReplaceAll(class, ".", "") + `])`)
	if err != nil {
		return false
	}

	return pattern.MatchString(text)
}

// pendingMappings is the mappings whose entry is still a live todo or trap
// and whose item, where one is named, still open, and those whose state
// line still stands open.
func pendingMappings(folded *state, mappings []landedMapping) []landedMapping {
	return slices.DeleteFunc(slices.Clone(mappings), func(mapping landedMapping) bool {
		if _, _, isLedger := splitLedgerTarget(mapping.Target); isLedger {
			return !ledgerLineOpen(folded, mapping.Target)
		}

		id, itemID, isItem := strings.Cut(mapping.Target, "#")

		held, found := folded.entries[id]
		if !found || held.status != statusLive || (held.kind != kindTodo && held.kind != kindTrap) {
			return true
		}

		if !isItem {
			return false
		}

		item := held.item(itemID)

		return item == nil || item.State != itemOpen
	})
}

// landedFor is the pending mappings of the entries on seatName's shelf. A
// match to a state line is no question to settle: it stands under the line
// as evidence, which ledgerLanded reads.
func (mem *memory) landedFor(folded *state, seatName string) []landedMapping {
	var held landedSet
	if !mem.readSlot(landedKey, &held) {
		return nil
	}

	var own []landedMapping

	for _, mapping := range pendingMappings(folded, held.Mappings) {
		if _, _, isLedger := splitLedgerTarget(mapping.Target); isLedger {
			continue
		}

		id, _, _ := strings.Cut(mapping.Target, "#")
		if mem.onShelf(folded.entries[id], seatName) {
			own = append(own, mapping)
		}
	}

	return own
}

// dropLanded removes the dismissed mappings, and every one no longer pending,
// from the store. A delivered mapping stays: only its entry or item ending,
// or a dismiss, settles it.
func (mem *memory) dropLanded(folded *state, dismissed []landedMapping) {
	var held landedSet
	if !mem.readSlot(landedKey, &held) {
		return
	}

	kept := slices.DeleteFunc(pendingMappings(folded, held.Mappings), func(mapping landedMapping) bool {
		return slices.Contains(dismissed, mapping)
	})

	if len(kept) == 0 {
		mem.clearSlot(landedKey)

		return
	}

	mem.writeSlot(landedKey, &landedSet{Mappings: kept})
}

// landedSeenKey names a mapping in a context's shown set by its target and
// commit, so a later commit matching the same entry is news again.
func landedSeenKey(mapping landedMapping) string {
	return mapping.Target + "@" + shortCommit(mapping.Commit)
}

// dismissLanded forgets the stored matches of the entries and items named,
// each checked and found not to settle the work, without ending any: all or
// none, and an id holding no match of the seat's refuses the call.
func (mem *memory) dismissLanded(ctx context.Context, req request) result {
	name, isSeated := mem.seated(ctx)
	if !isSeated {
		return refused("%s", noSeatText(mem.hintChannel(ctx)))
	}

	folded, err := mem.load(ctx)
	if err != nil {
		return refused("%v", err)
	}

	pending := mem.landedFor(folded, name)

	var (
		dismissed []landedMapping
		failures  []string
	)

	for _, id := range req.IDs {
		index := slices.IndexFunc(pending, func(mapping landedMapping) bool { return mapping.Target == id })
		if index < 0 {
			failures = append(failures, fmt.Sprintf("%s: %s", id, ErrNoLanded))

			continue
		}

		if !slices.Contains(dismissed, pending[index]) {
			dismissed = append(dismissed, pending[index])
		}
	}

	if len(failures) > 0 {
		return refused("nothing dismissed:\n  %s", strings.Join(failures, "\n  "))
	}

	mem.dropLanded(folded, dismissed)

	return done("dismissed")
}

func landedDismissPositional(req *request) error {
	if len(req.Positional) < 2 || req.Positional[0] != "dismiss" {
		return errors.New("usage: " + self + " landed dismiss <id>...")
	}

	req.IDs = req.Positional[1:]

	return nil
}

func shortCommit(sha string) string {
	return sha[:min(commitLength, len(sha))]
}

// landedFlag is how a brief line ends for a mapping: retire? on the entry, a
// tick on the item it names.
func landedFlag(mapping landedMapping) string {
	_, itemID, isItem := strings.Cut(mapping.Target, "#")
	if isItem {
		return fmt.Sprintf("ack #%s? landed: %s %s", itemID, shortCommit(mapping.Commit), mapping.Subject)
	}

	return fmt.Sprintf("retire? landed: %s %s", shortCommit(mapping.Commit), mapping.Subject)
}

// landedAnswer is the command that settles a mapping once the agent checked
// it: ack on an item, retire on an entry.
func landedAnswer(at channel, mapping landedMapping) string {
	if strings.Contains(mapping.Target, "#") {
		return settleHint(mapping.Target, "done").in(at)
	}

	return retireHint(mapping.Target, "landed in "+shortCommit(mapping.Commit)).in(at)
}

// landedNotice is the one line a running session gets for its seat's pending
// mappings its context does not hold yet, each then marked in the shown set
// of that context; empty where none is news. The mappings stay stored.
func (mem *memory) landedNotice(ctx context.Context, seatName string, session string) string {
	if !mem.hasSlot(landedKey) {
		return ""
	}

	folded, err := mem.load(ctx)
	if err != nil {
		return ""
	}

	pending := mem.landedFor(folded, seatName)
	if len(pending) == 0 {
		return ""
	}

	var shown shownSet
	mem.readSlot(mem.shownKey(session), &shown)

	parts := make([]string, 0, len(pending))

	for _, mapping := range pending {
		key := landedSeenKey(mapping)
		if slices.Contains(shown.Landed, key) {
			continue
		}

		shown.Landed = append(shown.Landed, key)
		parts = append(parts, fmt.Sprintf("%s by %s %q (%s): %s", mapping.Target, shortCommit(mapping.Commit),
			mapping.Subject, mapping.Test, landedAnswer(mem.hintChannel(ctx), mapping)))
	}

	if len(parts) == 0 {
		return ""
	}

	mem.writeSlot(mem.shownKey(session), &shown)

	return fmt.Sprintf("%s: landed commits may settle live work; check each against the repository, then: %s",
		self, strings.Join(parts, "; "))
}

// watchBranches probes every `merged:` end of a live entry, which records the
// tip of each branch still there, and answers the branches named.
func (probe *endProbe) watchBranches(ctx context.Context, folded *state) []string {
	var watched []string

	for _, held := range folded.entryList() {
		if held.status != statusLive {
			continue
		}

		for _, end := range conditionsOf(held) {
			if end.form != "merged" {
				continue
			}

			probe.branch(ctx, end.value)

			branch, _, _ := strings.Cut(end.value, "@")
			if !slices.Contains(watched, branch) {
				watched = append(watched, branch)
			}
		}
	}

	return watched
}

// learnTips takes, for every watched branch that is gone, its tip from the
// newest merge git subjected by default: the merge's second parent, which a
// branch created, merged and deleted between two probes leaves as its only
// trace. It drops the tips of branches no live end names any more.
func (probe *endProbe) learnTips(commits []landedCommit, watched []string) {
	tips := probe.tips()
	changed := false

	for branch := range tips {
		if !slices.Contains(watched, branch) {
			delete(tips, branch)

			changed = true
		}
	}

	learned := map[string]bool{}

	for _, commit := range commits {
		named := defaultMerge.FindStringSubmatch(commit.subject)
		if named == nil || len(commit.parents) < 2 || !slices.Contains(watched, named[1]) ||
			learned[named[1]] || probe.present[named[1]] {
			continue
		}

		learned[named[1]] = true

		if tips[named[1]] != commit.parents[1] {
			tips[named[1]] = commit.parents[1]
			changed = true
		}
	}

	if changed {
		probe.saveTips()
	}
}

func (probe *endProbe) tips() map[string]string {
	if probe.recorded == nil {
		probe.recorded = branchTips{}
		probe.mem.readSlot(branchTipsKey, &probe.recorded)
	}

	return probe.recorded
}

func (probe *endProbe) saveTips() {
	if len(probe.recorded) == 0 {
		probe.mem.clearSlot(branchTipsKey)

		return
	}

	probe.mem.writeSlot(branchTipsKey, &probe.recorded)
}

// recordTip keeps the tip a probe saw of a branch that exists.
func (probe *endProbe) recordTip(branch string, tip string) {
	if tip == "" || probe.tips()[branch] == tip {
		return
	}

	probe.recorded[branch] = tip
	probe.saveTips()
}
