package test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/embed"
	"github.com/fza/agentic-helpers/source/hooks/internal/process"
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
	// Unborn answers as git does in a checkout without a commit: HEAD names
	// no revision.
	Unborn bool
	// Log answers `log --oneline -n <n> <range>` by its range.
	Log map[string]string
	// Tracked lists the paths `ls-files -- <path>` names as tracked.
	// ListOutsideRepository answers `ls-files` as git does outside a
	// repository, ListFails as git failing any other way.
	Tracked               []string
	ListOutsideRepository bool
	ListFails             bool
	// Before runs ahead of every call it answers, the way a file changes on
	// disk while git runs.
	Before func(args []string)
	// Remotes answers `config --get-regexp ^remote\..*\.url$`, a line per
	// remote as git prints it; empty answers as git does with no remote.
	Remotes string

	mutex sync.Mutex
	calls [][]string
}

// SwitchBranch checks out another branch while a call may be running.
func (fake *Git) SwitchBranch(branch string) {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()

	fake.Branch = branch
}

// Calls is every call git was asked, its arguments each.
func (fake *Git) Calls() [][]string {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()

	return slices.Clone(fake.calls)
}

func (fake *Git) Output(_ context.Context, _ string, args ...string) (string, error) {
	if fake.Before != nil {
		fake.Before(args)
	}

	fake.mutex.Lock()
	fake.calls = append(fake.calls, slices.Clone(args))
	branch := fake.Branch
	fake.mutex.Unlock()

	switch strings.Join(args, " ") {
	case "rev-parse --is-inside-work-tree --show-toplevel HEAD --abbrev-ref HEAD":
		if fake.OutsideWorkTree {
			return "", errors.New("fatal: not a git repository")
		}

		if fake.Unborn {
			return "", errors.New("fatal: ambiguous argument 'HEAD'")
		}

		return "true\n" + fake.Top + "\n" + fake.Commit + "\n" + branch + "\n", nil
	case "rev-parse --short=8 HEAD", "rev-parse --abbrev-ref HEAD":
		if fake.Unborn {
			return "", errors.New("fatal: ambiguous argument 'HEAD'")
		}
	}

	switch strings.Join(args, " ") {
	case "rev-parse --is-inside-work-tree":
		if fake.OutsideWorkTree {
			return "", errors.New("fatal: not a git repository")
		}

		return "true\n", nil
	case "rev-parse --show-toplevel":
		if fake.OutsideWorkTree {
			return "", errors.New("fatal: not a git repository")
		}

		return fake.Top + "\n", nil
	case "rev-parse --short=8 HEAD":
		return fake.Commit + "\n", nil
	case "rev-parse --abbrev-ref HEAD":
		return branch + "\n", nil
	}

	if strings.Join(args, " ") == `config --get-regexp ^remote\..*\.url$` {
		if fake.Remotes == "" {
			return "", errors.New("exit status 1")
		}

		return fake.Remotes, nil
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

	if len(args) == 5 && args[0] == "log" && args[1] == "--oneline" && args[2] == "-n" {
		answer, found := fake.Log[args[4]]
		if !found {
			return "", errors.New("fatal: bad revision")
		}

		return answer, nil
	}

	if len(args) == 3 && args[0] == "ls-files" && args[1] == "--" {
		switch {
		case fake.ListOutsideRepository:
			return "", fmt.Errorf("running git %v: %w", args, process.ErrNotARepository)
		case fake.ListFails:
			return "", errors.New("fatal: unable to read index file")
		case slices.Contains(fake.Tracked, args[2]):
			return args[2] + "\n", nil
		}

		return "", nil
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
	Err error
	// Answering, where set, lets that many calls answer before Err applies,
	// as an embedder failing partway through a fill.
	Answering int
	mutex     sync.Mutex
	calls     int
	// texts is every text it was asked to embed.
	texts []string
}

const embedDims = 64

func (fake *Embedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	fake.mutex.Lock()
	fake.calls++
	fake.texts = append(fake.texts, texts...)
	answered := fake.calls <= fake.Answering
	fake.mutex.Unlock()

	if fake.Err != nil && !answered {
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

// Space is the fingerprint memory and the tool library file the fake's
// vectors under, with no template set.
func (fake *Embedder) Space() string {
	return embed.Template(fake, embed.Config{}).Fingerprint()
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
	// held stops the clock moving a second per read, so what a test pins
	// does not hang on how often the code under test reads the time.
	held bool
}

func NewClock(start time.Time) *Clock {
	return &Clock{now: start}
}

func (clock *Clock) Now() time.Time {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()

	if !clock.held {
		clock.now = clock.now.Add(time.Second)
	}

	return clock.now
}

// Hold stops the clock moving on its own: from then on only Advance moves it.
func (clock *Clock) Hold() {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()

	clock.held = true
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
