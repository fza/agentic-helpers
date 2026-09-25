package project_test

import (
	"context"
	"errors"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/project"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestRoot(t *testing.T) {
	cases := []struct {
		name   string
		lookup test.CommonDir
		want   string
	}{
		{name: "main checkout", lookup: test.CommonDir{Dir: "/work/repo/.git"}, want: "/work/repo"},
		{name: "outside a repository", lookup: test.CommonDir{Err: errors.New("not a git repository")}, want: "/work/worktree"},
		{name: "empty answer", lookup: test.CommonDir{}, want: "/work/worktree"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := project.Root(context.Background(), "/work/worktree", tc.lookup)

			if got != tc.want {
				t.Error("the root should be the parent of the shared git directory, or the base without one")
			}
		})
	}
}
