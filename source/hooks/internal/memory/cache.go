package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/atomicfile"
)

// Heat increments per use. Displaying an entry adds nothing, in the brief or
// in prompt recall alike: an entry shown would otherwise rank higher for being
// shown, and the order would feed itself.
const (
	heatNew       = 1.0
	heatConfirm   = 1.0
	heatShow      = 0.5
	heatSearchHit = 0.25
	heatLink      = 0.5
)

// usedWindow is the span `who` counts a seat's filed and used entries over.
const usedWindow = 14 * 24 * time.Hour

// searchWarmed is how many of a search's top results it warms: a broad search
// returning most of the store says nothing about any one entry.
const searchWarmed = 3

var halfLives = map[string]time.Duration{
	"long":    90 * 24 * time.Hour,
	"normal":  14 * 24 * time.Hour,
	"short":   3 * 24 * time.Hour,
	"scratch": 12 * time.Hour,
}

var retentionLevels = []string{"standing", "long", "normal", "short", "scratch"}

func readJSON(path string, target any) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}

	return json.Unmarshal(data, target) == nil
}

func writeJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encoding %s: %w", filepath.Base(path), err)
	}

	err = os.MkdirAll(filepath.Dir(path), 0o700)
	if err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}

	return atomicfile.Write(path, data, 0o600)
}

func removeIfThere(path string) {
	err := os.Remove(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return
	}
}

// heatValue is an entry's heat as last set, and Used the last time a use
// warmed it, which filing never sets.
type heatValue struct {
	Value float64   `json:"value"`
	At    time.Time `json:"at"`
	Used  time.Time `json:"used,omitzero"`
}

func (mem *memory) heatPath() string {
	return filepath.Join(mem.cacheDir(), "heat.json")
}

func (mem *memory) heats() map[string]heatValue {
	held := map[string]heatValue{}
	readJSON(mem.heatPath(), &held)

	return held
}

func decayed(value heatValue, retention string, now time.Time) float64 {
	halfLife, decays := halfLives[retention]
	if !decays {
		return value.Value
	}

	elapsed := now.Sub(value.At)
	if elapsed <= 0 {
		return value.Value
	}

	return value.Value * math.Pow(0.5, float64(elapsed)/float64(halfLife))
}

// heat is an entry's heat now; an entry never used decays from its filing.
func (mem *memory) heat(heats map[string]heatValue, held *entry) float64 {
	value, found := heats[held.id]
	if !found {
		value = heatValue{Value: heatNew, At: held.filed}
	}

	return decayed(value, held.retention, mem.now())
}

// warm adds to each entry's heat. The cache may be lost at any time, so a
// failure to write it costs ranking, never memory.
func (mem *memory) warm(folded *state, amount float64, ids ...string) {
	if len(ids) == 0 {
		return
	}

	heats := mem.heats()
	for _, id := range ids {
		held, found := folded.entries[id]
		if !found {
			continue
		}

		warmed := heatValue{Value: mem.heat(heats, held) + amount, At: mem.now()}
		if amount > 0 {
			warmed.Used = warmed.At
		}

		heats[id] = warmed
	}

	_ = writeJSON(mem.heatPath(), heats)
}
