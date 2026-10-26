package capture

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fza/agentic-helpers/source/hooks/internal/sddcaps"
	"github.com/fza/agentic-helpers/source/hooks/internal/secrets"
)

const refusedPrefix = "agentic-capture: refused. "

// leaks scans exactly what `sdd new` is about to be handed: the body, every
// header value as its flag carries it (a YAML escape is decoded by then), and
// the bytes of every file the header attaches, read where sdd reads them. The
// grounding gate scans the draft before this command runs; scanning again here
// catches a draft or a file changed since, or written by the same Bash call.
// It names what refuses the capture, or nothing. Standard input and a file
// that cannot be read refuse too: neither can be scanned.
func (run *capture) leaks(ctx context.Context, held draft) string {
	draftName := run.request.draft

	findings := secrets.Scan(ctx, held.body, "")
	if len(findings) > 0 {
		rules, line := secrets.Describe(findings)

		return holdsCredential(rules, draftName, line+held.offset)
	}

	// The argument vector is `sdd new <type> <layer> <body>`, then a flag and
	// its value for every header field.
	args := command(held, "")

	for index, field := range []string{"type", "layer"} {
		found := scanned(ctx, args[2+index], "", fmt.Sprintf("%s's %s", draftName, field))
		if found != "" {
			return found
		}
	}

	for index := 5; index+1 < len(args); index += 2 {
		flag, value := args[index], args[index+1]

		found := scanned(ctx, value, "", fmt.Sprintf("%s's %s", draftName, flag))
		if found != "" {
			return found
		}
	}

	for _, item := range items(held.front["attach"]) {
		found := run.attachmentLeaks(ctx, fmt.Sprint(item))
		if found != "" {
			return found
		}
	}

	return ""
}

func (run *capture) attachmentLeaks(ctx context.Context, value string) string {
	attached := sddcaps.Attachment(value)
	if attached.Stdin {
		return fmt.Sprintf("%sThe capture attaches standard input as %s, which cannot be scanned for a credential; attach a file instead",
			refusedPrefix, attached.Name)
	}

	path := attached.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(run.root, path)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("%sThe capture names %s, which cannot be read to scan for a credential", refusedPrefix, attached.Path)
	}

	// By its name, so path rules fire on it, and where that finds nothing, again
	// as text that is no file: gitleaks' global allowlist skips whole files by
	// name (`.bin`, images, fonts), and a credential attached under such a name
	// would go unscanned. A scan by a name it does not skip finds all the
	// nameless scan would.
	content := string(raw)

	findings := secrets.Scan(ctx, content, attached.Name)
	if len(findings) == 0 {
		findings = secrets.Scan(ctx, content, "")
	}

	return described(findings, attached.Path)
}

func scanned(ctx context.Context, text string, name string, where string) string {
	return described(secrets.Scan(ctx, text, name), where)
}

func described(findings []secrets.Finding, where string) string {
	if len(findings) == 0 {
		return ""
	}

	rules, line := secrets.Describe(findings)

	return holdsCredential(rules, where, line)
}

func holdsCredential(rules string, where string, line int) string {
	return fmt.Sprintf("%sThe capture holds what looks like a credential (%s, %s, line %d); the graph is shared and immutable",
		refusedPrefix, rules, where, line)
}
