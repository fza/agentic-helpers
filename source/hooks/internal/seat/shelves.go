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
)

const shelvesName = "shelves.json"

var shelfIDPattern = regexp.MustCompile(`^h-[0-9a-z]{6}$`)

var ErrShelfNameTaken = errors.New("another shelf already has that name")

// Shelf is what the seats filing onto it share: its name, the topics they
// follow, and its lifetime, `durable` where empty.
type Shelf struct {
	Name     string   `json:"name"`
	Follows  []string `json:"follows,omitempty"`
	Lifetime string   `json:"lifetime,omitempty"`
}

// Shelves maps every shelf id of a store to its one shelf.
type Shelves struct {
	path    string
	Shelves map[string]Shelf
}

func IsShelfID(key string) bool {
	return shelfIDPattern.MatchString(key)
}

func ShelvesPath(store string) string {
	return filepath.Join(store, shelvesName)
}

// LoadShelves reads the store's shelves; found is false where the store has
// none, as a store of an earlier format.
func LoadShelves(store string) (Shelves, bool, error) {
	shelves := Shelves{path: ShelvesPath(store), Shelves: map[string]Shelf{}}

	raw, err := os.ReadFile(shelves.path)
	if errors.Is(err, fs.ErrNotExist) {
		return shelves, false, nil
	}

	if err != nil {
		return shelves, false, fmt.Errorf("reading the shelves: %w", err)
	}

	err = json.Unmarshal(raw, &shelves.Shelves)
	if err != nil {
		return shelves, false, fmt.Errorf("parsing the shelves: %w", err)
	}

	if shelves.Shelves == nil {
		shelves.Shelves = map[string]Shelf{}
	}

	return shelves, true, nil
}

func (shelves Shelves) IDOf(name string) (string, bool) {
	for _, id := range shelves.IDs() {
		if shelves.Shelves[id].Name == name {
			return id, true
		}
	}

	return "", false
}

// NameOf is the shelf's name, or the id itself where no shelf has it.
func (shelves Shelves) NameOf(id string) string {
	held, found := shelves.Shelves[id]
	if !found || held.Name == "" {
		return id
	}

	return held.Name
}

func (shelves Shelves) IDs() []string {
	ids := make([]string, 0, len(shelves.Shelves))
	for id := range shelves.Shelves {
		ids = append(ids, id)
	}

	slices.Sort(ids)

	return ids
}

// Mint adds a shelf named name under a fresh random id.
func (shelves *Shelves) Mint(name string, random io.Reader) (string, error) {
	_, taken := shelves.IDOf(name)
	if taken {
		return "", ErrShelfNameTaken
	}

	for range 64 {
		id, err := drawID("h-", random)
		if err != nil {
			return "", err
		}

		if _, used := shelves.Shelves[id]; !used {
			shelves.Shelves[id] = Shelf{Name: name}

			return id, nil
		}
	}

	return "", errors.New("drew no free shelf id in 64 tries")
}

func (shelves *Shelves) Rename(id string, name string) error {
	other, taken := shelves.IDOf(name)
	if taken && other != id {
		return ErrShelfNameTaken
	}

	held := shelves.Shelves[id]
	held.Name = name
	shelves.Shelves[id] = held

	return nil
}

// Save writes the shelves through a temporary file renamed into place.
func (shelves Shelves) Save() error {
	data, err := json.MarshalIndent(shelves.Shelves, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the shelves: %w", err)
	}

	err = os.MkdirAll(filepath.Dir(shelves.path), 0o700)
	if err != nil {
		return fmt.Errorf("creating the store: %w", err)
	}

	temporary := shelves.path + ".tmp"

	err = os.WriteFile(temporary, append(data, '\n'), 0o600)
	if err != nil {
		return fmt.Errorf("writing the shelves: %w", err)
	}

	err = os.Rename(temporary, shelves.path)
	if err != nil {
		return fmt.Errorf("placing the shelves: %w", err)
	}

	return nil
}

// OnShelf is every live seat filing onto the shelf, sorted by id.
func (registry Registry) OnShelf(shelf string) []string {
	var ids []string

	for _, id := range registry.IDs() {
		held := registry.Seats[id]
		if held.Shelf == shelf && !held.Expired {
			ids = append(ids, id)
		}
	}

	return ids
}
