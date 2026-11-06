// Package process runs commands and reads the process table, the latter
// through the kernel: sysctl on darwin, /proc on Linux. A `ps` spawn costs
// milliseconds on every ancestor of every seat lookup, and its text follows the
// caller's locale.
package process

import (
	"bytes"
	"encoding/binary"
	"os"
	"strings"
	"time"
)

// ProcargsCommand is the executable path and the arguments a darwin process
// was started with, from its kern.procargs2: the argument count, the
// executable path padded with NULs, then that many arguments. The kernel
// copies them from the process's own stack, so they are what the process
// started with, not what it set since.
func ProcargsCommand(raw []byte) (string, []string, bool) {
	if len(raw) < 4 {
		return "", nil, false
	}

	count := int(binary.NativeEndian.Uint32(raw[:4]))

	executable, rest, found := bytes.Cut(raw[4:], []byte{0})
	if !found {
		return "", nil, false
	}

	rest = bytes.TrimLeft(rest, "\x00")
	args := make([]string, 0, count)

	for range count {
		var arg []byte

		arg, rest, found = bytes.Cut(rest, []byte{0})
		if !found {
			return "", nil, false
		}

		args = append(args, string(arg))
	}

	return string(executable), args, true
}

type Table struct{}

func (Table) Self() int {
	return os.Getpid()
}

func (Table) Parent() int {
	return os.Getppid()
}

// LstartLayout is how `ps -o lstart=` prints a start time under the C locale,
// in the machine's local zone. Claims written by earlier releases hold it.
// Under any other locale `ps` translates the day and month names, which no
// layout here can read.
const LstartLayout = "Mon Jan _2 15:04:05 2006"

func ParseLstart(text string, zone *time.Location) (time.Time, bool) {
	started, err := time.ParseInLocation(LstartLayout, strings.Join(strings.Fields(text), " "), zone)
	if err != nil {
		return time.Time{}, false
	}

	return started, true
}

// darwin's process states and flags, from <sys/proc.h>: a zombie, and a
// process inside exit (`ps` shows it as E).
const (
	darwinZombie  = 5
	darwinExiting = 0x00002000
)

// Exiting is whether a darwin process with stat and flags is a zombie or on
// its way out. Such a husk keeps its pid and name while its parent has not
// reaped it, though it runs no session any more.
func Exiting(stat int8, flags int32) bool {
	return stat == darwinZombie || flags&darwinExiting != 0
}
