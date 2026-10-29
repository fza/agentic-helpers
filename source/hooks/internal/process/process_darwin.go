package process

import (
	"bytes"
	"context"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// ParentAndName is the parent pid and the executable's name, or zero and an
// empty name where the process is gone or exiting. The kernel keeps the name
// truncated to 16 bytes.
func (Table) ParentAndName(_ context.Context, pid int) (int, string) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || Exiting(info.Proc.P_stat, info.Proc.P_flag) {
		return 0, ""
	}

	return int(info.Eproc.Ppid), unix.ByteSliceToString(info.Proc.P_comm[:])
}

// StartTime identifies one run of a pid, so a reused pid never passes for the
// process that held it before. It is whole seconds, as claims record it.
func (Table) StartTime(_ context.Context, pid int) (time.Time, bool) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || Exiting(info.Proc.P_stat, info.Proc.P_flag) {
		return time.Time{}, false
	}

	seconds, _ := info.Proc.P_starttime.Unix()

	return time.Unix(seconds, 0), true
}

// Executables is the executable path of every process of this user whose
// name starts with prefix. kern.procargs2 opens with the argument count, then
// the path the process was started from.
func (Table) Executables(_ context.Context, prefix string) ([]string, error) {
	listed, err := unix.SysctlKinfoProcSlice("kern.proc.uid", os.Getuid())
	if err != nil {
		return nil, err
	}

	var paths []string

	for _, info := range listed {
		name := unix.ByteSliceToString(info.Proc.P_comm[:])
		if !strings.HasPrefix(name, prefix) || Exiting(info.Proc.P_stat, info.Proc.P_flag) {
			continue
		}

		raw, err := unix.SysctlRaw("kern.procargs2", int(info.Proc.P_pid))
		if err != nil || len(raw) <= 4 {
			continue
		}

		path, _, _ := bytes.Cut(raw[4:], []byte{0})
		paths = append(paths, string(path))
	}

	return paths, nil
}
