package process

import (
	"errors"
	"os/exec"
	"testing"
)

func TestOutsideRepository(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "git refusing a directory outside any repository",
			err:  &exec.ExitError{Stderr: []byte("fatal: not a git repository (or any of the parent directories): .git\n")},
			want: true,
		},
		{
			name: "git failing another way",
			err:  &exec.ExitError{Stderr: []byte("fatal: unable to read index file\n")},
		},
		{
			name: "git never starting",
			err:  errors.New(`exec: "git": executable file not found in $PATH`),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if outsideRepository(tc.err) != tc.want {
				t.Errorf("only git's refusal outside a repository should read as one")
			}
		})
	}
}
