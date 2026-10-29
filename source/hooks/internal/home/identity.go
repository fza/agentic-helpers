package home

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/atomicfile"
)

// identityLimit caps an identity read: one lists checkouts and identifiers,
// never data.
const identityLimit = 1 << 20

// An id is a UUID v7, then a suffix fixed at creation naming the project to a
// reader. Nothing but this shape is ever joined into a path.
var idPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}(-[a-z0-9]+(-[a-z0-9]+)*)?$`)

const uuidLength = 36

// ValidID holds a store id to its shape.
func ValidID(id string) bool {
	return idPattern.MatchString(id)
}

// Admitted is one checkout of a store: its directory, its git common
// directory where it is a git checkout, and the volume and inode of that
// directory, which survive a rename.
type Admitted struct {
	Path      string `json:"path"`
	CommonDir string `json:"common_dir,omitempty"`
	Volume    string `json:"volume,omitempty"`
	Inode     uint64 `json:"inode,omitempty"`
}

// Identity is what a store says about itself. RepoIDs and Origins are weak
// identifiers: a checkout matching only them is asked about. KeptApart lists
// the git common directories of graph repositories this store stays apart
// from in a split layout; Merged the ids of stores joined into it, which a
// pointer may still name. Unclaimed is when the store lost every checkout it
// knew, set only on a store waiting under `<data>/unclaimed/`.
type Identity struct {
	ID         string     `json:"id"`
	Created    time.Time  `json:"created"`
	Checkouts  []Admitted `json:"checkouts"`
	RepoIDs    []string   `json:"repo_ids,omitempty"`
	Origins    []string   `json:"origins,omitempty"`
	LegacyKeys []string   `json:"legacy_keys,omitempty"`
	KeptApart  []string   `json:"kept_apart,omitempty"`
	Merged     []string   `json:"merged,omitempty"`
	Unclaimed  *time.Time `json:"unclaimed,omitempty"`
}

// Name is how a store reads to a person: its repo_id, else its origin, else
// the suffix of its id.
func (identity Identity) Name() string {
	switch {
	case len(identity.RepoIDs) > 0:
		return identity.RepoIDs[0]
	case len(identity.Origins) > 0:
		return identity.Origins[0]
	}

	return Suffix(identity.ID)
}

// Paths is every checkout directory the store admits.
func (identity Identity) Paths() []string {
	paths := make([]string, 0, len(identity.Checkouts))
	for _, admitted := range identity.Checkouts {
		paths = append(paths, admitted.Path)
	}

	return paths
}

// Suffix is the part of an id after its UUID.
func Suffix(id string) string {
	if len(id) <= uuidLength+1 {
		return ""
	}

	return id[uuidLength+1:]
}

func readIdentity(path string) (Identity, error) {
	file, err := os.Open(path)
	if err != nil {
		return Identity{}, err
	}

	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, identityLimit))
	if err != nil {
		return Identity{}, fmt.Errorf("reading %s: %w", path, err)
	}

	var identity Identity

	err = json.Unmarshal(data, &identity)
	if err != nil {
		return Identity{}, fmt.Errorf("reading %s: %w", path, err)
	}

	if !ValidID(identity.ID) || identity.ID != filepath.Base(filepath.Dir(path)) {
		return Identity{}, fmt.Errorf("%s names no store of its own folder", path)
	}

	return identity, nil
}

func writeIdentity(dir string, identity Identity) error {
	err := os.MkdirAll(dir, 0o700)
	if err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	identity.normalize()

	data, err := json.MarshalIndent(identity, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the identity of %s: %w", identity.ID, err)
	}

	return atomicfile.Write(filepath.Join(dir, identityName), append(data, '\n'), 0o600)
}

func (identity *Identity) normalize() {
	for _, list := range []*[]string{
		&identity.RepoIDs, &identity.Origins, &identity.LegacyKeys, &identity.KeptApart, &identity.Merged,
	} {
		slices.Sort(*list)
		*list = slices.Compact(*list)
	}

	if identity.Checkouts == nil {
		identity.Checkouts = []Admitted{}
	}
}

// Identities is every store's identity, by id; a store whose identity cannot
// be read is left out.
func (roots Roots) Identities() map[string]Identity {
	return readAll(roots.Stores())
}

func readAll(root string) map[string]Identity {
	held := map[string]Identity{}

	listed, err := os.ReadDir(root)
	if err != nil {
		return held
	}

	for _, entry := range listed {
		if !entry.IsDir() || !ValidID(entry.Name()) {
			continue
		}

		identity, err := readIdentity(filepath.Join(root, entry.Name(), identityName))
		if err == nil {
			held[identity.ID] = identity
		}
	}

	return held
}

// Identity reads one store's identity.
func (roots Roots) Identity(id string) (Identity, error) {
	if !ValidID(id) {
		return Identity{}, fmt.Errorf("%q is no store id", id)
	}

	identity, err := readIdentity(filepath.Join(roots.storeDir(id), identityName))
	if errors.Is(err, fs.ErrNotExist) {
		return Identity{}, fmt.Errorf("no store %s: %w", id, err)
	}

	return identity, err
}

// newID draws a UUID v7 for now and joins suffix to it.
func newID(now time.Time, random io.Reader, suffix string) (string, error) {
	var uuid [16]byte

	binary.BigEndian.PutUint64(uuid[:8], uint64(now.UnixMilli())<<16)

	_, err := io.ReadFull(random, uuid[6:])
	if err != nil {
		return "", fmt.Errorf("drawing a store id: %w", err)
	}

	uuid[6] = 0x70 | uuid[6]&0x0f
	uuid[8] = 0x80 | uuid[8]&0x3f

	text := hex.EncodeToString(uuid[:])
	id := strings.Join([]string{text[0:8], text[8:12], text[12:16], text[16:20], text[20:32]}, "-")

	if suffix != "" {
		id += "-" + suffix
	}

	return id, nil
}

const suffixLimit = 40

// slug is text as a suffix: lower-case letters and digits, any run of other
// characters one dash, at most suffixLimit long.
func slug(text string) string {
	var built strings.Builder

	dash := false

	for _, char := range strings.ToLower(text) {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			if dash && built.Len() > 0 {
				built.WriteByte('-')
			}

			built.WriteRune(char)

			dash = false

			continue
		}

		dash = true
	}

	return strings.Trim(built.String()[:min(built.Len(), suffixLimit)], "-")
}

// suffixFor is the last path part of the repo_id, else of the origin, else
// the checkout directory's name.
func suffixFor(checkout Checkout) string {
	for _, named := range []string{checkout.RepoID, checkout.Origin, checkout.Root} {
		trimmed := strings.TrimRight(named, "/")
		if trimmed == "" {
			continue
		}

		last := trimmed[strings.LastIndexAny(trimmed, "/:")+1:]

		suffix := slug(strings.TrimSuffix(last, ".git"))
		if suffix != "" {
			return suffix
		}
	}

	return ""
}
