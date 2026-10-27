package project_test

import (
	"context"
	"errors"
	"testing"

	"github.com/fza/agentic-helpers/source/hooks/internal/project"
	"github.com/fza/agentic-helpers/source/hooks/test"
)

func TestRemembered(t *testing.T) {
	cases := []struct {
		name   string
		lookup test.CommonDir
		asks   []string
		want   int
	}{
		{name: "one directory asked often", lookup: test.CommonDir{Dir: "/work/repo/.git"},
			asks: []string{"/work/repo", "/work/repo", "/work/repo"}, want: 1},
		{name: "each directory asked once", lookup: test.CommonDir{Dir: "/work/repo/.git"},
			asks: []string{"/work/repo", "/work/other", "/work/repo"}, want: 2},
		{name: "a failed lookup asked again", lookup: test.CommonDir{Err: errors.New("not a git repository")},
			asks: []string{"/work/repo", "/work/repo"}, want: 2},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			counted := &test.CountedCommonDir{Lookup: tc.lookup}
			remembered := &project.Remembered{Lookup: counted}

			for _, dir := range tc.asks {
				got, err := remembered.CommonDir(context.Background(), dir)
				if err == nil && got != tc.lookup.Dir {
					t.Error("a remembered answer should be the lookup's own")
				}
			}

			if counted.Calls() != tc.want {
				t.Error("the lookup should be asked once per directory, and again after a failure")
			}
		})
	}
}
