package filelock_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fza/agentic-helpers/source/hooks/internal/filelock"
)

func TestExclusiveWaitsForTheHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".lock")

	holder, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("opening the lock: %v", err)
	}
	defer holder.Close()

	waiter, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("opening the lock again: %v", err)
	}
	defer waiter.Close()

	err = filelock.Exclusive(holder)
	if err != nil {
		t.Fatalf("taking the free lock: %v", err)
	}

	taken := make(chan error, 1)

	go func() {
		taken <- filelock.Exclusive(waiter)
	}()

	select {
	case <-taken:
		t.Fatal("a held lock should keep a second taker waiting")
	case <-time.After(100 * time.Millisecond):
	}

	err = filelock.Unlock(holder)
	if err != nil {
		t.Fatalf("releasing the lock: %v", err)
	}

	select {
	case err = <-taken:
		if err != nil {
			t.Errorf("the waiter should take the lock once it is released, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("the waiter should take the lock once it is released")
	}
}

func openTwice(t *testing.T) (*os.File, *os.File) {
	t.Helper()

	path := filepath.Join(t.TempDir(), ".lock")

	first, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("opening the lock: %v", err)
	}

	t.Cleanup(func() { _ = first.Close() })

	second, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("opening the lock again: %v", err)
	}

	t.Cleanup(func() { _ = second.Close() })

	return first, second
}

func TestSharedHoldersShareAndKeepAnExclusiveTakerOut(t *testing.T) {
	first, second := openTwice(t)

	err := filelock.Shared(first)
	if err != nil {
		t.Fatalf("taking the free lock shared: %v", err)
	}

	taken := make(chan error, 1)

	go func() { taken <- filelock.Shared(second) }()

	select {
	case err = <-taken:
		if err != nil {
			t.Fatalf("a second shared holder should be let in, got %v", err)
		}
	case <-time.After(time.Second):
		t.Error("a second shared holder should not wait for the first")

		_ = filelock.Unlock(first)
		<-taken

		return
	}

	_ = filelock.Unlock(second)

	held, err := filelock.TryExclusive(second)
	if err != nil || held {
		t.Errorf("an exclusive try should give way to a shared holder, got %v %v", held, err)
	}

	_ = filelock.Unlock(first)

	held, err = filelock.TryExclusive(second)
	if err != nil || !held {
		t.Errorf("an exclusive try should take a lock nobody holds, got %v %v", held, err)
	}
}

func TestSharedWaitsForAnExclusiveHolder(t *testing.T) {
	holder, waiter := openTwice(t)

	err := filelock.Exclusive(holder)
	if err != nil {
		t.Fatalf("taking the free lock: %v", err)
	}

	taken := make(chan error, 1)

	go func() { taken <- filelock.Shared(waiter) }()

	select {
	case <-taken:
		t.Fatal("an exclusive holder should keep a shared taker waiting")
	case <-time.After(100 * time.Millisecond):
	}

	_ = filelock.Unlock(holder)

	select {
	case err = <-taken:
		if err != nil {
			t.Errorf("the shared taker should get the lock once it is released, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("the shared taker should get the lock once it is released")
	}
}
