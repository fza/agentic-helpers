package seat

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/fza/agentic-helpers/source/hooks/internal/atomicfile"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
)

const (
	registryName = "seats.json"
	idAlphabet   = "abcdefghijklmnopqrstuvwxyz0123456789"
	idLength     = 6
)

// An id names a seat for good; a name is the label its holder may change. An
// id is a valid file key under the name rule too, which is why no seat may be
// named in an id's shape.
var idPattern = regexp.MustCompile(`^s-[0-9a-z]{6}$`)

var ErrNameTaken = errors.New("another seat already has that name")

// Entry is one seat of the registry: its current name and the shelf it files
// onto. Follows is what a store of an earlier format kept per seat; its shelf
// keeps them now. An expired seat was purged with its temporary shelf and is
// kept only while an entry that survived the purge still names it: its name is
// free for a new seat. A private seat is listed by its own project alone.
type Entry struct {
	Name    string   `json:"name"`
	Shelf   string   `json:"shelf,omitempty"`
	Follows []string `json:"follows,omitempty"`
	Expired bool     `json:"expired,omitempty"`
	Private bool     `json:"private,omitempty"`
}

// Registry maps every seat id of a store to its one entry. It is the whole of
// what a name resolves through: events, claims and slices name the id.
type Registry struct {
	path  string
	Seats map[string]Entry
}

func IsID(key string) bool {
	return idPattern.MatchString(key)
}

func RegistryPath(store string) string {
	return filepath.Join(store, registryName)
}

// LoadRegistry reads the store's registry; found is false where the store has
// none, as a store of an earlier format.
func LoadRegistry(store string) (Registry, bool, error) {
	registry := Registry{path: RegistryPath(store), Seats: map[string]Entry{}}

	raw, err := os.ReadFile(registry.path)
	if errors.Is(err, fs.ErrNotExist) {
		return registry, false, nil
	}

	if err != nil {
		return registry, false, fmt.Errorf("reading the seat registry: %w", err)
	}

	err = json.Unmarshal(raw, &registry.Seats)
	if err != nil {
		return registry, false, fmt.Errorf("parsing the seat registry: %w", err)
	}

	if registry.Seats == nil {
		registry.Seats = map[string]Entry{}
	}

	return registry, true, nil
}

// IDOf is the id of the live seat now named name.
func (registry Registry) IDOf(name string) (string, bool) {
	for _, id := range registry.IDs() {
		if registry.Seats[id].Name == name && !registry.Seats[id].Expired {
			return id, true
		}
	}

	return "", false
}

// NameOf is the current name of the seat id, marked where the seat expired,
// or the id itself where the registry holds none for it.
func (registry Registry) NameOf(id string) string {
	held, found := registry.Seats[id]
	if !found || held.Name == "" {
		return id
	}

	if held.Expired {
		return held.Name + " (expired)"
	}

	return held.Name
}

func (registry Registry) IDs() []string {
	ids := make([]string, 0, len(registry.Seats))
	for id := range registry.Seats {
		ids = append(ids, id)
	}

	slices.Sort(ids)

	return ids
}

// Mint adds a seat named name under a fresh random id.
func (registry *Registry) Mint(name string, random io.Reader) (string, error) {
	_, taken := registry.IDOf(name)
	if taken {
		return "", ErrNameTaken
	}

	for range 64 {
		id, err := drawID("s-", random)
		if err != nil {
			return "", err
		}

		if _, used := registry.Seats[id]; !used {
			registry.Seats[id] = Entry{Name: name}

			return id, nil
		}
	}

	return "", errors.New("drew no free seat id in 64 tries")
}

// Rename gives the seat id a new name, which no other seat may hold.
func (registry *Registry) Rename(id string, name string) error {
	other, taken := registry.IDOf(name)
	if taken && other != id {
		return ErrNameTaken
	}

	held := registry.Seats[id]
	held.Name = name
	registry.Seats[id] = held

	return nil
}

// Save writes the registry through a temporary file renamed into place, so a
// reader sees the old registry or the new, never half of one.
func (registry Registry) Save(root string) error {
	data, err := json.MarshalIndent(registry.Seats, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the seat registry: %w", err)
	}

	err = safefs.MkdirAll(root, filepath.Dir(registry.path), 0o700)
	if err != nil {
		return fmt.Errorf("creating the store: %w", err)
	}

	err = atomicfile.Write(root, registry.path, append(data, '\n'), 0o600)
	if err != nil {
		return fmt.Errorf("placing the seat registry: %w", err)
	}

	return nil
}

func drawID(prefix string, random io.Reader) (string, error) {
	id := make([]byte, 0, idLength)
	buffer := make([]byte, 1)

	for len(id) < idLength {
		_, err := io.ReadFull(random, buffer)
		if err != nil {
			return "", fmt.Errorf("drawing an id: %w", err)
		}

		// 252 is the largest multiple of the alphabet's size below 256: a byte
		// at or above it would favour the alphabet's first letters.
		if buffer[0] >= 252 {
			continue
		}

		id = append(id, idAlphabet[int(buffer[0])%len(idAlphabet)])
	}

	return prefix + string(id), nil
}
