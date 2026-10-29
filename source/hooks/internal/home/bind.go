package home

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"time"
)

var (
	ErrSameStore = errors.New("both name one store")
	ErrNoJoiner  = errors.New("no component knows how to join this data")
)

// Bound is what a bind settled: the store the checkout is bound to now, and
// every other store an identifier of the checkout moved from.
type Bound struct {
	Store Identity
	Moved []Moved
}

// Moved is a store that gave up identifiers to a bind, as it stands after.
type Moved struct {
	From        Identity
	Identifiers []string
}

// Bind binds the checkout to the store target names, its own or one merged
// into another. Every identifier of the checkout another store holds moves to
// it, and a graph repository on another store is kept apart from it.
func (resolver Resolver) Bind(ctx context.Context, checkout Checkout, target string) (Bound, error) {
	if !ValidID(target) {
		return Bound{}, fmt.Errorf("%w: %q is no store id", ErrNoStore, target)
	}

	return resolver.bind(ctx, checkout, target)
}

// BindNew binds the checkout to a store created for it.
func (resolver Resolver) BindNew(ctx context.Context, checkout Checkout) (Bound, error) {
	return resolver.bind(ctx, checkout, "")
}

func (resolver Resolver) bind(ctx context.Context, checkout Checkout, target string) (Bound, error) {
	err := resolver.Roots.CheckFormat()
	if err != nil {
		return Bound{}, err
	}

	var bound Bound

	checkout = checkout.withRemotes(ctx)

	err = resolver.locked(ctx, func() error {
		held := resolver.Roots.load()
		held.rebuild()

		graphID := ""
		if checkout.Graph != nil {
			graphID, _ = held.strongMatch(*checkout.Graph)
		}

		id := held.canonical(target)

		if target == "" {
			created, createErr := resolver.create(held, suffixFor(checkout))
			if createErr != nil {
				return createErr
			}

			id = created
		}

		if id == "" {
			return fmt.Errorf("%w %s; `who --all` names the stores", ErrNoStore, target)
		}

		for _, other := range sortedIDs(held.roots.Identities()) {
			if other == id {
				continue
			}

			identity, _ := held.store(other)

			released := release(identity, checkout)
			if len(released) == 0 {
				continue
			}

			writeErr := writeIdentity(held.roots.storeDir(other), *identity)
			if writeErr != nil {
				return writeErr
			}

			bound.Moved = append(bound.Moved, Moved{From: *identity, Identifiers: released})
		}

		identity, _ := held.store(id)
		held.admit(identity, checkout, true)

		if graphID != "" && graphID != id && !slices.Contains(identity.KeptApart, checkout.Graph.CommonDir) {
			identity.KeptApart = append(identity.KeptApart, checkout.Graph.CommonDir)
		}

		writeErr := writeIdentity(held.roots.storeDir(id), *identity)
		if writeErr != nil {
			return writeErr
		}

		checkout.Pointer = ""
		writePointer(checkout, id)

		bound.Store = *identity

		return errors.Join(held.roots.ensureFormat(), held.roots.RebuildIndex())
	})

	return bound, err
}

// release takes every identifier of the checkout from the store, answering
// them as a person reads them.
func release(identity *Identity, checkout Checkout) []string {
	var released []string

	kept := identity.Checkouts[:0]

	for _, admitted := range identity.Checkouts {
		sameDir := pathKey(admitted.CommonDir, admitted.Path) == pathKey(checkout.CommonDir, checkout.Root)
		sameInode := admitted.Volume != "" && admitted.Volume == checkout.Volume && admitted.Inode == checkout.Inode

		if sameDir || sameInode {
			released = append(released, "checkout "+admitted.Path)

			continue
		}

		kept = append(kept, admitted)
	}

	identity.Checkouts = kept

	drop := func(list *[]string, value string, label string) {
		if value != "" && slices.Contains(*list, value) {
			*list = slices.DeleteFunc(*list, func(held string) bool { return held == value })
			released = append(released, label+" "+value)
		}
	}

	drop(&identity.RepoIDs, checkout.RepoID, "repo_id")
	drop(&identity.Origins, checkout.Origin, "origin")

	return released
}

// Joiner joins a component's data of one store into another's, from and into
// being the component's folders. It runs holding the home's lock.
type Joiner func(ctx context.Context, from string, into string) error

// Merge joins the store from into the store into: each component's data, by
// its joiner where both hold some and by a move where only from does, then
// its checkouts and identifiers. from's id keeps naming the joined store, so
// a pointer still holding it resolves, and every pointer that named it is
// rewritten.
func (resolver Resolver) Merge(ctx context.Context, from string, into string, joiners map[string]Joiner) (Identity, error) {
	err := resolver.Roots.CheckFormat()
	if err != nil {
		return Identity{}, err
	}

	var joined Identity

	err = resolver.locked(ctx, func() error {
		held := resolver.Roots.load()
		held.rebuild()

		fromID, intoID := held.canonical(from), held.canonical(into)

		switch {
		case fromID == "":
			return fmt.Errorf("%w %s", ErrNoStore, from)
		case intoID == "":
			return fmt.Errorf("%w %s", ErrNoStore, into)
		case fromID == intoID:
			return fmt.Errorf("%s and %s: %w", from, into, ErrSameStore)
		}

		components, joinErr := components(held.roots.storeDir(fromID))
		if joinErr != nil {
			return joinErr
		}

		for _, component := range components {
			_, statErr := os.Stat(filepath.Join(held.roots.storeDir(intoID), component))
			if statErr == nil && joiners[component] == nil {
				return fmt.Errorf("%s: %w", component, ErrNoJoiner)
			}
		}

		for _, component := range components {
			joinErr = joinComponent(ctx, held.roots, fromID, intoID, component, joiners[component])
			if joinErr != nil {
				return joinErr
			}
		}

		source, _ := held.store(fromID)
		target, _ := held.store(intoID)
		absorb(target, *source)

		writeErr := writeIdentity(held.roots.storeDir(intoID), *target)
		if writeErr != nil {
			return writeErr
		}

		removeErr := errors.Join(os.RemoveAll(held.roots.storeDir(fromID)),
			os.RemoveAll(filepath.Join(held.roots.Cache, storesDir, fromID)))
		if removeErr != nil {
			return fmt.Errorf("removing the joined store %s: %w", fromID, removeErr)
		}

		for _, admitted := range source.Checkouts {
			if admitted.CommonDir != "" && readPointer(admitted.CommonDir) == fromID {
				writePointer(Checkout{CommonDir: admitted.CommonDir}, intoID)
			}
		}

		joined = *target

		return held.roots.RebuildIndex()
	})

	return joined, err
}

func components(dir string) ([]string, error) {
	listed, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}

	var names []string

	for _, entry := range listed {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}

	return names, nil
}

func joinComponent(ctx context.Context, roots Roots, fromID string, intoID string, component string, join Joiner) error {
	from, into := roots.Store(fromID, component), roots.Store(intoID, component)

	_, err := os.Stat(into)
	if err != nil {
		err = os.Rename(from, into)
		if err != nil {
			return fmt.Errorf("moving %s into %s: %w", component, intoID, err)
		}

		return nil
	}

	err = join(ctx, from, into)
	if err != nil {
		return fmt.Errorf("joining %s into %s: %w", component, intoID, err)
	}

	return nil
}

func absorb(target *Identity, source Identity) {
	for _, admitted := range source.Checkouts {
		if !slices.Contains(target.Checkouts, admitted) {
			target.Checkouts = append(target.Checkouts, admitted)
		}
	}

	target.RepoIDs = append(target.RepoIDs, source.RepoIDs...)
	target.Origins = append(target.Origins, source.Origins...)
	target.LegacyKeys = append(target.LegacyKeys, source.LegacyKeys...)
	target.KeptApart = append(target.KeptApart, source.KeptApart...)
	target.Merged = append(append(target.Merged, source.ID), source.Merged...)
	target.normalize()
}

// Adopt creates a store admitting every checkout given and writes the
// pointer into each git common directory: the way old data whose checkouts
// still exist becomes a store. legacyKeys are the keys it was kept under.
func (resolver Resolver) Adopt(ctx context.Context, checkouts []Checkout, legacyKeys []string) (string, error) {
	if len(checkouts) == 0 {
		return "", errors.New("adopting a store takes at least one checkout")
	}

	var id string

	for index := range checkouts {
		checkouts[index] = checkouts[index].withRemotes(ctx)
	}

	err := resolver.locked(ctx, func() error {
		held := resolver.Roots.load()
		held.rebuild()

		created, err := resolver.create(held, suffixFor(checkouts[0]))
		if err != nil {
			return err
		}

		identity, _ := held.store(created)
		identity.LegacyKeys = append(identity.LegacyKeys, legacyKeys...)

		for _, checkout := range checkouts {
			held.admit(identity, checkout, true)
		}

		err = writeIdentity(held.roots.storeDir(created), *identity)
		if err != nil {
			return err
		}

		for _, checkout := range checkouts {
			writePointer(checkout, created)
		}

		id = created

		return errors.Join(held.roots.ensureFormat(), held.roots.RebuildIndex())
	})

	return id, err
}

// Unclaim creates a store under `<data>/unclaimed/` for data whose checkout
// is gone: kept under legacyKey, last seen at lastKnown (their git common directories), unclaimed since.
// The data itself goes into UnclaimedStore.
func (resolver Resolver) Unclaim(ctx context.Context, legacyKey string, lastKnown []string, since time.Time) (string, error) {
	var id string

	err := resolver.locked(ctx, func() error {
		created, err := newID(resolver.now(), resolver.random(), slug(path.Base(legacyKey)))
		if err != nil {
			return err
		}

		stamp := since.UTC()
		identity := Identity{ID: created, Created: resolver.now(), LegacyKeys: []string{legacyKey}, Unclaimed: &stamp}

		for _, commonDir := range lastKnown {
			identity.Checkouts = append(identity.Checkouts, Admitted{Path: filepath.Dir(commonDir), CommonDir: commonDir})
		}

		err = writeIdentity(resolver.Roots.unclaimedStore(created), identity)
		if err != nil {
			return err
		}

		id = created

		return errors.Join(resolver.Roots.ensureFormat(), resolver.Roots.RebuildIndex())
	})

	return id, err
}

// UnclaimedStore is where a component keeps an unclaimed store's data.
func (roots Roots) UnclaimedStore(id string, component string) string {
	return filepath.Join(roots.unclaimedStore(id), component)
}

// SweepUnclaimed deletes every unclaimed store whose grace has passed since
// it became unclaimed and none of whose last known checkouts exists again,
// answering the ids it deleted.
func (resolver Resolver) SweepUnclaimed(ctx context.Context, grace time.Duration) ([]string, error) {
	_, err := os.Stat(resolver.Roots.unclaimed())
	if err != nil {
		return nil, nil
	}

	var removed []string

	err = resolver.locked(ctx, func() error {
		now := resolver.now()

		for id, identity := range readAll(resolver.Roots.unclaimed()) {
			if identity.Unclaimed == nil || now.Sub(*identity.Unclaimed) < grace || anyExists(identity.Checkouts) {
				continue
			}

			removeErr := os.RemoveAll(resolver.Roots.unclaimedStore(id))
			if removeErr != nil {
				return fmt.Errorf("deleting the unclaimed store %s: %w", id, removeErr)
			}

			removed = append(removed, id)
		}

		if len(removed) == 0 {
			return nil
		}

		slices.Sort(removed)

		return resolver.Roots.RebuildIndex()
	})

	return removed, err
}

func anyExists(checkouts []Admitted) bool {
	return slices.ContainsFunc(checkouts, func(admitted Admitted) bool {
		_, err := os.Stat(cmpOr(admitted.CommonDir, admitted.Path))

		return err == nil
	})
}
