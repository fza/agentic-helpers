package process

import (
	"bufio"
	"bytes"
	"io/fs"
	"strconv"
	"strings"
	"time"
)

// clockTicks is the kernel's USER_HZ, the unit of a start time in
// /proc/<pid>/stat. Every architecture Linux releases ship for fixes it at 100.
const clockTicks = 100

// Procfs reads the process table from Root, a Linux /proc.
type Procfs struct {
	Root fs.FS
}

func (procfs Procfs) ParentAndName(pid int) (int, string) {
	fields, name, readable := procfs.stat(pid)
	if !readable {
		return 0, ""
	}

	parent, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, ""
	}

	return parent, name
}

// StartTime is the boot time plus the ticks the process started after it, in
// whole seconds, as claims record it.
func (procfs Procfs) StartTime(pid int) (time.Time, bool) {
	fields, _, readable := procfs.stat(pid)
	if !readable {
		return time.Time{}, false
	}

	ticks, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return time.Time{}, false
	}

	booted, found := procfs.bootTime()
	if !found {
		return time.Time{}, false
	}

	return time.Unix(booted+ticks/clockTicks, 0), true
}

// stat splits /proc/<pid>/stat into the name, field 2, and every field after
// it, the state (field 3) first, through the start time (field 22) at least.
// The name sits in parentheses and may hold spaces and parentheses of its own,
// so it ends at the last closing one.
func (procfs Procfs) stat(pid int) ([]string, string, bool) {
	raw, err := fs.ReadFile(procfs.Root, strconv.Itoa(pid)+"/stat")
	if err != nil {
		return nil, "", false
	}

	opening := bytes.IndexByte(raw, '(')
	closing := bytes.LastIndexByte(raw, ')')
	if opening < 0 || closing < opening {
		return nil, "", false
	}

	fields := strings.Fields(string(raw[closing+1:]))
	if len(fields) < 20 {
		return nil, "", false
	}

	// A zombie or a dead process keeps its stat until reaped, though it runs
	// nothing any more.
	if fields[0] == "Z" || fields[0] == "X" {
		return nil, "", false
	}

	return fields, string(raw[opening+1 : closing]), true
}

// Executables is the executable path of every process whose name starts with
// prefix and whose `exe` link reads: a process of another user keeps it
// unreadable.
func (procfs Procfs) Executables(prefix string) ([]string, error) {
	listed, err := fs.ReadDir(procfs.Root, ".")
	if err != nil {
		return nil, err
	}

	var paths []string

	for _, entry := range listed {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}

		_, name := procfs.ParentAndName(pid)
		if !strings.HasPrefix(name, prefix) {
			continue
		}

		path, err := fs.ReadLink(procfs.Root, entry.Name()+"/exe")
		if err == nil {
			paths = append(paths, path)
		}
	}

	return paths, nil
}

func (procfs Procfs) bootTime() (int64, bool) {
	raw, err := fs.ReadFile(procfs.Root, "stat")
	if err != nil {
		return 0, false
	}

	lines := bufio.NewScanner(bytes.NewReader(raw))
	for lines.Scan() {
		value, found := strings.CutPrefix(lines.Text(), "btime ")
		if !found {
			continue
		}

		booted, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return 0, false
		}

		return booted, true
	}

	return 0, false
}
