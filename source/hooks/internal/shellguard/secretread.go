package shellguard

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"
	"mvdan.cc/sh/v3/syntax"

	"github.com/fza/agentic-helpers/source/hooks/internal/credfile"
	"github.com/fza/agentic-helpers/source/hooks/internal/pieces"
	"github.com/fza/agentic-helpers/source/hooks/internal/pluginconfig"
	"github.com/fza/agentic-helpers/source/hooks/internal/shellwalk"
)

const (
	reasonCredentialFile = "%s holds a credential (%s); reading it is refused. " +
		"Its lines, those inside a credential withheld:\n%s"
	reasonKeyStore   = "%s holds a credential (a private key store by its path); reading it is refused"
	reasonTooLarge   = "%s: file too large to check; reading it is refused"
	reasonReadUnread = "could not read the call; refusing"
	lineWithheld     = "[withheld]"
	whatKeys         = "secret-named keys %s"
	whatPrivateKey   = "a private key"
	whatStore        = "a credential store by name"
	whatUnscanned    = "a scan that could not finish"
)

// readLimit is the lines a Read returns where it names no limit; copyLimit
// the most lines a refusal hands back for a Bash reader, which returns the
// whole file; lineCut the longest line it hands back, as Read cuts one.
const (
	readLimit = 2000
	copyLimit = 2000
	lineCut   = 2000
)

type readPayload struct {
	Cwd       string `json:"cwd"`
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		Offset       int    `json:"offset"`
		Limit        int    `json:"limit"`
	} `json:"tool_input"`
}

// SecretRead answers one PreToolUse call on Read or NotebookRead: a file
// holding a credential is refused, the refusal handing back the lines the
// read returns with each one inside a credential withheld. A call it cannot
// read refuses, exit 2, as the Bash guard does; a file it cannot open or stat
// it leaves to the tool, which fails on it too.
func SecretRead(ctx context.Context, env Env, streams Streams) (code int) {
	defer func() {
		failure := recover()
		if failure != nil {
			_, _ = fmt.Fprintln(streams.Err, self+": internal failure; refusing")
			code = 2
		}
	}()

	read, err := pluginconfig.ReadWith(env.Settings, env.Getenv)
	if err != nil {
		read = pluginconfig.Defaults()
	}

	if !pieces.ShellGuard.On(read) || read.ShellGuardSecretRead == pluginconfig.ShellGuardOff {
		return 0
	}

	var call readPayload

	err = json.NewDecoder(streams.In).Decode(&call)
	if err != nil {
		_, _ = fmt.Fprintln(streams.Err, self+": "+reasonReadUnread)

		return 2
	}

	named := call.ToolInput.FilePath
	first, last := 1, 0

	if call.ToolName == "NotebookRead" || named == "" {
		named = call.ToolInput.NotebookPath
	} else {
		first = max(call.ToolInput.Offset, 1)
		last = first + cmp(call.ToolInput.Limit, readLimit) - 1
	}

	skips, _ := pluginconfig.SecretReadSkips(read.ShellGuardSecretReadSkip)

	reason := judgeRead(ctx, env, skips, named, call.Cwd, first, last, false)
	if reason == "" {
		return 0
	}

	var reply answer

	reply.HookSpecificOutput.HookEventName = "PreToolUse"
	reply.HookSpecificOutput.PermissionDecision = "deny"
	reply.HookSpecificOutput.PermissionDecisionReason = self + ": " + reason

	err = json.NewEncoder(streams.Out).Encode(reply)
	if err != nil {
		_, _ = fmt.Fprintln(streams.Err, self+": could not answer; refusing")

		return 2
	}

	return 0
}

// cmp is value where it is set, fallback where it is not.
func cmp(value int, fallback int) int {
	if value > 0 {
		return value
	}

	return fallback
}

// judgeRead is the refusal of a read of named from first to last, last 0 for
// the whole file, or "" where it passes. whole says the reader returns the
// whole file whatever the range, as a Bash reader does: a config file too
// large to scan whole is then refused rather than read unjudged.
func judgeRead(ctx context.Context, env Env, skips []pluginconfig.SecretReadSkip, named string, workingDir string,
	first int, last int, whole bool,
) string {
	file := credentialPath(named, workingDir, env.Home)
	if file == "" || secretReadSkipped(env, skips, file) {
		return ""
	}

	info, err := os.Stat(file)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}

	if credfile.BinaryKeyStore(file, physical(env.Home)) || credfile.BinaryKeyStore(file, env.Home) {
		return fmt.Sprintf(reasonKeyStore, named)
	}

	if whole && info.Size() > credfile.ScanLimit && credfile.ConfigType(file) {
		return fmt.Sprintf(reasonTooLarge, named)
	}

	scanned, err := scanned(file, info.Size(), first, last)
	if err != nil {
		return ""
	}

	spans := credfile.Spans(ctx, file, scanned.text, scanned.first)

	lines := scanned.lines()
	if last == 0 {
		last = scanned.first + len(lines) - 1
	}

	if !credfile.Overlaps(spans, first, last) {
		return ""
	}

	return fmt.Sprintf(reasonCredentialFile, named, describeSpans(spans, first, last), withheldCopy(lines, scanned.first, spans, first, last))
}

// credentialPath is the file a read names, absolute and through every
// symlink, or "" where it does not resolve.
func credentialPath(named string, workingDir string, home string) string {
	if named == "" {
		return ""
	}

	target := resolve(named, physical(workingDir), home)
	if target == "" {
		return ""
	}

	actual, err := filepath.EvalSymlinks(target)
	if err != nil {
		return ""
	}

	return actual
}

// secretReadSkipped is whether a `shell_guard_secret_read_skip` entry for the
// session's project names the file.
func secretReadSkipped(env Env, skips []pluginconfig.SecretReadSkip, file string) bool {
	root := projectRoot(env)
	if root == "" {
		return false
	}

	for _, skip := range skips {
		project := skip.Project
		if project == "~" || strings.HasPrefix(project, "~/") {
			project = filepath.Join(env.Home, strings.TrimPrefix(project, "~"))
		}

		if physical(project) != root || !within(file, root) {
			continue
		}

		relative, err := filepath.Rel(root, file)
		if err != nil {
			continue
		}

		for _, glob := range skip.Globs {
			matched, err := doublestar.Match(glob, filepath.ToSlash(relative))
			if err == nil && matched {
				return true
			}
		}
	}

	return false
}

// scannedText is the part of a file spans are found over, its first line's
// number with it.
type scannedText struct {
	text  string
	first int
}

func (part scannedText) lines() []string {
	return strings.Split(strings.ReplaceAll(part.text, "\r\n", "\n"), "\n")
}

// scanned is the whole file up to credfile.ScanLimit; past that, for a range,
// the range and the credfile.Lookback before and after it, and for the whole
// file its first credfile.ScanLimit bytes.
func scanned(file string, size int64, first int, last int) (scannedText, error) {
	opened, err := os.Open(file)
	if err != nil {
		return scannedText{}, fmt.Errorf("opening %s: %w", file, err)
	}
	defer func() { _ = opened.Close() }()

	if size <= credfile.ScanLimit || last == 0 {
		content, err := io.ReadAll(io.LimitReader(opened, credfile.ScanLimit))
		if err != nil {
			return scannedText{}, fmt.Errorf("reading %s: %w", file, err)
		}

		return scannedText{text: strings.TrimSuffix(string(content), "\n"), first: 1}, nil
	}

	reader := bufio.NewReader(opened)

	var before, kept []string

	held := 0
	after := 0
	number := 0

	for after <= credfile.Lookback {
		line, err := reader.ReadString('\n')
		if line == "" && err != nil {
			break
		}

		number++
		line = strings.TrimSuffix(line, "\n")

		switch {
		case number < first:
			before = append(before, line)
			held += len(line) + 1

			for held > credfile.Lookback && len(before) > 0 {
				held -= len(before[0]) + 1
				before = before[1:]
			}
		case number <= last:
			kept = append(kept, line)
		default:
			// A span overlapping the range's end is found only with its own
			// end, which a private key's rule needs.
			kept = append(kept, line)
			after += len(line) + 1
		}

		if err != nil {
			break
		}
	}

	return scannedText{text: strings.Join(append(before, kept...), "\n"), first: first - len(before)}, nil
}

// describeSpans names what the spans overlapping the range hold.
func describeSpans(spans []credfile.Span, first int, last int) string {
	var keys []string

	var what []string

	for _, span := range spans {
		if span.First > last || span.Last < first {
			continue
		}

		var named string

		switch span.Rule {
		case credfile.RuleKeyName:
			if !slices.Contains(keys, span.Key) {
				keys = append(keys, span.Key)
			}

			continue
		case credfile.RuleStore:
			named = whatStore
		case credfile.RulePrivateKey, credfile.RuleAgeKey, credfile.RulePuTTY:
			named = whatPrivateKey
		default:
			named = whatUnscanned
		}

		if !slices.Contains(what, named) {
			what = append(what, named)
		}
	}

	if len(keys) > 0 {
		what = append([]string{fmt.Sprintf(whatKeys, strings.Join(keys, ", "))}, what...)
	}

	return strings.Join(what, "; ")
}

// withheldCopy is the lines from first to last, numbered as Read numbers
// them, each inside a span withheld, at most copyLimit of them.
func withheldCopy(lines []string, numberedFrom int, spans []credfile.Span, first int, last int) string {
	var copied strings.Builder

	shown := 0

	for number := first; number <= last; number++ {
		index := number - numberedFrom
		if index < 0 || index >= len(lines) {
			continue
		}

		if shown == copyLimit {
			fmt.Fprintf(&copied, "... %d more line(s) not shown\n", last-number+1)

			break
		}

		line := lines[index]
		if credfile.Covered(spans, number) {
			line = lineWithheld
		} else if utf8.RuneCountInString(line) > lineCut {
			line = string([]rune(line)[:lineCut])
		}

		fmt.Fprintf(&copied, "%6d\t%s\n", number, line)

		shown++
	}

	return strings.TrimSuffix(copied.String(), "\n")
}

// judgeCredentialReads is the secret-read guard's verdict on a Bash command:
// a plain reader of a file holding a credential, named by a literal path, is
// refused as a Read of the whole file is.
func judgeCredentialReads(ctx context.Context, file *syntax.File, workingDir string, env Env, options Options) Verdict {
	if options.SecretRead == pluginconfig.ShellGuardOff {
		return Verdict{Decision: Allow}
	}

	check := credentialReads{ctx: ctx, env: env, skips: options.SecretReadSkip}
	shellwalk.WalkWith(file, &check, shellwalk.Scripts{Files: env.Files, Home: env.Home, Dir: workingDir})

	if check.reason == "" {
		return Verdict{Decision: Allow}
	}

	return Verdict{Decision: Deny, Reason: check.reason}
}

type credentialReads struct {
	ctx    context.Context
	env    Env
	skips  []pluginconfig.SecretReadSkip
	reason string
}

func (check *credentialReads) Command(call shellwalk.Call) {
	if check.reason != "" || call.Dir == "" {
		return
	}

	words := make([]string, 0, len(call.Args))

	for _, arg := range call.Args {
		value, isLiteral := shellwalk.Literal(arg)
		if !isLiteral || shellwalk.Patterned(arg) {
			return
		}

		words = append(words, value)
	}

	options, isReader := readerTable[words[0]]
	if !isReader {
		return
	}

	files, known := readerFiles(options, words[1:])
	if !known {
		return
	}

	for _, named := range files {
		reason := judgeRead(check.ctx, check.env, check.skips, named, call.Dir, 1, 0, true)
		if reason != "" {
			check.reason = reason

			return
		}
	}
}

func (check *credentialReads) Code(string, []*syntax.Word, string) {}

func (check *credentialReads) Inline(string, []*syntax.Word) {}

func (check *credentialReads) Piped(string) {}

func (check *credentialReads) Unseen(string) {}

func (check *credentialReads) Script(string, string, string, shellwalk.Scripts) {}

func (check *credentialReads) Unread(string, string) {}
