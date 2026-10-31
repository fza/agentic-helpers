package embed_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

// limited is an embedder of provider behind an endpoint enforcing limit, and
// the endpoint's record.
func limited(t *testing.T, provider string, limit *test.TokenLimit, config embed.Config) (embed.Templated, *test.Endpoint) {
	t.Helper()

	fake := &test.Endpoint{Answer: limit.Answer}
	config.Provider, config.Model = provider, "model"

	raw, err := embed.New(config, fake.Client(t))
	if err != nil {
		t.Fatalf("the embedder should be created: %v", err)
	}

	return embed.Template(raw, config), fake
}

func storedCalibration(t *testing.T, cache string, embedder embed.Templated, endpoint string, path string) embed.Calibration {
	t.Helper()

	data, err := os.ReadFile(embed.CalibrationPath(cache, embed.CalibrationKey(embedder.Fingerprint(), endpoint, path)))
	if err != nil {
		t.Fatalf("the calibration should be stored under its key: %v", err)
	}

	var stored embed.Calibration

	err = json.Unmarshal(data, &stored)
	if err != nil {
		t.Fatalf("the stored calibration should be JSON: %v", err)
	}

	return stored
}

func TestCalibrationMeasuresTheLimitEachWayAProviderAnswers(t *testing.T) {
	cases := []struct {
		name               string
		limit              int
		refuse, unreported bool
		method             string
		tokens             int
		maxRunes           int
	}{
		// 2048 tokens at the CJK probe's token a rune, less the margin: 1843,
		// rounded down to whole 64-rune steps.
		{name: "silent truncation plateaus", limit: 2048, method: "plateau", tokens: 2048, maxRunes: 1792},
		{name: "a refusal is bisected", limit: 2048, refuse: true, method: "bisect", maxRunes: 1792},
		{name: "no ceiling within the overflow", limit: 100000, method: "unbounded", tokens: 5000, maxRunes: 4480},
		{name: "no count reported", limit: 2048, unreported: true, method: "unreported", maxRunes: embed.FallbackRunes},
	}

	for _, provider := range []string{"openai", "ollama"} {
		for _, tc := range cases {
			t.Run(provider+"/"+tc.name, func(t *testing.T) {
				limit := test.TokenLimit{Limit: tc.limit, Refuse: tc.refuse, Unreported: tc.unreported}
				cache := t.TempDir()
				embedder, fake := limited(t, provider, &limit, embed.Config{})

				maxRunes, err := embedder.MaxRunes(t.Context(), cache)
				if err != nil {
					t.Fatalf("calibrating should succeed: %v", err)
				}

				if maxRunes != tc.maxRunes {
					t.Errorf("the cap should follow the limit and the worst ratio, got %d", maxRunes)
				}

				endpoint := strings.TrimSuffix(strings.TrimSuffix(fake.Requests[0].Address, "/v1/embeddings"), "/api/embed")
				path := strings.TrimPrefix(fake.Requests[0].Address, endpoint)
				stored := storedCalibration(t, cache, embedder, endpoint, path)

				if stored.Method != tc.method || stored.MaxRunes != tc.maxRunes {
					t.Errorf("the stored calibration should name how the limit was found, got %+v", stored)
				}

				if tc.tokens > 0 && stored.LimitTokens != tc.tokens {
					t.Errorf("the stored limit should be the count the overflow probe reported, got %d", stored.LimitTokens)
				}

				if tc.method == "bisect" && (stored.LimitTokens > limit.Limit || stored.LimitTokens < limit.Limit-16) {
					t.Errorf("bisection should close in on the refused limit within one step, got %d", stored.LimitTokens)
				}
			})
		}
	}
}

func TestCalibrationProbesLatinThenCJK(t *testing.T) {
	limit := test.TokenLimit{Limit: 100000}
	embedder, _ := limited(t, "openai", &limit, embed.Config{})

	_, err := embedder.MaxRunes(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("calibrating should succeed: %v", err)
	}

	inputs := limit.Inputs()
	if len(inputs) < 3 || utf8.RuneCountInString(inputs[0]) != 1000 || utf8.RuneCountInString(inputs[1]) != 1000 ||
		utf8.RuneCountInString(inputs[2]) != 20000 {
		t.Fatalf("calibration should send a 1000-rune, a 1000-rune and a 20000-rune probe, got %d inputs", len(inputs))
	}

	if strings.Trim(inputs[0], "word ") != "" || strings.Trim(inputs[1], "字") != "" {
		t.Errorf("the first probe should be Latin text and the second CJK")
	}
}

func TestCalibrationGoesThroughTheDocumentTemplate(t *testing.T) {
	limit := test.TokenLimit{Limit: 100000}
	embedder, _ := limited(t, "openai", &limit, embed.Config{DocumentTemplate: "text: {text}", QueryTemplate: "query: {text}"})

	_, err := embedder.MaxRunes(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("calibrating should succeed: %v", err)
	}

	if !strings.HasPrefix(limit.Inputs()[0], "text: word") {
		t.Errorf("a probe should be measured as a document is sent, got %.20q", limit.Inputs()[0])
	}
}

func TestACalibrationIsReusedPerEndpointAndMeasuredAgainOnAChange(t *testing.T) {
	cache := t.TempDir()
	limit := test.TokenLimit{Limit: 2048}
	base := embed.Config{Endpoint: "http://127.0.0.1:1235"}
	moved := embed.Config{Endpoint: "http://127.0.0.1:9999"}
	gateway := embed.Config{Endpoint: "http://127.0.0.1:1235", Path: "/v3/compat/embeddings"}

	first, firstFake := limited(t, "openai", &limit, base)

	_, err := first.MaxRunes(t.Context(), cache)
	if err != nil || len(firstFake.Requests) == 0 {
		t.Fatalf("the first fill should calibrate: %v", err)
	}

	again, againFake := limited(t, "openai", &limit, base)

	maxRunes, err := again.MaxRunes(t.Context(), cache)
	if err != nil || maxRunes != 1792 || len(againFake.Requests) != 0 {
		t.Errorf("the same endpoint should reuse its stored calibration without a request, got %d after %d requests: %v",
			maxRunes, len(againFake.Requests), err)
	}

	for name, config := range map[string]embed.Config{"another endpoint": moved, "another path": gateway} {
		t.Run(name, func(t *testing.T) {
			changed, changedFake := limited(t, "openai", &limit, config)

			_, err := changed.MaxRunes(t.Context(), cache)
			if err != nil || len(changedFake.Requests) == 0 {
				t.Errorf("a changed route should be measured again: %v", err)
			}

			if changed.Fingerprint() != first.Fingerprint() {
				t.Error("a changed route should keep the vector fingerprint")
			}
		})
	}
}

func TestAMalformedCalibrationIsAnErrorNeverMeasuredOver(t *testing.T) {
	cases := []struct {
		name, content, says string
	}{
		{name: "not JSON", content: "{", says: "reading calibration "},
		{name: "no cap", content: `{"method":"plateau","max_runes":0}`, says: "holds no usable rune cap (0)"},
		{name: "a cap off the step", content: `{"max_runes":1000}`, says: "holds no usable rune cap (1000)"},
	}

	for _, tc := range cases {
		name, content := tc.name, tc.content
		t.Run(name, func(t *testing.T) {
			cache := t.TempDir()
			limit := test.TokenLimit{Limit: 2048}
			embedder, fake := limited(t, "openai", &limit, embed.Config{Endpoint: "http://127.0.0.1:1235"})

			path := embed.CalibrationPath(cache, embed.CalibrationKey(embedder.Fingerprint(), "http://127.0.0.1:1235", "/v1/embeddings"))
			test.WriteFile(t, path, content)

			_, err := embedder.MaxRunes(t.Context(), cache)
			if err == nil || !strings.Contains(err.Error(), tc.says) {
				t.Errorf("a calibration that does not read should be an error saying what is wrong, got %v", err)
			}

			if tc.name == "not JSON" && !strings.Contains(err.Error(), path) {
				t.Errorf("a calibration that does not decode should be named by its file, got %v", err)
			}

			if len(fake.Requests) != 0 {
				t.Errorf("a calibration that does not read should never be measured over, sent %d requests", len(fake.Requests))
			}

			kept, _ := os.ReadFile(path)
			if string(kept) != content {
				t.Error("a calibration that does not read should be left as it is")
			}
		})
	}
}

func TestAFailedProbeIsAnErrorAndStoresNothing(t *testing.T) {
	cases := []struct {
		name    string
		answer  func(request test.EmbedRequest) (int, string)
		message string
	}{
		{name: "the latin probe", answer: func(test.EmbedRequest) (int, string) { return http.StatusInternalServerError, "" }, message: "calibration latin probe: "},
		{name: "the cjk probe", answer: func(request test.EmbedRequest) (int, string) {
			if strings.Contains(request.Inputs[0], "字") {
				return http.StatusInternalServerError, ""
			}

			return (&test.TokenLimit{Limit: 100000}).Answer(request)
		}, message: "calibration cjk probe: "},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cache := t.TempDir()
			fake := &test.Endpoint{Answer: tc.answer}

			raw, err := embed.New(embed.Config{Provider: "openai", Model: "model"}, fake.Client(t))
			if err != nil {
				t.Fatalf("the embedder should be created: %v", err)
			}

			_, err = embed.Template(raw, embed.Config{}).MaxRunes(t.Context(), cache)
			if err == nil || !strings.HasPrefix(err.Error(), tc.message) {
				t.Errorf("a failed probe should be an error naming it, got %v", err)
			}

			stored, _ := filepath.Glob(filepath.Join(cache, "*", "*"))
			if len(stored) != 0 {
				t.Errorf("a failed calibration should store nothing, found %q", stored)
			}
		})
	}
}

func TestALimitLeavingNoRoomIsAnError(t *testing.T) {
	// The probes of a thousand runes count as they read; anything longer is
	// cut to 60 tokens without a word.
	fake := &test.Endpoint{Answer: func(request test.EmbedRequest) (int, string) {
		tokens := test.Tokens(request.Inputs[0])
		if utf8.RuneCountInString(request.Inputs[0]) > 1000 {
			tokens = 60
		}

		return http.StatusOK, test.EncodeCounted(request, [][]float32{{1, 0}}, tokens)
	}}

	raw, err := embed.New(embed.Config{Provider: "openai", Model: "model"}, fake.Client(t))
	if err != nil {
		t.Fatalf("the embedder should be created: %v", err)
	}

	_, err = embed.Template(raw, embed.Config{}).MaxRunes(t.Context(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "calibrated limit of 60 tokens leaves no room for a 64-rune cap") {
		t.Errorf("a limit too small for one step should be an error, got %v", err)
	}
}

func TestAnEmbedderThatCannotMeasureGetsTheFallbackCap(t *testing.T) {
	embedder := embed.Template(&recording{fingerprint: "fake/2"}, embed.Config{})

	maxRunes, err := embedder.MaxRunes(t.Context(), t.TempDir())
	if err != nil || maxRunes != embed.FallbackRunes {
		t.Errorf("an embedder reporting no count should get the fallback cap, got %d: %v", maxRunes, err)
	}
}

func TestQuantiseRoundsDownToWholeSteps(t *testing.T) {
	cases := map[int]int{0: 0, 63: 0, 64: 64, 1843: 1792, 1855: 1792, 1856: 1856}

	for runes, want := range cases {
		if embed.Quantise(runes) != want {
			t.Errorf("a cap should round down to a whole 64-rune step, %d gave %d", runes, embed.Quantise(runes))
		}
	}
}
