package grounding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/fza/agentic-helpers/source/hooks/internal/hookio"
)

// A capture may take its body from a file staged through
// `mcp__sdd__stage_attachment`, named by `bodyHandle`. sdd's staging folder is
// its own layout, which only `read_attachment` is meant to reach, so the gate
// keeps the text of every staging it sees under the handle sdd answers with,
// and repeats sdd's staging rules: `patches` edit the file staged under
// `name` in place, and a leading `---` block is a header, not body. A change
// to either in sdd needs one here.
const bodyHandleField = "bodyHandle"

// stagedFile is a staged file's text as the gate followed it. One not held is
// a handle the gate already refused once for holding no text.
type stagedFile struct {
	Text string `json:"text"`
	Held bool   `json:"held"`
}

type stagedAnswer struct {
	Handle string `json:"handle"`
	SHA256 string `json:"sha256"`
}

type stagingInput struct {
	Name    string `json:"name"`
	Content string `json:"content"`
	Path    string `json:"path"`
	Patches []any  `json:"patches"`
}

// recordStaged keeps the text a staging that went through left under its
// handle, where the project lints captures. A staging the gate cannot follow
// (a patch it cannot apply, a file it cannot read, bytes whose hash differ
// from the one sdd answered with) drops what it held: that text is no longer
// the file's.
func recordStaged(ctx context.Context, env Env, payload hookio.Payload) {
	if payload.ToolName != mcpStageAttachment || payload.Failed() {
		return
	}

	config, err := env.config()
	if err != nil || config.DraftLint == "" {
		return
	}

	var input stagingInput

	err = json.Unmarshal(payload.ToolInput.Raw, &input)
	if err != nil {
		return
	}

	answer := answerOf(payload.ToolResponse, func(answer stagedAnswer) bool { return answer.Handle != "" })
	if answer.Handle == "" {
		return
	}

	path, err := ledgerPath(env.graphRoot(), env.ledgerDir(), payload)
	if err != nil {
		return
	}

	ledger := loadEvidence(ctx, path)

	text, followed := stagedText(env, input, ledger.Staged[answer.Handle])
	if followed && (answer.SHA256 == "" || hashed(text) == answer.SHA256) {
		ledger.Staged[answer.Handle] = stagedFile{Text: text, Held: true}
	} else {
		delete(ledger.Staged, answer.Handle)
	}

	_ = saveEvidence(ctx, env.graphRoot(), path, ledger)
}

// stagedText is the text a staging leaves the file with: the content given,
// the file a path names, or the held text with the patches applied by sdd's
// edit rule. sdd edits the file staged under `name` in place and answers with
// that file's handle, so held is the record under the answered handle; with
// none held, its text is empty and no patch applies.
func stagedText(env Env, input stagingInput, held stagedFile) (string, bool) {
	if len(input.Patches) > 0 {
		return applyPatch(held.Text, input.Patches)
	}

	if input.Path != "" {
		_, content, read := readNamed(env, input.Path)

		return string(content), read
	}

	return input.Content, true
}

func hashed(text string) string {
	sum := sha256.Sum256([]byte(text))

	return hex.EncodeToString(sum[:])
}

// stagedBody is what sdd takes as body from a staged file: everything after a
// header block opened and closed by a `---` line, else the whole file, a
// leading byte order mark dropped either way.
func stagedBody(text string) string {
	text = strings.TrimPrefix(text, "\uFEFF")
	lines := strings.SplitAfter(text, "\n")

	if !headerDelimiter(lines[0]) {
		return text
	}

	for index := 1; index < len(lines); index++ {
		if headerDelimiter(lines[index]) {
			return strings.Join(lines[index+1:], "")
		}
	}

	return text
}

func headerDelimiter(line string) bool {
	return strings.TrimRight(line, "\r\n") == "---"
}

// handleRefusal lints the body a capture call names by handle. A handle the
// gate holds no text for is refused once, asking for the file staged again or
// the body inline, and passes after. A call reaching it has a ledger: a capture
// start owes reads only a ledger holds, and a report counts only for a capture
// a ledger holds.
func handleRefusal(ctx context.Context, env Env, command string, ledger evidence, path string, handle string) string {
	file, found := ledger.Staged[handle]
	if file.Held {
		return lintBody(ctx, env, command, stagedBody(file.Text))
	}

	if found {
		return ""
	}

	ledger.Staged[handle] = stagedFile{}
	_ = saveEvidence(ctx, env.graphRoot(), path, ledger)

	return refusal("engine-handle.txt", "{handle}", handle)
}

func (call engineCall) handle() (string, bool) {
	handle, named := call.fields[bodyHandleField].(string)

	return handle, named && handle != ""
}
