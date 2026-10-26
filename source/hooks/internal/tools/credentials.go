package tools

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/fza/agentic-helpers/source/hooks/internal/secrets"
)

type labelled struct {
	label string
	text  string
}

// credentialIn is the refusal for the first file or flag of a write holding a
// credential, or nothing. add and update run it before the description is
// embedded, so a refused write never reaches the embedding endpoint;
// writeVersion runs it again on every version, so no write path skips it.
func credentialIn(ctx context.Context, files []walked, fields []labelled) string {
	for _, file := range files {
		found := scanFile(ctx, file)
		if len(found) > 0 {
			return credentialRefusal(file.name, found)
		}
	}

	for _, field := range fields {
		found := secrets.Scan(ctx, field.text, "")
		if len(found) > 0 {
			return credentialRefusal(field.label, found)
		}
	}

	return ""
}

// scanFile scans a tool file by its name, so path rules fire on it, and again
// as text that is no file: gitleaks' path allowlists skip whole files (lock
// files, vendored folders, images), and a tool file of such a name would
// otherwise keep a credential unscanned.
func scanFile(ctx context.Context, file walked) []secrets.Finding {
	content := string(file.data)
	found := append(secrets.Scan(ctx, content, file.name), secrets.Scan(ctx, content, "")...)

	slices.SortStableFunc(found, func(a, b secrets.Finding) int { return cmp.Compare(a.Line, b.Line) })

	return found
}

func credentialInVersion(ctx context.Context, next version) error {
	refusal := credentialIn(ctx, next.files, append(describedFields(next.spec), labelled{label: "--why", text: next.why}))
	if refusal != "" {
		return errors.New(refusal)
	}

	return nil
}

func credentialRefusal(where string, found []secrets.Finding) string {
	rules, line := secrets.Describe(found)

	return fmt.Sprintf("%s: %s looks like a credential (%s, line %d); the library never holds one", self, where, rules, line)
}

func describedFields(described spec) []labelled {
	return []labelled{
		{label: "--summary", text: described.Summary},
		{label: "--usage", text: described.Usage},
		{label: "--inputs", text: described.Inputs},
		{label: "--outputs", text: described.Outputs},
	}
}
