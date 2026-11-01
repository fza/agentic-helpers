package test

import (
	"context"
	"sync"
)

// SddValues stands in for `sdd config get <key>`: it answers Values by key,
// nothing for a key it lacks, as an sdd without the key prints nothing on
// stdout, and counts each call, every one a process the real reader starts.
type SddValues struct {
	Values map[string]string
	mutex  sync.Mutex
	asked  int
}

func (fake *SddValues) SddValue(_ context.Context, _ string, key string) string {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()

	fake.asked++

	return fake.Values[key]
}

func (fake *SddValues) Asked() int {
	fake.mutex.Lock()
	defer fake.mutex.Unlock()

	return fake.asked
}
