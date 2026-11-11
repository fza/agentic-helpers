package credfile_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/credfile"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestASecretKeyIsTheLastWordOrTheLastTwo(t *testing.T) {
	for _, tc := range []struct {
		name   string
		secret bool
	}{
		{"password", true}, {"DB_PASSWORD", true}, {"spring.datasource.password", true}, {"passwd", true},
		{"pwd", true}, {"db_pass", true}, {"passphrase", true}, {"client_secret", true}, {"secret", true},
		{"api_token", true}, {"accessToken", true}, {"apikey", true}, {"apiKey", true}, {"API_KEY", true},
		{"api-key", true}, {"APIKey", true}, {"aws_secret_access_key", true}, {"private_key", true},
		{"signingKey", true}, {"AWSSecretKey", true}, {"credentials", true}, {"creds", true}, {"Authorization", true}, {"session_cookie", true},
		{"max_tokens", false}, {"token_url", false}, {"password_min_length", false}, {"key", false},
		{"public_key", false}, {"author", false}, {"access_log", false}, {"PASSWORD_FILE", false}, {"", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if credfile.SecretKey(tc.name) != tc.secret {
				t.Errorf("the key name should read as secret only when its last word or last two words are listed")
			}
		})
	}
}

func TestAPlaceholderIsNoRealValue(t *testing.T) {
	for _, tc := range []struct {
		value string
		real  bool
	}{
		{"hunter2", true}, {`"hunter2",`, true}, {"'s3cr3t'", true}, {"true", true}, {"hunter2 # comment", true},
		{"", false}, {`""`, false}, {"null", false}, {"~", false}, {"${DB_PASSWORD}", false}, {"$DB_PASSWORD", false},
		{"{{ .Values.password }}", false}, {"%(password)s", false}, {"<your-password>", false}, {"****", false},
		{"xxxx", false}, {"${TOKEN} # from the vault", false}, {"{", false}, {"[", false}, {`"" # set me`, false}, {`"${TOKEN}"`, false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			if credfile.RealValue(tc.value) != tc.real {
				t.Errorf("only a value that can hold a secret should count as real")
			}
		})
	}
}

func TestOnlyConfigFilesAreReadByKey(t *testing.T) {
	for _, tc := range []struct {
		file   string
		config bool
	}{
		{"app.yaml", true}, {"a/b/settings.JSON", true}, {"Cargo.toml", true}, {"x.ini", true}, {"x.properties", true},
		{"main.tfvars", true}, {"pom.xml", true}, {".env", true}, {".env.local", true}, {"prod.env", true},
		{".npmrc", true}, {"/home/u/.aws/credentials", true}, {".s3cfg", true},
		{"main.go", false}, {"README.md", false}, {"environment.go", false}, {"key.pem", false},
	} {
		t.Run(tc.file, func(t *testing.T) {
			if credfile.ConfigType(tc.file) != tc.config {
				t.Errorf("only a file whose extension or name marks configuration should be read by key")
			}
		})
	}
}

func TestStoresAndBinaryKeyStoresAreKnownByPath(t *testing.T) {
	for _, file := range []string{"/h/.netrc", "/h/.pgpass", "/h/.git-credentials", "/srv/.htpasswd"} {
		if !credfile.Store(file) {
			t.Errorf("%s should be a credential store by its name", file)
		}
	}

	for _, file := range []string{"/h/a.p12", "/h/b.PFX", "/h/c.jks", "/h/d.keystore", "/h/.gnupg/private-keys-v1.d/x.key"} {
		if !credfile.BinaryKeyStore(file, "/h") {
			t.Errorf("%s should be a binary key store by its path", file)
		}
	}

	if credfile.BinaryKeyStore("/h/.gnupg/pubring.kbx", "/h") || credfile.Store("/h/netrc.md") {
		t.Error("a file named like no store should be no store")
	}
}

func spanLines(spans []credfile.Span) [][2]int {
	var lines [][2]int
	for _, span := range spans {
		lines = append(lines, [2]int{span.First, span.Last})
	}

	slices.SortFunc(lines, func(a, b [2]int) int { return a[0] - b[0] })

	return lines
}

func TestKeySpansCoverEachSecretLineInEveryConfigFormat(t *testing.T) {
	for _, tc := range []struct {
		file string
		text string
		want [][2]int
	}{
		{"app.yaml", "server:\n  port: 8080\n  password: hunter2\n  user: app\n", [][2]int{{3, 3}}},
		{"app.yaml", "password: ${DB_PASSWORD}\ntoken_url: https://x\nmax_tokens: 4096\nkey: app.kubernetes.io/x\n", nil},
		{"app.json", "{\n  \"user\": \"a\",\n  \"apiKey\": \"abc123\"\n}\n", [][2]int{{3, 3}}},
		{"min.json", `{"user":"a","client_secret":"abc123","n":1}`, [][2]int{{1, 1}}},
		{".env", "export API_TOKEN=abc123\nDEBUG=1\n", [][2]int{{1, 1}}},
		{"x.toml", "[db]\npassword = \"s3cr3t\"\n", [][2]int{{2, 2}}},
		{"x.ini", "[db]\npass=s3cr3t\n", [][2]int{{2, 2}}},
		{"x.xml", "<db>\n  <password>s3cr3t</password>\n  <conn user=\"a\" password=\"b\"/>\n</db>\n", [][2]int{{2, 2}, {3, 3}}},
		{"tls.yaml", "name: x\nprivate_key: |\n  line one\n  line two\nnext: y\n", [][2]int{{2, 4}}},
		{"main.go", "password := \"hunter2\"\n", nil},
	} {
		t.Run(tc.file, func(t *testing.T) {
			got := spanLines(credfile.Spans(context.Background(), tc.file, tc.text, 1))
			if !slices.Equal(got, tc.want) {
				t.Errorf("each secret-named key with a real value should be one span, its block with it: got %v", got)
			}
		})
	}
}

func TestAPrivateKeyIsOneSpanInAnyFile(t *testing.T) {
	key := test.SecretFixture(t, "positive/private-key.txt")
	text := "intro\n" + key + "\noutro\n"
	keyLines := strings.Count(key, "\n") + 1

	spans := credfile.Spans(context.Background(), "notes.md", text, 1)
	if !slices.Equal(spanLines(spans), [][2]int{{2, 1 + keyLines}}) {
		t.Errorf("a private key should be one span over every line it holds: got %v", spanLines(spans))
	}

	block := "tls_key: |\n  " + strings.ReplaceAll(key, "\n", "\n  ") + "\n"
	if !credfile.Overlaps(credfile.Spans(context.Background(), "tls.yaml", block, 1), 3, 4) {
		t.Error("a private key held in a YAML block should cover the block's later lines")
	}
}

func TestAPuTTYKeyFileRunsToItsMAC(t *testing.T) {
	text := "PuTTY-User-Key-File-3: ssh-ed25519\nEncryption: none\nPrivate-Lines: 1\nAAAA\nPrivate-MAC: ab\nafter\n"

	got := spanLines(credfile.Spans(context.Background(), "id.ppk", text, 1))
	if !slices.Equal(got, [][2]int{{1, 5}}) {
		t.Errorf("a PuTTY key should span from its header to its MAC line: got %v", got)
	}
}

func TestAStoreIsOneSpanOverTheWholeFile(t *testing.T) {
	got := spanLines(credfile.Spans(context.Background(), "/h/.netrc", "machine a\nlogin b\npassword c", 1))
	if !slices.Equal(got, [][2]int{{1, 3}}) {
		t.Errorf("a credential store should be withheld whole: got %v", got)
	}
}

func TestSpansCountFromTheFirstLineGiven(t *testing.T) {
	got := spanLines(credfile.Spans(context.Background(), "a.yaml", "x: 1\npassword: abc\n", 101))
	if !slices.Equal(got, [][2]int{{102, 102}}) {
		t.Errorf("a span should be numbered from the scanned part's first line: got %v", got)
	}
}

func TestAnAgeKeyIsASpan(t *testing.T) {
	data, err := fs.ReadFile(test.SecretFixtures(t), "gitleaks/age-secret-key.json")
	if err != nil {
		t.Fatalf("reading the age samples: %v", err)
	}

	var samples struct {
		TruePositives []struct {
			Text string `json:"text"`
		} `json:"truePositives"`
	}

	err = json.Unmarshal(data, &samples)
	if err != nil || len(samples.TruePositives) == 0 {
		t.Fatalf("decoding the age samples: %v", err)
	}

	spans := credfile.Spans(context.Background(), "keys.txt", "# created\n"+samples.TruePositives[0].Text+"\n", 1)
	if !credfile.Covered(spans, 2) || credfile.Covered(spans, 1) {
		t.Errorf("an age secret key should be a span over its own line: got %v", spanLines(spans))
	}
}

func TestAScanThatCannotFinishCoversTheWholeText(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got := spanLines(credfile.Spans(ctx, "notes.md", "a\nb\nc", 1))
	if !slices.Equal(got, [][2]int{{1, 3}}) {
		t.Errorf("a scan cut short should judge every line it could not read: got %v", got)
	}
}
