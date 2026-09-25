package install_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/install"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

var binaries = []string{"agentic-carryforward", "agentic-grounding-gate", "agentic-scratch"}

type fixture struct {
	t        *testing.T
	env      install.Env
	checkout string
}

type result struct {
	code   int
	stdout string
	stderr string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	checkout := t.TempDir()
	test.WriteFile(t, filepath.Join(checkout, "source", "hooks", "go.mod"), "module x\n")

	for _, skill := range []string{"agent-one", "agent-two"} {
		test.WriteFile(t, filepath.Join(checkout, "skills", skill, "SKILL.md"), "# "+skill+"\n")
	}

	bin := t.TempDir()
	for _, binary := range binaries {
		test.WriteFile(t, filepath.Join(bin, binary), "binary")
	}

	return &fixture{
		t:        t,
		checkout: checkout,
		env: install.Env{
			ConfigDir:  t.TempDir(),
			BinDir:     bin,
			WorkingDir: filepath.Join(checkout, "source", "hooks"),
			Now:        func() time.Time { return time.Date(2026, 9, 25, 13, 0, 0, 0, time.UTC) },
		},
	}
}

func (f *fixture) settings() string {
	return filepath.Join(f.env.ConfigDir, "settings.json")
}

func (f *fixture) run(args ...string) result {
	f.t.Helper()

	captured := &test.Captured{}
	code := install.Main(context.Background(), args, f.env, captured.Streams(""))

	return result{code: code, stdout: captured.Out.String(), stderr: captured.Err.String()}
}

func (f *fixture) written() map[string]any {
	f.t.Helper()

	data, err := os.ReadFile(f.settings())
	if err != nil {
		f.t.Fatalf("reading the settings: %v", err)
	}

	held := map[string]any{}

	err = json.Unmarshal(data, &held)
	if err != nil {
		f.t.Fatalf("decoding the settings: %v", err)
	}

	return held
}

func commands(held map[string]any) []string {
	var found []string

	hooks, _ := held["hooks"].(map[string]any)
	for _, groups := range hooks {
		for _, group := range groups.([]any) {
			for _, entry := range group.(map[string]any)["hooks"].([]any) {
				found = append(found, entry.(map[string]any)["command"].(string))
			}
		}
	}

	return found
}

func containing(found []string, part string) int {
	count := 0

	for _, command := range found {
		if strings.Contains(command, part) {
			count++
		}
	}

	return count
}

func TestSettings(t *testing.T) {
	t.Run("a settings file that does not exist is created", func(t *testing.T) {
		f := newFixture(t)

		if f.run().code != 0 {
			t.Fatal("the install should succeed")
		}

		found := commands(f.written())
		if containing(found, "agentic-carryforward") != 8 || containing(found, "agentic-grounding-gate") != 4 || containing(found, "agentic-scratch") != 2 {
			t.Errorf("every hook should be wired, got: %v", found)
		}

		if containing(found, filepath.Join(f.env.BinDir, "agentic-scratch")+`" end`) != 1 {
			t.Error("a hook should run from the directory the binaries sit in")
		}

		if groups := f.written()["hooks"].(map[string]any)["SessionStart"].([]any); len(groups) != 1 {
			t.Errorf("an event should hold one group per matcher, got %d", len(groups))
		}
	})

	t.Run("everything the person set survives, in its order", func(t *testing.T) {
		f := newFixture(t)
		test.WriteFile(t, f.settings(), test.Fixture(t, "install/theirs.json"))

		if f.run().code != 0 {
			t.Fatal("the install should succeed")
		}

		held := f.written()
		if held["model"] != "opus" || held["env"].(map[string]any)["SOME_TOKEN"] != "keep-me" || containing(commands(held), "echo their-own-rule") != 1 {
			t.Errorf("the person's settings should survive, got: %v", held)
		}

		startup := held["hooks"].(map[string]any)["SessionStart"].([]any)[0].(map[string]any)
		if startup["matcher"] != "startup" || len(startup["hooks"].([]any)) != 1 {
			t.Errorf("a group under another matcher should keep only its own entries, got: %v", startup)
		}

		data, _ := os.ReadFile(f.settings())
		text := string(data)
		if !(strings.Index(text, `"env"`) < strings.Index(text, `"model"`) && strings.Index(text, `"model"`) < strings.Index(text, `"hooks"`) && strings.Index(text, `"hooks"`) < strings.Index(text, `"statusLine"`)) {
			t.Errorf("the keys should keep their order, got: %s", text)
		}
	})

	t.Run("a copy of the old settings is kept", func(t *testing.T) {
		f := newFixture(t)
		test.WriteFile(t, f.settings(), test.Fixture(t, "install/theirs.json"))
		f.run()

		backup, err := os.ReadFile(f.settings() + ".bak-20260925T130000Z")
		if err != nil || string(backup) != test.Fixture(t, "install/theirs.json") {
			t.Error("the old file should be copied byte for byte")
		}
	})

	t.Run("running it twice changes nothing", func(t *testing.T) {
		f := newFixture(t)
		f.run()
		once, _ := os.ReadFile(f.settings())

		second := f.run()
		twice, _ := os.ReadFile(f.settings())

		if second.code != 0 || !strings.Contains(second.stdout, "already says this") || string(once) != string(twice) {
			t.Errorf("a second run should change nothing, got: %+v", second)
		}
	})

	t.Run("entries naming an old path or an old script are replaced", func(t *testing.T) {
		f := newFixture(t)
		test.WriteFile(t, f.settings(), test.Fixture(t, "install/moved.json"))
		f.run()

		found := commands(f.written())
		if containing(found, "/somewhere/else/") != 0 || containing(found, "/old/bin/") != 0 {
			t.Errorf("stale entries should be gone, got: %v", found)
		}
	})

	t.Run("remove takes only what this repository owns", func(t *testing.T) {
		f := newFixture(t)
		test.WriteFile(t, f.settings(), test.Fixture(t, "install/theirs.json"))
		f.run()

		if f.run("--remove").code != 0 {
			t.Fatal("the removal should succeed")
		}

		held := f.written()
		if found := commands(held); len(found) != 2 || containing(found, "echo their-") != 2 || held["model"] != "opus" {
			t.Errorf("only the person's own hooks should remain, got: %v", held)
		}
	})

	t.Run("remove drops an empty hooks key", func(t *testing.T) {
		f := newFixture(t)
		f.run()
		f.run("--remove")

		if _, found := f.written()["hooks"]; found {
			t.Error("an empty hooks key should go")
		}
	})

	t.Run("print writes nothing", func(t *testing.T) {
		f := newFixture(t)
		test.WriteFile(t, f.settings(), test.Fixture(t, "install/theirs.json"))

		got := f.run("--print")
		data, _ := os.ReadFile(f.settings())

		if got.code != 0 || string(data) != test.Fixture(t, "install/theirs.json") || !strings.Contains(got.stdout, "agentic-carryforward") {
			t.Errorf("print should show the wiring and write nothing, got: %+v", got)
		}
	})

	t.Run("settings that are not JSON are refused rather than overwritten", func(t *testing.T) {
		f := newFixture(t)
		test.WriteFile(t, f.settings(), "{ this is not json")

		got := f.run()
		data, _ := os.ReadFile(f.settings())

		if got.code != 1 || !strings.Contains(got.stderr, "not readable as JSON") || string(data) != "{ this is not json" {
			t.Errorf("a broken file should be refused and kept, got: %+v", got)
		}
	})

	t.Run("the gate refuses before a question, a draft edit or a read", func(t *testing.T) {
		f := newFixture(t)
		f.run()

		groups := f.written()["hooks"].(map[string]any)["PreToolUse"].([]any)
		if len(groups) != 1 || groups[0].(map[string]any)["matcher"] != "AskUserQuestion|Write|Edit|NotebookEdit|Bash" {
			t.Errorf("the gate mode should be wired once, on every gated tool, got: %v", groups)
		}
	})

	t.Run("a missing binary is named", func(t *testing.T) {
		f := newFixture(t)
		_ = os.Remove(filepath.Join(f.env.BinDir, "agentic-scratch"))

		if !strings.Contains(f.run().stderr, "agentic-scratch is missing") {
			t.Error("a binary not yet installed should be named")
		}
	})

	t.Run("outside a checkout, nothing is touched", func(t *testing.T) {
		f := newFixture(t)
		f.env.WorkingDir = t.TempDir()

		got := f.run()
		if got.code != 1 || test.Exists(t, f.settings()) {
			t.Errorf("an install outside a checkout should be refused, got: %+v", got)
		}
	})

	t.Run("an unknown flag is refused", func(t *testing.T) {
		if newFixture(t).run("--bogus").code != 1 {
			t.Error("an unknown flag should be refused")
		}
	})
}

func TestSkills(t *testing.T) {
	skills := func(f *fixture) string { return filepath.Join(f.env.ConfigDir, "skills") }

	t.Run("every skill is linked", func(t *testing.T) {
		f := newFixture(t)
		f.run()

		for _, skill := range []string{"agent-one", "agent-two"} {
			held, err := os.Readlink(filepath.Join(skills(f), skill))
			if err != nil || held != filepath.Join(f.checkout, "skills", skill) {
				t.Errorf("%s should be a link to the checkout, got %q", skill, held)
			}
		}
	})

	t.Run("a link naming somewhere else is replaced", func(t *testing.T) {
		f := newFixture(t)
		link := filepath.Join(skills(f), "agent-one")
		test.WriteFile(t, filepath.Join(skills(f), "placeholder"), "")

		err := os.Symlink("/somewhere/else/that/moved", link)
		if err != nil {
			t.Fatalf("linking: %v", err)
		}

		f.run()

		held, _ := os.Readlink(link)
		if held != filepath.Join(f.checkout, "skills", "agent-one") {
			t.Error("a stale link should point at the checkout")
		}
	})

	t.Run("a directory somebody else put there is left alone", func(t *testing.T) {
		f := newFixture(t)
		theirs := filepath.Join(skills(f), "agent-one", "SKILL.md")
		test.WriteFile(t, theirs, "theirs")

		got := f.run()
		data, _ := os.ReadFile(theirs)

		if !strings.Contains(got.stderr, "not a link") || string(data) != "theirs" {
			t.Errorf("a real directory should be reported and kept, got: %+v", got)
		}

		removed := f.run("--remove")

		if !test.Exists(t, theirs) || strings.Contains(removed.stderr, "not a link") {
			t.Error("a removal should pass a real directory by, silently")
		}
	})

	t.Run("remove unlinks them", func(t *testing.T) {
		f := newFixture(t)
		f.run()
		f.run("--remove")

		entries, _ := os.ReadDir(skills(f))
		if len(entries) != 0 {
			t.Error("every link should go")
		}
	})

	t.Run("running it twice relinks nothing", func(t *testing.T) {
		f := newFixture(t)
		f.run()

		if strings.Contains(f.run().stdout, "linked") {
			t.Error("a second run should link nothing")
		}
	})

	t.Run("print links nothing", func(t *testing.T) {
		f := newFixture(t)

		got := f.run("--print")
		if !strings.Contains(got.stdout, "link ") || test.Exists(t, skills(f)) {
			t.Error("print should name the links and make none")
		}
	})
}
