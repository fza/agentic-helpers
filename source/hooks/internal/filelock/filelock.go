// Package filelock holds a shared or an exclusive flock on an open file. A blocking flock
// a signal interrupts fails with EINTR, and a caller that returns that error
// fails where it should have waited, so the wait is taken up again.
package filelock

import (
	"errors"
	"os"
	"syscall"
)

func Exclusive(file *os.File) error {
	return retryInterrupted(func() error {
		return syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
	})
}

func Shared(file *os.File) error {
	return retryInterrupted(func() error {
		return syscall.Flock(int(file.Fd()), syscall.LOCK_SH)
	})
}

// TryExclusive takes the lock where nobody holds it, answering false at once
// where somebody does.
func TryExclusive(file *os.File) (bool, error) {
	err := retryInterrupted(func() error {
		return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	})
	if errors.Is(err, syscall.EWOULDBLOCK) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	return true, nil
}

func Unlock(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}

func retryInterrupted(call func() error) error {
	for {
		err := call()
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}
