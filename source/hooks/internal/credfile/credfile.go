// Package credfile tells which lines of a file hold a credential before a
// tool shows them: a key with a secret name holding a real value in a config
// file, a credential store known by its name, and a private key, found by the
// shared scanner's private-key and age-secret-key rules or as a PuTTY key
// file. A file found by its path alone, a binary key store, is refused whole.
//
// A credential is a span of lines: a multi-line one carries its key name or
// marker only on its first line, so spans are found over the file, not over
// the slice a tool returns, and a slice is judged by whether it overlaps one.
package credfile

import (
	"context"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode"

	"github.com/fza/agentic-helpers/source/hooks/internal/secrets"
)

// ScanLimit is how much of a file is scanned whole for spans; Lookback how
// much before and after a slice of a larger file is scanned with it, so a span
// crossing either end of the slice is found from its first line to its last.
const (
	ScanLimit = 8 << 20
	Lookback  = 1 << 20
)

// RuleKeyName ... RuleStore name what a span holds, as the withheld marker and
// the refusal name it.
const (
	RuleKeyName    = "secret-key-name"
	RulePrivateKey = "private-key"
	RuleAgeKey     = "age-secret-key"
	RulePuTTY      = "putty-private-key"
	RuleStore      = "credential-store"
)

// Span / First and Last are the 1-based lines it covers, Last included; Key
// the secret-named key it holds, for a key-name span.
type Span struct {
	First int
	Last  int
	Rule  string
	Key   string
}

var (
	configExtensions = []string{
		".yaml", ".yml", ".json", ".jsonc", ".json5", ".toml", ".ini", ".cfg", ".conf", ".properties", ".tfvars", ".xml",
	}
	configNames      = []string{".npmrc", ".pypirc", ".yarnrc", "credentials", ".s3cfg", ".env"}
	storeNames       = []string{".netrc", ".pgpass", ".git-credentials", ".htpasswd"}
	binaryExtensions = []string{".p12", ".pfx", ".jks", ".keystore"}

	secretLastWords = []string{
		"password", "passwd", "pwd", "pass", "passphrase", "secret", "token", "apikey", "credential", "credentials",
		"creds", "authorization", "cookie",
	}
	secretLastPairs = []string{
		"api key", "access key", "secret key", "private key", "signing key", "encryption key", "master key",
		"auth key", "client secret", "session key",
	}
)

// ConfigType is whether a file is read by key: its extension or name marks it
// as configuration.
func ConfigType(file string) bool {
	base := path.Base(file)

	return slices.Contains(configExtensions, strings.ToLower(path.Ext(base))) || slices.Contains(configNames, base) ||
		strings.HasPrefix(base, ".env.") || strings.HasSuffix(base, ".env")
}

// Store is whether a file's name marks it as a credential store, refused
// whole.
func Store(file string) bool {
	return slices.Contains(storeNames, path.Base(file))
}

// BinaryKeyStore is whether a file is a private key store by its path alone:
// its content is binary, so no marker in it can be read.
func BinaryKeyStore(file string, home string) bool {
	if slices.Contains(binaryExtensions, strings.ToLower(path.Ext(file))) {
		return true
	}

	gnupg := path.Join(home, ".gnupg", "private-keys-v1.d")

	return home != "" && strings.HasPrefix(file, gnupg+"/")
}

// Spans is every credential span in text, the file's content or the part of
// it scanned, its lines numbered from first. file names it, for its type.
func Spans(ctx context.Context, file string, text string, first int) []Span {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")

	var spans []Span

	if Store(file) {
		spans = append(spans, Span{First: first, Last: first + len(lines) - 1, Rule: RuleStore})
	}

	for _, finding := range secrets.Scan(ctx, text, "") {
		switch finding.RuleID {
		case RulePrivateKey, RuleAgeKey:
			spans = append(spans, Span{
				First: first + finding.Line - 1, Last: first + max(finding.LastLine, finding.Line) - 1, Rule: finding.RuleID,
			})
		case secrets.Incomplete:
			// An unfinished scan judges the whole text as one span: nothing
			// it could not read passes.
			spans = append(spans, Span{First: first, Last: first + len(lines) - 1, Rule: secrets.Incomplete})
		}
	}

	spans = append(spans, puttySpans(lines, first)...)

	if ConfigType(file) {
		spans = append(spans, keySpans(file, lines, first)...)
	}

	return spans
}

// puttySpans is each PuTTY key file block, from its first line to its
// Private-MAC line, or to the end where it has none.
func puttySpans(lines []string, first int) []Span {
	var spans []Span

	for index := 0; index < len(lines); index++ {
		if !strings.HasPrefix(strings.TrimSpace(lines[index]), "PuTTY-User-Key-File-") {
			continue
		}

		last := len(lines) - 1

		for end := index + 1; end < len(lines); end++ {
			if strings.HasPrefix(strings.TrimSpace(lines[end]), "Private-MAC:") {
				last = end

				break
			}
		}

		spans = append(spans, Span{First: first + index, Last: first + last, Rule: RulePuTTY})
		index = last
	}

	return spans
}

var (
	quotedPair = regexp.MustCompile(`"((?:[^"\\]|\\.)+)"\s*:\s*("(?:[^"\\]|\\.)*"|[^,}\]]*)`)
	plainPair  = regexp.MustCompile(`^\s*(?:-\s+)?(?:export\s+)?['"]?([A-Za-z0-9_.\-]+)['"]?\s*[:=]\s*(.*)$`)
	xmlElement = regexp.MustCompile(`<([A-Za-z0-9_.:\-]+)(?:\s[^>]*)?>([^<]*)</`)
	xmlAttr    = regexp.MustCompile(`([A-Za-z0-9_.:\-]+)\s*=\s*"([^"]*)"`)
)

// keySpans is each secret-named key holding a real value, one line each, or
// with its block for a YAML block scalar.
func keySpans(file string, lines []string, first int) []Span {
	var spans []Span

	for index := range lines {
		for _, pair := range Pairs(file, lines[index]) {
			if !SecretKey(pair[0]) {
				continue
			}

			value := strings.TrimSpace(pair[1])
			last := index

			if strings.HasPrefix(value, "|") || strings.HasPrefix(value, ">") {
				last, value = blockScalar(lines, index)
			}

			if !RealValue(value) {
				continue
			}

			spans = append(spans, Span{First: first + index, Last: first + last, Rule: RuleKeyName, Key: pair[0]})
		}
	}

	return spans
}

// Pairs is every key and value a line of a config file holds, as its format
// writes them: quoted keys as JSON writes them, `key: value` and
// `key = value` as YAML, TOML, INI, properties and env files do, and for XML
// an element's text or an attribute's value.
func Pairs(file string, line string) [][2]string {
	var pairs [][2]string

	for _, match := range quotedPair.FindAllStringSubmatch(line, -1) {
		pairs = append(pairs, [2]string{match[1], match[2]})
	}

	if len(pairs) == 0 {
		match := plainPair.FindStringSubmatch(line)
		if match != nil {
			pairs = append(pairs, [2]string{match[1], match[2]})
		}
	}

	if strings.EqualFold(path.Ext(file), ".xml") {
		for _, match := range xmlElement.FindAllStringSubmatch(line, -1) {
			pairs = append(pairs, [2]string{match[1], match[2]})
		}

		for _, match := range xmlAttr.FindAllStringSubmatch(line, -1) {
			pairs = append(pairs, [2]string{match[1], `"` + match[2] + `"`})
		}
	}

	return pairs
}

// blockScalar is the last line of the YAML block scalar the key at index
// opens, every following line indented deeper than the key or blank, and the
// block's text.
func blockScalar(lines []string, index int) (int, string) {
	indent := indentOf(lines[index])
	last := index

	var text []string

	for next := index + 1; next < len(lines); next++ {
		if strings.TrimSpace(lines[next]) == "" {
			continue
		}

		if indentOf(lines[next]) <= indent {
			break
		}

		last = next
		text = append(text, strings.TrimSpace(lines[next]))
	}

	return last, strings.Join(text, "\n")
}

func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

// SecretKey is whether a key's name marks its value as a secret: split into
// words at `_`, `-`, `.`, a colon and camelCase, its last word or its last two
// words are on the lists.
func SecretKey(name string) bool {
	words := Words(name)
	if len(words) == 0 {
		return false
	}

	last := words[len(words)-1]
	if slices.Contains(secretLastWords, last) {
		return true
	}

	return len(words) > 1 && slices.Contains(secretLastPairs, words[len(words)-2]+" "+last)
}

// Words is a key name split at `_`, `-`, `.`, a colon and each camelCase
// boundary, lowercased: `APIKey` reads as `api key`.
func Words(name string) []string {
	var words []string

	for _, part := range strings.FieldsFunc(name, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		runes := []rune(part)
		start := 0

		for index := 1; index < len(runes); index++ {
			lowerToUpper := unicode.IsLower(runes[index-1]) && unicode.IsUpper(runes[index])
			acronymEnd := unicode.IsUpper(runes[index-1]) && unicode.IsUpper(runes[index]) &&
				index+1 < len(runes) && unicode.IsLower(runes[index+1])

			if lowerToUpper || acronymEnd {
				words = append(words, strings.ToLower(string(runes[start:index])))
				start = index
			}
		}

		words = append(words, strings.ToLower(string(runes[start:])))
	}

	return words
}

var (
	reference = regexp.MustCompile(`^(\$\{.*\}|\$[A-Za-z_][A-Za-z0-9_]*|\{\{.*\}\}|%\(.*\)s|<.*>)$`)
	masked    = regexp.MustCompile(`^[*xX]+$`)
)

// RealValue is whether a value can hold a secret: not empty, null, `~`, a
// reference to a value kept elsewhere, a run of only `*` or `x`, or the
// opening of a nested object or list.
func RealValue(value string) bool {
	value = strings.TrimSpace(value)

	if value != "" && (value[0] == '"' || value[0] == '\'') {
		closing := strings.IndexByte(value[1:], value[0])
		if closing >= 0 {
			value = value[1 : closing+1]
		}
	} else {
		// An unquoted value ends at a comment, as YAML, TOML and INI read
		// one.
		value, _, _ = strings.Cut(value, " #")
		value = strings.TrimRight(strings.TrimSpace(value), ",;")
	}

	value = strings.TrimSpace(value)

	switch strings.ToLower(value) {
	case "", "null", "~", "{", "[", "{}", "[]":
		return false
	}

	return !reference.MatchString(value) && !masked.MatchString(value)
}

// Overlaps is whether a span covers any line from first to last.
func Overlaps(spans []Span, first int, last int) bool {
	return slices.ContainsFunc(spans, func(span Span) bool { return span.First <= last && span.Last >= first })
}

// Covered is whether a span covers the line.
func Covered(spans []Span, line int) bool {
	return Overlaps(spans, line, line)
}
