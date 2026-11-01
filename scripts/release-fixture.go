// Command release-fixture writes the fixture of one agentic-helpers release
// under source/hooks/test/fixtures/releases/<version>/: it downloads the
// release's binaries for this machine from GitHub, checks them against the
// release's checksums.txt, drives them through a scripted session in a
// temporary home and checkout, and copies what they wrote with dump.json,
// every read the session's state answers to and what the release printed.
//
// Run it from the repository root before each release, with the version of
// the release before it: go run scripts/release-fixture.go v2.24.0. The
// network is reached here and never in a test; the hop test
// (source/hooks/internal/formatsteps) migrates every committed fixture.
//
// Every path the release wrote that names the temporary folder is written
// with @WORK@ in its place, and the test puts its own folder back.
package main

import (
	"archive/tar"

	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	repository = "fza/agentic-helpers"
	sessionA   = "a1a1a1a1-0000-4000-8000-000000000001"
	sessionB   = "b2b2b2b2-0000-4000-8000-000000000002"
	workMark   = "@WORK@"
	binaryMax  = 256 << 20

	// packedSuffix marks a file the fixture keeps gzipped, which no store
	// file's own name carries: an attachment may end in .gz.
	packedSuffix = ".fixture-gz"
	packAbove    = 64 << 10
)

var (
	errUsage    = errors.New("usage: go run scripts/release-fixture.go v<major>.<minor>.<patch>")
	errChecksum = errors.New("the archive does not match the release's checksums.txt")
	versionForm = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
	filedID     = regexp.MustCompile(`filed (m-[0-9a-z]+)`)
)

// Command is one read of the dump: what ran, as which session, and the JSON
// the release answered.
type Command struct {
	Binary  string          `json:"binary"`
	Args    []string        `json:"args"`
	Session string          `json:"session"`
	Out     json.RawMessage `json:"out"`
}

// Dump is dump.json: the release, the moment its reads ran, the store each
// entry was filed into, and the reads.
type Dump struct {
	Release  string    `json:"release"`
	At       time.Time `json:"at"`
	Entries  []string  `json:"entries"`
	Commands []Command `json:"commands"`
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	err := run(ctx, os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "release-fixture:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) != 1 || !versionForm.MatchString(args[0]) {
		return errUsage
	}

	release := args[0]

	target, err := filepath.Abs(filepath.Join("source", "hooks", "test", "fixtures", "releases", release))
	if err != nil {
		return fmt.Errorf("locating the fixture folder: %w", err)
	}

	_, err = os.Stat(filepath.Join("source", "hooks", "go.mod"))
	if err != nil {
		return fmt.Errorf("run it from the repository root: %w", err)
	}

	work, err := os.MkdirTemp("", "release-fixture-")
	if err != nil {
		return fmt.Errorf("creating the work folder: %w", err)
	}

	defer func() { _ = os.RemoveAll(work) }()

	work, err = filepath.EvalSymlinks(work)
	if err != nil {
		return fmt.Errorf("resolving the work folder: %w", err)
	}

	bin := filepath.Join(work, "bin")

	err = fetch(ctx, release, bin)
	if err != nil {
		return err
	}

	session := &scripted{work: work, bin: bin, repo: filepath.Join(work, "repo"), home: filepath.Join(work, "home")}

	err = session.prepare(ctx)
	if err != nil {
		return err
	}

	err = session.play(ctx)
	if err != nil {
		return err
	}

	dump, err := session.dump(ctx, release)
	if err != nil {
		return err
	}

	return session.write(target, dump)
}

// fetch downloads the release's archive for this machine and checksums.txt,
// checks the one against the other and unpacks the binaries into bin.
func fetch(ctx context.Context, release string, bin string) error {
	version := strings.TrimPrefix(release, "v")
	archive := fmt.Sprintf("agentic-helpers_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	base := fmt.Sprintf("https://github.com/%s/releases/download/%s/", repository, release)

	sums, err := download(ctx, base+"checksums.txt")
	if err != nil {
		return err
	}

	packed, err := download(ctx, base+archive)
	if err != nil {
		return err
	}

	sum := sha256.Sum256(packed)
	if !slices.Contains(strings.Split(string(sums), "\n"), hex.EncodeToString(sum[:])+"  "+archive) {
		return fmt.Errorf("%w: %s", errChecksum, archive)
	}

	return unpack(packed, bin)
}

func download(ctx context.Context, url string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("asking for %s: %w", url, err)
	}

	response, err := (&http.Client{Timeout: 5 * time.Minute}).Do(request)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", url, err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: %s", url, response.Status)
	}

	data, err := io.ReadAll(io.LimitReader(response.Body, binaryMax))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", url, err)
	}

	return data, nil
}

// unpack writes every regular file of the archive whose name is a bare
// agentic- binary into bin, nothing else.
func unpack(packed []byte, bin string) error {
	unzipped, err := gzip.NewReader(bytes.NewReader(packed))
	if err != nil {
		return fmt.Errorf("opening the archive: %w", err)
	}

	err = os.MkdirAll(bin, 0o700)
	if err != nil {
		return fmt.Errorf("creating %s: %w", bin, err)
	}

	archive := tar.NewReader(unzipped)

	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("reading the archive: %w", err)
		}

		name := header.Name
		if header.Typeflag != tar.TypeReg || strings.ContainsAny(name, `/\`) || !strings.HasPrefix(name, "agentic-") {
			continue
		}

		data, err := io.ReadAll(io.LimitReader(archive, binaryMax))
		if err != nil {
			return fmt.Errorf("reading %s: %w", name, err)
		}

		err = os.WriteFile(filepath.Join(bin, name), data, 0o700)
		if err != nil {
			return fmt.Errorf("writing %s: %w", name, err)
		}
	}
}

// scripted is the session the release is driven through: its binaries, a
// git checkout and a home of its own.
type scripted struct {
	work string
	bin  string
	repo string
	home string
	ids  map[string]string
}

func (session *scripted) prepare(ctx context.Context) error {
	files := map[string]string{
		"README.md":                  "# app\n",
		"internal/gate/gate.go":      "package gate\n",
		"notes/attached.txt":         "what the gate reads, kept with the entry\n",
		"scripts/count-lines.sh":     "#!/bin/sh\n# count-lines: print the line count of each file named\nwc -l \"$@\"\n",
		"scripts/list-todos.sh":      "#!/bin/sh\n# list-todos: print every TODO line below the folder named\ngrep -rn TODO \"${1:-.}\"\n",
		"../home/.config/.keep":      "",
		"../home/.claude/.keep":      "",
		"../home/.local/share/.keep": "",
	}

	for name, content := range files {
		path := filepath.Join(session.repo, name)

		err := os.MkdirAll(filepath.Dir(path), 0o700)
		if err != nil {
			return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
		}

		err = os.WriteFile(path, []byte(content), 0o600)
		if err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
	}

	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"add", "-A"},
		{"-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "commit", "-q", "-m", "chore: start"},
	} {
		command := exec.CommandContext(ctx, "git", args...)
		command.Dir = session.repo
		command.Env = session.env(sessionA)

		output, err := command.CombinedOutput()
		if err != nil {
			return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, output)
		}
	}

	return nil
}

// env is the environment every binary runs in: the scratch home, the
// session, every plugin option reaching a network off, and nothing of the
// environment the script runs in but PATH.
func (session *scripted) env(id string) []string {
	executable, _ := os.Executable()

	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + session.home,
		"TMPDIR=" + filepath.Join(session.work, "tmp"),
		"TZ=UTC",
		"LANG=C",
		"CLAUDE_PROJECT_DIR=" + session.repo,
		"CLAUDE_CODE_SESSION_ID=" + id,
		"AGENTIC_MEMORY_PROCESS_NAME=" + filepath.Base(executable),
		"CLAUDE_PLUGIN_OPTION_EMBEDDING_PROVIDER=off",
		"CLAUDE_PLUGIN_OPTION_UPDATE_NOTICE=false",
		"CLAUDE_PLUGIN_OPTION_MEMORY_TURNS_OFF_AUTO=false",
	}
}

// call runs one binary of the release as the session id, stdin fed in.
func (session *scripted) call(ctx context.Context, id string, stdin string, binary string, args ...string) (string, error) {
	err := os.MkdirAll(filepath.Join(session.work, "tmp"), 0o700)
	if err != nil {
		return "", fmt.Errorf("creating the scratch folder: %w", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	command := exec.CommandContext(runCtx, filepath.Join(session.bin, binary), args...)
	command.Dir = session.repo
	command.Env = session.env(id)
	command.Stdin = strings.NewReader(stdin)

	var out, errOut bytes.Buffer
	command.Stdout, command.Stderr = &out, &errOut

	err = command.Run()
	if err != nil {
		return out.String(), fmt.Errorf("%s %s: %w: %s%s", binary, strings.Join(args, " "), err, out.String(), errOut.String())
	}

	return out.String(), nil
}

func (session *scripted) file(ctx context.Context, id string, key string, body string, args ...string) error {
	out, err := session.call(ctx, id, body, "agentic-memory", append([]string{"new"}, args...)...)
	if err != nil {
		return err
	}

	found := filedID.FindStringSubmatch(out)
	if found == nil {
		return fmt.Errorf("filing %s answered no id: %s", key, out)
	}

	session.ids[key] = found[1]

	return nil
}

func hook(payload map[string]any) string {
	data, _ := json.Marshal(payload)

	return string(data)
}

// play is the scripted session: two seats, an entry of every kind, a todo's
// items and due time, an attachment, links, a retire and a supersede, each
// seat's state, the hooks that keep per-session state, a tool added and
// taken, and a shell-guard ask.
func (session *scripted) play(ctx context.Context) error {
	session.ids = map[string]string{}

	start := func(id string) step {
		return step{id: id, stdin: hook(map[string]any{"session_id": id, "source": "startup", "cwd": session.repo}),
			binary: "agentic-memory", args: []string{"session-start"}}
	}

	before := []step{
		start(sessionA),
		{id: sessionA, binary: "agentic-memory", args: []string{"claim", "main"}},
	}

	err := session.steps(ctx, before)
	if err != nil {
		return err
	}

	for _, entry := range []struct {
		key  string
		body string
		args []string
	}{
		{key: "note", body: "the gate reads internal/gate/gate.go for Bash calls only",
			args: []string{"--title", "Gate reads Bash calls only today", "--topic", "gate", "--about", "README.md"}},
		{key: "rule", body: "the owner allows commits on main without asking",
			args: []string{"--title", "Owner allows commits on main", "--topic", "grants", "--kind", "rule", "--from", "user"}},
		{key: "trap", body: "a push of the release branch by hand skips the release workflow",
			args: []string{"--title", "Never push the release branch by hand", "--topic", "release", "--kind", "trap",
				"--when", "command:git push origin release"}},
		{key: "todo", body: "port the darwin waiter to linux",
			args: []string{"--title", "Port the waiter to Linux", "--topic", "gate", "--kind", "todo",
				"--item", "read the darwin waiter", "--item", "write the linux waiter", "--item", "test both", "--due", "2027-01-01"}},
		{key: "attached", body: "the attached notes hold what the gate reads",
			args: []string{"--title", "Gate notes kept as a file", "--topic", "gate", "--attach", "notes/attached.txt"}},
		{key: "retired", body: "an early guess about the gate",
			args: []string{"--title", "Gate guess later proven wrong", "--topic", "gate"}},
		{key: "old", body: "the release takes two steps",
			args: []string{"--title", "Release takes two manual steps", "--topic", "release"}},
		{key: "new", body: "the release takes one step now",
			args: []string{"--title", "Release takes one manual step", "--topic", "release"}},
	} {
		err = session.file(ctx, sessionA, entry.key, entry.body, entry.args...)
		if err != nil {
			return err
		}
	}

	item, err := session.firstItem(ctx, session.ids["todo"])
	if err != nil {
		return err
	}

	during := []step{
		{id: sessionA, binary: "agentic-memory", args: []string{"ack", session.ids["todo"] + "#" + item, "--done"}},
		{id: sessionA, binary: "agentic-memory", args: []string{"due", session.ids["note"], "2027-02-01"}},
		{id: sessionA, binary: "agentic-memory", args: []string{"link", session.ids["note"], session.ids["rule"]}},
		{id: sessionA, binary: "agentic-memory", args: []string{"tag", session.ids["note"], "hooks"}},
		{id: sessionA, binary: "agentic-memory", args: []string{"retitle", session.ids["attached"], "Gate notes kept as one file"}},
		{id: sessionA, binary: "agentic-memory", args: []string{"retire", session.ids["retired"], "--because", "proven wrong"}},
		{id: sessionA, binary: "agentic-memory", args: []string{"supersede", session.ids["old"], "--by", session.ids["new"],
			"--because", "the release lost a step"}},
		{id: sessionA, binary: "agentic-memory", args: []string{"ack", "state", "--add", "gate fix halfway; next: wire the MCP reads, then docs"}},
		{id: sessionA, stdin: hook(map[string]any{"session_id": sessionA, "prompt": "go on with the gate", "cwd": session.repo}),
			binary: "agentic-memory", args: []string{"prompt"}},
		{id: sessionA, binary: "agentic-tools", args: []string{"add", "count-lines", "--from", "scripts/count-lines.sh",
			"--summary", "print the line count of each file named", "--usage", "count-lines <file>..."}},
		{id: sessionA, binary: "agentic-tools", args: []string{"add", "list-todos", "--from", "scripts/list-todos.sh",
			"--summary", "print every TODO line below a folder", "--usage", "list-todos [<folder>]"}},
		{id: sessionA, binary: "agentic-tools", args: []string{"take", "count-lines"}},
		{id: sessionA, stdin: hook(map[string]any{"session_id": sessionA, "tool_name": "Bash", "cwd": session.repo,
			"permission_mode": "default", "tool_input": map[string]any{"command": "rm -rf /usr/local/share/fixture-only"}}),
			binary: "agentic-shell-guard", args: nil, quiet: true},
		{id: sessionA, binary: "agentic-memory", args: []string{"release"}},
		start(sessionB),
		{id: sessionB, binary: "agentic-memory", args: []string{"claim", "drive"}},
	}

	err = session.steps(ctx, during)
	if err != nil {
		return err
	}

	err = session.file(ctx, sessionB, "drive", "drive syncs the release notes for main",
		"--title", "Drive syncs the release notes", "--topic", "release", "--for", "main")
	if err != nil {
		return err
	}

	return session.steps(ctx, []step{
		{id: sessionB, binary: "agentic-memory", args: []string{"ack", "state", "--add", "drive sync done; next: tell main"}},
		{id: sessionB, binary: "agentic-memory", args: []string{"release"}},
		{id: sessionA, binary: "agentic-memory", args: []string{"claim", "main"}},
	})
}

// firstItem is the id of the todo's first item, as show --json lists it.
func (session *scripted) firstItem(ctx context.Context, todo string) (string, error) {
	out, err := session.call(ctx, sessionA, "", "agentic-memory", "show", "--json", "--no-heat", todo)
	if err != nil {
		return "", err
	}

	var shown struct {
		Entries []struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
		} `json:"entries"`
	}

	err = json.Unmarshal([]byte(out), &shown)
	if err != nil || len(shown.Entries) == 0 || len(shown.Entries[0].Items) == 0 {
		return "", fmt.Errorf("show %s listed no item: %s", todo, out)
	}

	return shown.Entries[0].Items[0].ID, nil
}

type step struct {
	id     string
	stdin  string
	binary string
	args   []string
	quiet  bool
}

func (session *scripted) steps(ctx context.Context, steps []step) error {
	for _, held := range steps {
		_, err := session.call(ctx, held.id, held.stdin, held.binary, held.args...)
		if err != nil && !held.quiet {
			return err
		}
	}

	return nil
}

// dump runs the reads dump.json records: each seat's brief and every entry
// shown by the seat that holds it, and the tool library's list.
func (session *scripted) dump(ctx context.Context, release string) (Dump, error) {
	held := Dump{Release: release, At: time.Now().UTC().Truncate(time.Second)}

	for key := range session.ids {
		held.Entries = append(held.Entries, session.ids[key])
	}

	slices.Sort(held.Entries)

	reads := []Command{
		{Binary: "agentic-memory", Args: []string{"brief", "--json", "--again"}, Session: sessionA},
		{Binary: "agentic-memory", Args: append([]string{"show", "--json", "--no-heat", "--with-superseded", "--all-items", "--again"}, held.Entries...),
			Session: sessionA},
		{Binary: "agentic-tools", Args: []string{"list", "--json", "--again"}, Session: sessionA},
	}

	for _, read := range reads {
		out, err := session.call(ctx, read.Session, "", read.Binary, read.Args...)
		if err != nil {
			return held, err
		}

		if !json.Valid([]byte(out)) {
			return held, fmt.Errorf("%s %s answered no JSON: %s", read.Binary, strings.Join(read.Args, " "), out)
		}

		read.Out = json.RawMessage(strings.ReplaceAll(strings.TrimSpace(out), session.work, workMark))
		held.Commands = append(held.Commands, read)
	}

	return held, nil
}

// write replaces the fixture folder with what the session left: the home's
// data and cache, the checkout's files without its git folder, which a
// repository cannot hold, and dump.json.
func (session *scripted) write(target string, dump Dump) error {
	err := os.RemoveAll(target)
	if err != nil {
		return fmt.Errorf("clearing %s: %w", target, err)
	}

	err = os.RemoveAll(filepath.Join(session.repo, ".git"))
	if err != nil {
		return fmt.Errorf("clearing the checkout's git folder: %w", err)
	}

	for from, to := range map[string]string{
		filepath.Join(session.home, ".local", "share", "agentic-helpers"): "data",
		filepath.Join(session.home, ".cache", "agentic-helpers"):          "cache",
		session.repo: "checkout",
	} {
		err = session.copyTree(from, filepath.Join(target, to))
		if err != nil {
			return err
		}
	}

	data, err := json.MarshalIndent(dump, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding dump.json: %w", err)
	}

	return os.WriteFile(filepath.Join(target, "dump.json"), append(data, '\n'), 0o644)
}

// copyTree copies every regular file below from, a text file's mention of
// the work folder written as @WORK@; a folder the release never wrote is
// skipped.
func (session *scripted) copyTree(from string, to string) error {
	return filepath.WalkDir(from, func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == from {
			return nil
		}

		if err != nil || !entry.Type().IsRegular() {
			return err
		}

		relative, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		// A file in a binary format, a slot file among them, keeps the work
		// folder where it names it: its records fix their lengths.
		if isText(data) {
			data = bytes.ReplaceAll(data, []byte(session.work), []byte(workMark))
		}

		target := filepath.Join(to, relative)

		// A slot file is megabytes of empty slots; packed, a fixture stays
		// small.
		if len(data) > packAbove {
			target += packedSuffix

			data, err = pack(data)
			if err != nil {
				return err
			}
		}

		err = os.MkdirAll(filepath.Dir(target), 0o755)
		if err != nil {
			return err
		}

		return os.WriteFile(target, data, 0o644)
	})
}

func pack(data []byte) ([]byte, error) {
	var packed bytes.Buffer

	writer, err := gzip.NewWriterLevel(&packed, gzip.BestCompression)
	if err != nil {
		return nil, fmt.Errorf("packing: %w", err)
	}

	_, err = writer.Write(data)
	if err != nil {
		return nil, fmt.Errorf("packing: %w", err)
	}

	err = writer.Close()
	if err != nil {
		return nil, fmt.Errorf("packing: %w", err)
	}

	return packed.Bytes(), nil
}

func isText(data []byte) bool {
	return !bytes.ContainsRune(data, 0) && utf8.Valid(data)
}
