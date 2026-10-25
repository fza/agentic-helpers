//go:build !unix

package memorylog

import "os"

// Without flock, writers are serialized only within one process, by the Log's
// mutex, and a directory cannot be synced. Releases ship for unix alone.
func lockFile(*os.File) error { return nil }

func unlockFile(*os.File) error { return nil }

func syncDir(string) error { return nil }
