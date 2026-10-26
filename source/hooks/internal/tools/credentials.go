package tools

import (
	"context"
	"fmt"

	"github.com/fza/agentic-helpers/source/hooks/internal/secrets"
)

type labelled struct {
	label string
	text  string
}

// credentialIn is the refusal for the first file or flag of a write holding a
// credential, or nothing. It runs before the description is embedded, so a
// refused write never reaches the embedding endpoint.
func credentialIn(ctx context.Context, files []walked, fields []labelled) string {
	for _, file := range files {
		found := secrets.Scan(ctx, string(file.data), file.name)
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
