package home

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/atomicfile"
	"github.com/fza/agentic-helpers/source/hooks/internal/filelock"
)

var ErrNoStore = errors.New("no such store")

// Resolver maps checkouts to stores. Now and Random default to the clock and
// crypto/rand.
type Resolver struct {
	Roots  Roots
	Now    func() time.Time
	Random io.Reader
}

// Resolution is the store a checkout works on. New says no store matched:
// Store is then an id nothing holds yet, which Ensure would create. A
// Question stands where the checkout's store is the user's call; Store is
// empty then, unless the checkout keeps working on its own store meanwhile, as
// the code side of a split layout does.
type Resolution struct {
	Store    string
	New      bool
	Question *Question
}

func (resolver Resolver) now() time.Time {
	if resolver.Now == nil {
		return time.Now().UTC()
	}

	return resolver.Now().UTC()
}

func (resolver Resolver) random() io.Reader {
	if resolver.Random == nil {
		return rand.Reader
	}

	return resolver.Random
}

// Find resolves the checkout without creating a store: a checkout matching
// none gets a Resolution marked New. Matching a store teaches it what a
// strong match may teach, and an unclaimed store the checkout's legacy key
// names is promoted.
func (resolver Resolver) Find(ctx context.Context, checkout Checkout) (Resolution, error) {
	return resolver.resolve(ctx, checkout, false)
}

// Ensure resolves the checkout as Find does, creating a store, silently,
// where none matches and no question stands.
func (resolver Resolver) Ensure(ctx context.Context, checkout Checkout) (Resolution, error) {
	return resolver.resolve(ctx, checkout, true)
}

func (resolver Resolver) resolve(ctx context.Context, checkout Checkout, create bool) (Resolution, error) {
	err := resolver.Roots.CheckFormat()
	if err != nil {
		return Resolution{}, err
	}

	settled, found := resolver.settled(checkout)
	if found {
		return settled, resolver.keepIndex(ctx)
	}

	_, err = os.Stat(resolver.Roots.Data)
	if err != nil && !create {
		return resolver.unbound()
	}

	checkout = checkout.withRemotes(ctx)

	var resolution Resolution

	err = resolver.locked(ctx, func() error {
		held := resolver.Roots.load()

		// A store is created only once the identities themselves, not just
		// the index, show none matching.
		next := held.decide(checkout)
		if next.fresh && create && !held.rebuilt {
			held.rebuild()
			next = held.decide(checkout)
		}

		if next.fresh && !create {
			var unboundErr error

			resolution, unboundErr = resolver.unbound()

			return errors.Join(unboundErr, held.saveIndex())
		}

		var applyErr error

		resolution, applyErr = resolver.apply(held, checkout, next)

		return applyErr
	})

	return resolution, err
}

// keepIndex rebuilds an index gone missing, which no lookup by pointer needs
// but every other lookup does.
func (resolver Resolver) keepIndex(ctx context.Context) error {
	_, err := os.Stat(resolver.Roots.index())
	if err == nil {
		return nil
	}

	return resolver.locked(ctx, func() error {
		_, statErr := os.Stat(resolver.Roots.index())
		if statErr == nil {
			return nil
		}

		return resolver.Roots.RebuildIndex()
	})
}

func (resolver Resolver) unbound() (Resolution, error) {
	id, err := newID(resolver.now(), resolver.random(), "")
	if err != nil {
		return Resolution{}, err
	}

	return Resolution{Store: id, New: true}, nil
}

// settled answers without the lock where the pointer names a store admitting
// this very checkout, as it stands, and nothing about its graph repository
// asks anything.
func (resolver Resolver) settled(checkout Checkout) (Resolution, bool) {
	if checkout.Pointer == "" {
		return Resolution{}, false
	}

	identity, err := resolver.Roots.Identity(checkout.Pointer)
	if err != nil || !admitsExactly(identity, checkout) {
		return Resolution{}, false
	}

	graph := checkout.Graph
	if graph != nil && graph.Pointer != identity.ID && !slices.Contains(identity.KeptApart, graph.CommonDir) {
		return Resolution{}, false
	}

	return Resolution{Store: identity.ID}, true
}

func admitsExactly(identity Identity, checkout Checkout) bool {
	return slices.ContainsFunc(identity.Checkouts, func(admitted Admitted) bool {
		return admitted.Path == checkout.Root && admitted.CommonDir == checkout.CommonDir &&
			admitted.Volume == checkout.Volume && admitted.Inode == checkout.Inode
	})
}

// locked runs change holding the home's lock, which every change to a
// pointer, an identity or the index takes.
func (resolver Resolver) locked(ctx context.Context, change func() error) error {
	err := ctx.Err()
	if err != nil {
		return err
	}

	err = os.MkdirAll(resolver.Roots.Data, 0o700)
	if err != nil {
		return fmt.Errorf("creating %s: %w", resolver.Roots.Data, err)
	}

	file, err := os.OpenFile(filepath.Join(resolver.Roots.Data, lockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("opening the home's lock: %w", err)
	}

	defer func() { _ = file.Close() }()

	err = filelock.Exclusive(file)
	if err != nil {
		return fmt.Errorf("taking the home's lock: %w", err)
	}

	defer func() { _ = filelock.Unlock(file) }()

	return change()
}

// mapping is the identities as a decision reads them: through the index,
// each hit checked against the identity it names.
type mapping struct {
	roots     Roots
	held      index
	rebuilt   bool
	stores    map[string]*Identity
	unclaimed map[string]*Identity
}

func (roots Roots) load() *mapping {
	held, found := roots.readIndex()

	loaded := &mapping{roots: roots, held: held, stores: map[string]*Identity{}, unclaimed: map[string]*Identity{}}
	if !found {
		loaded.rebuild()
	}

	return loaded
}

func (held *mapping) rebuild() {
	stores := readAll(held.roots.Stores())
	unclaimed := readAll(held.roots.unclaimed())

	held.held = buildIndex(stores, unclaimed)
	held.rebuilt = true
	held.stores = map[string]*Identity{}
	held.unclaimed = map[string]*Identity{}

	for id, identity := range stores {
		held.stores[id] = &identity
	}

	for id, identity := range unclaimed {
		held.unclaimed[id] = &identity
	}
}

func (held *mapping) saveIndex() error {
	if !held.rebuilt {
		return nil
	}

	return held.roots.writeIndex(held.held)
}

func (held *mapping) store(id string) (*Identity, bool) {
	identity, found := held.stores[id]
	if found {
		return identity, true
	}

	if !ValidID(id) {
		return nil, false
	}

	read, err := readIdentity(filepath.Join(held.roots.storeDir(id), identityName))
	if err != nil {
		return nil, false
	}

	held.stores[id] = &read

	return &read, true
}

// lookup is every store holding key. A hit its store does not confirm makes
// the index rebuilt once.
func (held *mapping) lookup(key string) []string {
	if key == "" {
		return nil
	}

	var ids []string

	for _, id := range held.held.Identifiers[key] {
		identity, found := held.store(id)
		if !found || !identity.holds(key) {
			if !held.rebuilt {
				held.rebuild()

				return held.lookup(key)
			}

			continue
		}

		ids = append(ids, id)
	}

	return ids
}

// canonical is the store an id names: itself, or the store it was merged
// into.
func (held *mapping) canonical(id string) string {
	_, found := held.store(id)
	if found {
		return id
	}

	merged := held.lookup(aliasKey(id))
	if len(merged) == 1 {
		return merged[0]
	}

	return ""
}

// decision is what resolving a checkout settles: the store it works on, or a
// store to create (fresh) or promote, whether its graph repository's
// checkout joins that store too, and any question standing.
type decision struct {
	store      string
	fresh      bool
	promote    string
	graphJoins bool
	question   *Question
}

func (held *mapping) decide(checkout Checkout) decision {
	id, question := held.strongMatch(checkout)
	if question != nil {
		return decision{question: question}
	}

	if id != "" {
		next := decision{store: id}
		held.checkSplit(&next, checkout)

		return next
	}

	joined, found := held.joinGraph(checkout)
	if found {
		return joined
	}

	weak := held.weakMatch(checkout)
	if len(weak) > 0 {
		return decision{question: held.weakQuestion(checkout, weak)}
	}

	promote := held.unclaimedFor(checkout.LegacyKey)
	if promote != "" {
		return decision{promote: promote, graphJoins: checkout.Graph != nil}
	}

	return decision{fresh: true, graphJoins: checkout.Graph != nil}
}

// joinGraph is the one place a checkout with no store of its own joins the
// store of the graph repository its `.sdd/` names. The config naming that
// repository is committed text a clone brings along, no stronger a sign of
// the owner's intent than a shared origin, so the join is silent only where
// the graph store already admits the checkout (a bind, an earlier join, an
// index too stale to say so); otherwise a ReasonSplit question names the
// graph store, and nothing is joined or written until a bind answers it.
func (held *mapping) joinGraph(checkout Checkout) (decision, bool) {
	if checkout.Graph == nil {
		return decision{}, false
	}

	graphID, question := held.strongMatch(*checkout.Graph)
	if question != nil || graphID == "" {
		return decision{}, false
	}

	identity, _ := held.store(graphID)
	if admits(*identity, checkout) {
		return decision{store: graphID, graphJoins: true}, true
	}

	asked := held.question(checkout, ReasonSplit, []string{graphID})
	asked.Graph = checkout.Graph.Root

	return decision{question: asked}, true
}

// admits says the store lists the checkout.
func admits(identity Identity, checkout Checkout) bool {
	return slices.ContainsFunc(identity.Checkouts, func(admitted Admitted) bool { return admitted.is(checkout) })
}

// is says the admitted checkout is this one: the same git common directory
// and path, or the same volume and inode, as a moved checkout keeps.
func (admitted Admitted) is(checkout Checkout) bool {
	sameDir := pathKey(admitted.CommonDir, admitted.Path) == pathKey(checkout.CommonDir, checkout.Root)
	sameInode := admitted.Volume != "" && admitted.Volume == checkout.Volume && admitted.Inode == checkout.Inode

	return sameDir || sameInode
}

// strongMatch is the store the checkout's pointer, inode or path names, in
// that order, or a question where they disagree with the store they name.
func (held *mapping) strongMatch(checkout Checkout) (string, *Question) {
	if checkout.Pointer != "" {
		id := held.canonical(checkout.Pointer)

		identity, found := held.store(id)
		if found {
			if held.confirms(*identity, checkout) {
				return id, nil
			}

			return "", held.question(checkout, ReasonCopied, []string{id})
		}
	}

	for _, key := range []string{inodeKey(checkout.Volume, checkout.Inode), pathKey(checkout.CommonDir, checkout.Root)} {
		ids := held.lookup(key)

		switch {
		case len(ids) > 1:
			return "", held.question(checkout, ReasonConflict, ids)
		case len(ids) == 1:
			identity, _ := held.store(ids[0])
			if key != inodeKey(checkout.Volume, checkout.Inode) && contradicts(*identity, checkout) {
				return "", held.question(checkout, ReasonReused, ids)
			}

			return ids[0], nil
		}
	}

	return "", nil
}

// confirms holds a pointer to the store it names: the store admits the
// checkout by path or by volume and inode, or admits none still on disk, as
// after a move across volumes. A pointer copied with a checkout it does not
// admit confirms nothing.
func (held *mapping) confirms(identity Identity, checkout Checkout) bool {
	for _, admitted := range identity.Checkouts {
		if pathKey(admitted.CommonDir, admitted.Path) == pathKey(checkout.CommonDir, checkout.Root) {
			return true
		}

		if admitted.Volume != "" && admitted.Volume == checkout.Volume && admitted.Inode == checkout.Inode {
			return true
		}
	}

	for _, admitted := range identity.Checkouts {
		_, err := os.Stat(cmpOr(admitted.CommonDir, admitted.Path))
		if err == nil {
			return false
		}
	}

	return true
}

func cmpOr(first string, second string) string {
	if first != "" {
		return first
	}

	return second
}

// contradicts says the checkout's repo_id or origin differs from the one the
// store knows: a path reused by another repository.
func contradicts(identity Identity, checkout Checkout) bool {
	if checkout.RepoID != "" && len(identity.RepoIDs) > 0 && !slices.Contains(identity.RepoIDs, checkout.RepoID) {
		return true
	}

	return checkout.Origin != "" && len(identity.Origins) > 0 && !slices.Contains(identity.Origins, checkout.Origin)
}

// checkSplit asks keep or merge where the graph repository of a split layout
// keeps another store than the code repository works on, unless that store
// was kept apart; a graph repository with no store joins the code's.
func (held *mapping) checkSplit(next *decision, checkout Checkout) {
	if checkout.Graph == nil {
		return
	}

	graphID, question := held.strongMatch(*checkout.Graph)

	switch {
	case question != nil:
		return
	case graphID == "" || graphID == next.store:
		next.graphJoins = true

		return
	}

	identity, _ := held.store(next.store)
	if slices.Contains(identity.KeptApart, checkout.Graph.CommonDir) {
		return
	}

	next.question = held.question(checkout, ReasonSplit, []string{next.store, graphID})
	next.question.Merge = []string{next.store, graphID}
}

// weakMatch is every store holding the checkout's repo_id or origin.
func (held *mapping) weakMatch(checkout Checkout) []string {
	var ids []string

	if checkout.RepoID != "" {
		ids = append(ids, held.lookup(repoKey(checkout.RepoID))...)
	}

	if checkout.Origin != "" {
		ids = append(ids, held.lookup(originKey(checkout.Origin))...)
	}

	slices.Sort(ids)

	return slices.Compact(ids)
}

func (held *mapping) weakQuestion(checkout Checkout, ids []string) *Question {
	if len(ids) == 1 {
		return held.question(checkout, ReasonKnown, ids)
	}

	return held.question(checkout, ReasonConflict, ids)
}

func (held *mapping) unclaimedFor(legacyKey string) string {
	if legacyKey == "" {
		return ""
	}

	ids := held.held.Unclaimed[legacyKey]
	if len(ids) != 1 {
		return ""
	}

	identity, err := readIdentity(filepath.Join(held.roots.unclaimedStore(ids[0]), identityName))
	if err != nil || !slices.Contains(identity.LegacyKeys, legacyKey) {
		return ""
	}

	held.unclaimed[ids[0]] = &identity

	return ids[0]
}

// apply carries a decision out: the checkout admitted to its store and its
// pointer written, a store promoted or created first where the decision says.
func (resolver Resolver) apply(held *mapping, checkout Checkout, next decision) (Resolution, error) {
	if next.question != nil && next.store == "" {
		return Resolution{Question: next.question}, held.saveIndex()
	}

	id := next.store

	switch {
	case next.promote != "":
		err := resolver.promote(held, next.promote)
		if err != nil {
			return Resolution{}, err
		}

		id = next.promote
	case next.fresh:
		created, err := resolver.create(held, suffixFor(graphOr(checkout)))
		if err != nil {
			return Resolution{}, err
		}

		id = created
	}

	identity, _ := held.store(id)
	changed := held.admit(identity, checkout, false)

	if next.graphJoins && checkout.Graph != nil {
		changed = held.admit(identity, *checkout.Graph, false) || changed
	}

	if changed {
		err := writeIdentity(held.roots.storeDir(id), *identity)
		if err != nil {
			return Resolution{}, err
		}
	}

	writePointer(checkout, id)

	if next.graphJoins && checkout.Graph != nil {
		writePointer(*checkout.Graph, id)
	}

	err := held.roots.ensureFormat()
	if err != nil {
		return Resolution{}, err
	}

	if changed || held.rebuilt || next.fresh || next.promote != "" {
		err = held.roots.RebuildIndex()
		if err != nil {
			return Resolution{}, err
		}
	}

	return Resolution{Store: id, Question: next.question}, nil
}

// graphOr is the checkout a new store is named for: the graph repository's
// in a split layout, whose store the code repository shares.
func graphOr(checkout Checkout) Checkout {
	if checkout.Graph != nil {
		return *checkout.Graph
	}

	return checkout
}

func (resolver Resolver) create(held *mapping, suffix string) (string, error) {
	id, err := newID(resolver.now(), resolver.random(), suffix)
	if err != nil {
		return "", err
	}

	identity := Identity{ID: id, Created: resolver.now()}

	err = writeIdentity(held.roots.storeDir(id), identity)
	if err != nil {
		return "", err
	}

	held.stores[id] = &identity

	return id, nil
}

func (resolver Resolver) promote(held *mapping, id string) error {
	identity := held.unclaimed[id]

	err := os.MkdirAll(held.roots.Stores(), 0o700)
	if err != nil {
		return fmt.Errorf("creating %s: %w", held.roots.Stores(), err)
	}

	err = os.Rename(held.roots.unclaimedStore(id), held.roots.storeDir(id))
	if err != nil {
		return fmt.Errorf("promoting the unclaimed store %s: %w", id, err)
	}

	identity.Unclaimed = nil
	held.stores[id] = identity

	return writeIdentity(held.roots.storeDir(id), *identity)
}

// admit records the checkout in the store: an admitted checkout of the same
// git common directory or the same volume and inode is updated in place, so a
// moved checkout keeps one entry. Its repo_id and origin are learned where no
// other store holds them; force takes them from any that does. It answers
// whether anything changed.
func (held *mapping) admit(identity *Identity, checkout Checkout, force bool) bool {
	entry := Admitted{Path: checkout.Root, CommonDir: checkout.CommonDir, Volume: checkout.Volume, Inode: checkout.Inode}
	changed := false

	at := slices.IndexFunc(identity.Checkouts, func(admitted Admitted) bool { return admitted.is(checkout) })

	switch {
	case at < 0:
		identity.Checkouts = append(identity.Checkouts, entry)
		changed = true
	case identity.Checkouts[at] != entry:
		identity.Checkouts[at] = entry
		changed = true
	}

	learn := func(list *[]string, value string, key string) {
		if value == "" || slices.Contains(*list, value) {
			return
		}

		holders := held.lookup(key)
		if !force && slices.ContainsFunc(holders, func(other string) bool { return other != identity.ID }) {
			return
		}

		*list = append(*list, value)
		changed = true
	}

	learn(&identity.RepoIDs, checkout.RepoID, repoKey(checkout.RepoID))
	learn(&identity.Origins, checkout.Origin, originKey(checkout.Origin))
	learn(&identity.LegacyKeys, checkout.LegacyKey, "")

	return changed
}

// writePointer names the store in the checkout's git common directory. A
// pointer that cannot be written costs only the speed of the next lookup:
// the path and inode still match.
func writePointer(checkout Checkout, id string) {
	if checkout.CommonDir == "" || checkout.Pointer == id {
		return
	}

	_ = atomicfile.Write(filepath.Join(checkout.CommonDir, PointerName), []byte(id+"\n"), 0o644)
}
