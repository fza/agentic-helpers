package embed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/atomicfile"
	"github.com/fza/agentic-helpers/source/hooks/internal/safefs"
)

// FallbackRunes caps every input where the provider reports no token count,
// or where no calibration could finish.
const FallbackRunes = 2048

// CapQuantum is the step a derived cap rounds down to, so probe noise below
// one step leaves the cap, and every row hash folding it, unchanged.
const CapQuantum = 64

// The probes, margins and file naming match sdd's calibration, so one
// endpoint gets one cap from both tools.
const (
	latinProbeRunes    = 1000
	cjkProbeRunes      = 1000
	overflowProbeRunes = 20000
	bisectTolerance    = 64
	safetyMargin       = 0.9
	plateauFraction    = 0.9
	calibrationsDir    = "embedding-calibration"
)

// Measurer is an embedder that answers how many tokens its provider counted
// for one input, 0 where the provider reports none, and the route its
// requests take.
type Measurer interface {
	Measure(ctx context.Context, text string) (int, error)
	Route() (endpoint string, path string)
}

// Calibration is what one serving endpoint allows: Method names how the input
// limit was found (plateau, bisect, unbounded, unreported), LimitTokens is the
// largest accepted count, TokensPerRune the worst observed ratio, and MaxRunes
// the quantised cap every input is sized to.
type Calibration struct {
	Method        string  `json:"method"`
	LimitTokens   int     `json:"limit_tokens"`
	TokensPerRune float64 `json:"tokens_per_rune"`
	MaxRunes      int     `json:"max_runes"`
}

func Quantise(runes int) int {
	return runes / CapQuantum * CapQuantum
}

// Calibrate probes with single inputs: Latin text for the ordinary ratio, CJK
// for the worst, and an overflow for the limit. A provider truncating
// silently shows a count stuck below the linear growth; one refusing an
// over-limit input is bisected between the last size accepted and the first
// refused, any error there reading as a refusal.
func Calibrate(ctx context.Context, measurer Measurer) (Calibration, error) {
	latinTokens, err := measurer.Measure(ctx, repeatRunes("word ", latinProbeRunes))
	if err != nil {
		return Calibration{}, fmt.Errorf("calibration latin probe: %w", err)
	}

	if latinTokens == 0 {
		return Calibration{Method: "unreported", MaxRunes: FallbackRunes}, nil
	}

	cjkTokens, err := measurer.Measure(ctx, repeatRunes("字", cjkProbeRunes))
	if err != nil {
		return Calibration{}, fmt.Errorf("calibration cjk probe: %w", err)
	}

	worst := math.Max(float64(latinTokens)/latinProbeRunes, float64(cjkTokens)/cjkProbeRunes)
	method, limit := findLimit(ctx, measurer, latinTokens)

	maxRunes := Quantise(int(math.Floor(float64(limit) * safetyMargin / worst)))
	if maxRunes < CapQuantum {
		return Calibration{}, fmt.Errorf("calibrated limit of %d tokens leaves no room for a %d-rune cap", limit, CapQuantum)
	}

	return Calibration{Method: method, LimitTokens: limit, TokensPerRune: worst, MaxRunes: maxRunes}, nil
}

func findLimit(ctx context.Context, measurer Measurer, latinTokens int) (string, int) {
	overflowTokens, err := measurer.Measure(ctx, repeatRunes("word ", overflowProbeRunes))
	if err == nil {
		expected := float64(overflowProbeRunes) * float64(latinTokens) / latinProbeRunes
		if float64(overflowTokens) < plateauFraction*expected {
			return "plateau", overflowTokens
		}

		return "unbounded", overflowTokens
	}

	low, lowTokens, high := latinProbeRunes, latinTokens, overflowProbeRunes
	for high-low > bisectTolerance {
		middle := (low + high) / 2

		tokens, err := measurer.Measure(ctx, repeatRunes("word ", middle))
		if err != nil {
			high = middle

			continue
		}

		low, lowTokens = middle, tokens
	}

	return "bisect", lowTokens
}

func repeatRunes(unit string, count int) string {
	runes := []rune(strings.Repeat(unit, count/len([]rune(unit))+1))

	return string(runes[:count])
}

// CalibrationKey names one serving endpoint of one vector space: the vector
// fingerprint, the endpoint URL and the path. The vector store stays out of
// it, so a changed endpoint measures again and re-embeds nothing by itself.
func CalibrationKey(fingerprint string, endpoint string, path string) string {
	return fingerprint + "\n" + endpoint + "\n" + path
}

// CalibrationPath is where the calibration for key lives below the cache root.
func CalibrationPath(cache string, key string) string {
	sum := sha256.Sum256([]byte(key))

	return filepath.Join(cache, calibrationsDir, "calibration-"+hex.EncodeToString(sum[:])[:16]+".json")
}

// LoadOrCalibrate is the calibration stored for key, measured and stored
// where none is. A stored file that does not read is an error naming it, never
// measured over, so a cap never changes without a word.
func LoadOrCalibrate(ctx context.Context, measurer Measurer, cache string, key string) (Calibration, error) {
	path := CalibrationPath(cache, key)

	data, err := os.ReadFile(path)
	if err == nil {
		var stored Calibration

		err = json.Unmarshal(data, &stored)
		if err != nil {
			return Calibration{}, fmt.Errorf("reading calibration %s: %w", path, err)
		}

		if stored.MaxRunes < CapQuantum || stored.MaxRunes%CapQuantum != 0 {
			return Calibration{}, fmt.Errorf("calibration for %s holds no usable rune cap (%d)", key, stored.MaxRunes)
		}

		return stored, nil
	}

	if !errors.Is(err, fs.ErrNotExist) {
		return Calibration{}, fmt.Errorf("reading calibration %s: %w", path, err)
	}

	measured, err := Calibrate(ctx, measurer)
	if err != nil {
		return Calibration{}, err
	}

	encoded, err := json.Marshal(measured)
	if err != nil {
		return Calibration{}, fmt.Errorf("encoding the calibration: %w", err)
	}

	err = safefs.MkdirAll(cache, filepath.Dir(path), 0o700)
	if err != nil {
		return Calibration{}, fmt.Errorf("creating the calibration folder: %w", err)
	}

	err = atomicfile.Write(cache, path, encoded, 0o600)
	if err != nil {
		return Calibration{}, fmt.Errorf("storing the calibration: %w", err)
	}

	return measured, nil
}
