package memorylog

import (
	"fmt"
	"runtime/debug"
)

// guardFault runs read over a mapped segment, turning a fault in the mapping
// (the file truncated or its storage gone under it) into ErrFault. Any other
// panic is a bug and stays one.
func guardFault(read func() error) (err error) {
	previous := debug.SetPanicOnFault(true)

	defer func() {
		debug.SetPanicOnFault(previous)

		recovered := recover()
		if recovered == nil {
			return
		}

		fault, isFault := recovered.(interface{ Addr() uintptr })
		if !isFault {
			panic(recovered)
		}

		err = fmt.Errorf("%w at %#x", ErrFault, fault.Addr())
	}()

	return read()
}
