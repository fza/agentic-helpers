package process

import (
	"context"
	"time"

	"golang.org/x/sys/unix"
)

// ParentAndName is the parent pid and the executable's name, or zero and an
// empty name where the process is gone. The kernel keeps the name truncated to
// 16 bytes.
func (Table) ParentAndName(_ context.Context, pid int) (int, string) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, ""
	}

	return int(info.Eproc.Ppid), unix.ByteSliceToString(info.Proc.P_comm[:])
}

// StartTime identifies one run of a pid, so a reused pid never passes for the
// process that held it before. It is whole seconds, as claims record it.
func (Table) StartTime(_ context.Context, pid int) (time.Time, bool) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return time.Time{}, false
	}

	seconds, _ := info.Proc.P_starttime.Unix()

	return time.Unix(seconds, 0), true
}
