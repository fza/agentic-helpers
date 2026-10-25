package writers_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/writers"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func checkout(t *testing.T, root string, name string) string {
	t.Helper()

	at := filepath.Join(root, name)
	test.WriteFile(t, filepath.Join(at, ".git", "HEAD"), "ref: refs/heads/main\n")

	return at
}

func TestAdmitClaimsThenHoldsToTheList(t *testing.T) {
	root := t.TempDir()
	list := writers.List{Home: filepath.Join(root, "home"), Key: "github.com/acme/app", Lookup: test.GitDirOf{}}
	first, second := checkout(t, root, "first"), checkout(t, root, "second")

	err := list.Admit(context.Background(), first)
	if err != nil {
		t.Fatalf("the first call should claim the project: %v", err)
	}

	var refusal writers.NotListed

	err = list.Admit(context.Background(), second)
	if !errors.As(err, &refusal) || refusal.Here != second || len(refusal.Checkouts) != 1 || refusal.Checkouts[0] != first {
		t.Errorf("a checkout not listed should be refused, naming the listed ones and itself, got %v", err)
	}

	_, err = list.Trust(context.Background(), second, second)
	if !errors.As(err, &refusal) {
		t.Errorf("a checkout not listed should never trust, got %v", err)
	}

	added, err := list.Trust(context.Background(), first, second)
	if err != nil || added != second {
		t.Fatalf("a listed checkout should trust another, got %q %v", added, err)
	}

	err = list.Admit(context.Background(), second)
	if err != nil {
		t.Errorf("a trusted checkout should be admitted: %v", err)
	}
}

func TestAListThatCannotBeReadAdmitsNoOne(t *testing.T) {
	cases := map[string]func(t *testing.T, at string){
		"a link": func(t *testing.T, at string) {
			target := filepath.Join(filepath.Dir(at), "target.json")
			test.WriteFile(t, target, "[]\n")
			_ = os.Symlink("target.json", at)
		},
		"a hard link": func(t *testing.T, at string) {
			target := filepath.Join(filepath.Dir(at), "target.json")
			test.WriteFile(t, target, "[]\n")
			_ = os.Link(target, at)
		},
		"a pipe":           func(t *testing.T, at string) { _ = syscall.Mkfifo(at, 0o600) },
		"too large":        func(t *testing.T, at string) { test.WriteFile(t, at, "["+string(make([]byte, 70_000))+"]") },
		"a relative entry": func(t *testing.T, at string) { test.WriteFile(t, at, `["app/.git"]`) },
	}

	for name, plant := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			list := writers.List{Home: filepath.Join(root, "home"), Key: "github.com/acme/app", Lookup: test.GitDirOf{}}
			app := checkout(t, root, "app")

			_ = os.MkdirAll(filepath.Dir(list.Path()), 0o700)
			plant(t, list.Path())

			done := make(chan error, 1)

			go func() { done <- list.Admit(context.Background(), app) }()

			select {
			case err := <-done:
				if !errors.Is(err, writers.ErrUnreadable) {
					t.Errorf("a list that cannot be read should admit no one, got %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("reading the list should never wait on a pipe")
			}
		})
	}
}

func TestAFolderOfTheKeyThatIsALinkAdmitsNoOne(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	list := writers.List{Home: home, Key: "github.com/acme/app", Lookup: test.GitDirOf{}}
	app := checkout(t, root, "app")

	// A sibling inside the same folder: os.Root alone refuses an absolute link
	// or one leaving the folder it opened, and follows this one.
	owner := filepath.Join(home, ".claude", "agentic-helpers", "github.com")
	test.WriteFile(t, filepath.Join(owner, "other", "app", "writers.json"), `["`+filepath.Join(app, ".git")+`"]`)
	_ = os.Symlink("other", filepath.Join(owner, "acme"))

	err := list.Admit(context.Background(), app)
	if err == nil {
		t.Error("a list reached through a link should never admit")
	}
}

func TestALegacyListMovesIn(t *testing.T) {
	root := t.TempDir()
	app := checkout(t, root, "app")
	legacy := filepath.Join(root, "home", ".claude", "agentic-tools", "github.com", "acme", "app", "writers.json")
	test.WriteFile(t, legacy, `["`+filepath.Join(app, ".git")+`"]`+"\n")

	list := writers.List{Home: filepath.Join(root, "home"), Key: "github.com/acme/app", Lookup: test.GitDirOf{}}

	err := list.Admit(context.Background(), app)
	if err != nil {
		t.Fatalf("a checkout the legacy list names should be admitted: %v", err)
	}

	if test.Exists(t, legacy) || !test.Exists(t, list.Path()) {
		t.Error("the legacy list should move into the project's list")
	}
}

func TestALegacyListBindsBeforeItMoves(t *testing.T) {
	root := t.TempDir()
	owner, other := checkout(t, root, "owner"), checkout(t, root, "other")
	home := filepath.Join(root, "home")
	legacy := filepath.Join(home, ".claude", "agentic-tools", "github.com", "acme", "app", "writers.json")
	test.WriteFile(t, legacy, `["`+filepath.Join(owner, ".git")+`"]`+"\n")

	list := writers.List{Home: home, Key: "github.com/acme/app", Lookup: test.GitDirOf{}}

	var refusal writers.NotListed

	err := list.Check(context.Background(), other)
	if !errors.As(err, &refusal) {
		t.Errorf("a check should hold to the legacy list while the project's is missing, got %v", err)
	}

	err = list.Admit(context.Background(), other)
	if !errors.As(err, &refusal) {
		t.Errorf("a checkout the legacy list leaves out should never claim the project, got %v", err)
	}

	data, _ := os.ReadFile(list.Path())
	if string(data) != `["`+filepath.Join(owner, ".git")+`"]`+"\n" {
		t.Errorf("the refused claim should still move the legacy list in, got %s", data)
	}
}

func TestACheckClaimsNothing(t *testing.T) {
	root := t.TempDir()
	list := writers.List{Home: filepath.Join(root, "home"), Key: "github.com/acme/app", Lookup: test.GitDirOf{}}

	err := list.Check(context.Background(), checkout(t, root, "app"))
	if !errors.Is(err, writers.ErrUnclaimed) || test.Exists(t, list.Path()) {
		t.Errorf("a check of an unclaimed project should say so and write nothing, got %v", err)
	}
}
