package filelock

import (
	"errors"
	"syscall"
	"testing"
)

func TestRetryInterrupted(t *testing.T) {
	cases := []struct {
		name    string
		results []error
		want    error
		calls   int
	}{
		{name: "taken at once", results: []error{nil}, calls: 1},
		{name: "interrupted twice, then taken", results: []error{syscall.EINTR, syscall.EINTR, nil}, calls: 3},
		{name: "interrupted, then refused", results: []error{syscall.EINTR, syscall.EBADF}, want: syscall.EBADF, calls: 2},
		{name: "refused at once", results: []error{syscall.EWOULDBLOCK}, want: syscall.EWOULDBLOCK, calls: 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0

			err := retryInterrupted(func() error {
				calls++

				return tc.results[calls-1]
			})

			if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) || calls != tc.calls {
				t.Errorf("an interrupted call should be retried and any other outcome returned, got %v after %d calls", err, calls)
			}
		})
	}
}
