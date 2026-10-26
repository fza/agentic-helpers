package test

import (
	"bytes"
	"context"
	"errors"
	"hash/fnv"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
)

// Git answers the questions agentic-memory asks git: whether the directory is
// a work tree, the checkout's top level, its HEAD commit and its branch.
type Git struct {
	Top    string
	Commit string
	Branch string
	// OutsideWorkTree answers as git does in a directory that is no work tree.
	OutsideWorkTree bool
	// Ignored lists the paths `git check-ignore` reports as ignored, and
	// ExcludesFile answers `core.excludesFile` from the global config.
	Ignored      []string
	ExcludesFile string
	// Branches maps each local branch to the revisions its tip is an ancestor
	// of; OriginHead answers `symbolic-ref refs/remotes/origin/HEAD`.
	Branches   map[string][]string
	OriginHead string
}

func (fake *Git) Output(_ context.Context, _ string, args ...string) (string, error) {
	switch strings.Join(args, " ") {
	case "rev-parse --is-inside-work-tree":
		if fake.OutsideWorkTree {
			return "", errors.New("fatal: not a git repository")
		}

		return "true\n", nil
	case "rev-parse --show-toplevel":
		return fake.Top + "\n", nil
	case "rev-parse --short=8 HEAD":
		return fake.Commit + "\n", nil
	case "rev-parse --abbrev-ref HEAD":
		return fake.Branch + "\n", nil
	}

	if strings.Join(args, " ") == "config --global --get core.excludesFile" {
		if fake.ExcludesFile == "" {
			return "", errors.New("exit status 1")
		}

		return fake.ExcludesFile + "\n", nil
	}

	answer, answered, err := fake.branches(args)
	if answered {
		return answer, err
	}

	if len(args) == 3 && args[0] == "check-ignore" && args[1] == "-q" {
		if slices.Contains(fake.Ignored, args[2]) {
			return "", nil
		}

		return "", errors.New("exit status 1")
	}

	return "", errors.New("unexpected git call")
}

// branches answers the calls a `merged:` end condition makes, and reports
// whether the call was one of them.
func (fake *Git) branches(args []string) (string, bool, error) {
	switch {
	case slices.Equal(args, []string{"symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"}):
		if fake.OriginHead == "" {
			return "", true, errors.New("exit status 1")
		}

		return fake.OriginHead + "\n", true, nil
	case len(args) == 4 && args[0] == "rev-parse" && args[1] == "--verify" && args[2] == "--quiet":
		_, exists := fake.Branches[strings.TrimPrefix(args[3], "refs/heads/")]
		if !exists {
			return "", true, errors.New("exit status 1")
		}

		return "0123456789abcdef\n", true, nil
	case len(args) == 4 && args[0] == "merge-base" && args[1] == "--is-ancestor":
		if !slices.Contains(fake.Branches[strings.TrimPrefix(args[2], "refs/heads/")], args[3]) {
			return "", true, errors.New("exit status 1")
		}

		return "", true, nil
	}

	return "", false, nil
}

// Embedder maps a text to a bag of its words hashed into a small vector, so
// texts sharing words score close and the result is deterministic.
type Embedder struct {
	Err   error
	mutex sync.Mutex
	calls int
	// texts is every text it was asked to embed.
	texts []string
}

const embedDims = 64

func (fake *Embedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	fake.mutex.Lock()
	fake.calls++
	fake.texts = append(fake.texts, texts...)
	fake.mutex.Unlock()

	if fake.Err != nil {
		return nil, fake.Err
	}

	vectors := make([][]float32, 0, len(texts))
	for _, text := range texts {
		vectors = append(vectors, bagOfWords(text))
	}

	return vectors, nil
}

// Texts is every text the embedder was asked to embed.
func (fake *Embedder) Texts() []string {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()

	return append([]string(nil), fake.texts...)
}

func (fake *Embedder) Calls() int {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()

	return fake.calls
}

func (fake *Embedder) Fingerprint() string {
	return "fake/64"
}

func bagOfWords(text string) []float32 {
	vector := make([]float32, embedDims)

	for _, word := range strings.Fields(strings.ToLower(text)) {
		word = strings.Trim(word, ".,:;()'\"`")
		if len(word) < 3 || word == "entry" {
			continue
		}

		hash := fnv.New32a()
		_, _ = hash.Write([]byte(word))
		vector[hash.Sum32()%embedDims]++
	}

	norm := 0.0
	for _, value := range vector {
		norm += float64(value * value)
	}

	if norm > 0 {
		scale := float32(1 / math.Sqrt(norm))
		for index := range vector {
			vector[index] *= scale
		}
	}

	return vector
}

// Factory hands out the fake, or refuses like an unconfigured provider.
func (fake *Embedder) Factory(configured bool) func(embed.Config) (embed.Embedder, error) {
	return func(embed.Config) (embed.Embedder, error) {
		if !configured {
			return nil, embed.ErrNotConfigured
		}

		return fake, nil
	}
}

type Clock struct {
	mutex sync.Mutex
	now   time.Time
}

func NewClock(start time.Time) *Clock {
	return &Clock{now: start}
}

func (clock *Clock) Now() time.Time {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()

	clock.now = clock.now.Add(time.Second)

	return clock.now
}

func (clock *Clock) Advance(by time.Duration) {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()

	clock.now = clock.now.Add(by)
}

// Random is an endless deterministic byte stream for entry ids.
func Random() *bytes.Reader {
	data := make([]byte, 1<<16)
	state := uint32(2463534242)

	for index := range data {
		state ^= state << 13
		state ^= state >> 17
		state ^= state << 5
		data[index] = byte(state)
	}

	return bytes.NewReader(data)
}

// Graph stands in for `sdd export`: it answers Out, or fails with Err, and
// counts the calls.
type Graph struct {
	Out   string
	Err   error
	mutex sync.Mutex
	calls int
}

func (fake *Graph) Export(context.Context, string) ([]byte, error) {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()

	fake.calls++

	if fake.Err != nil {
		return nil, fake.Err
	}

	return []byte(fake.Out), nil
}

func (fake *Graph) Calls() int {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()

	return fake.calls
}
