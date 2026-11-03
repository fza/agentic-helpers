package process

import (
	"context"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

var proc = Procfs{Root: os.DirFS("/proc")}

func (Table) ParentAndName(_ context.Context, pid int) (int, string) {
	return proc.ParentAndName(pid)
}

func (Table) StartTime(_ context.Context, pid int) (time.Time, bool) {
	return proc.StartTime(pid)
}

// Terminal is the device file of pid's controlling terminal, empty where it
// has none, is gone, or the file at the name its device takes is another
// device.
func (Table) Terminal(_ context.Context, pid int) string {
	major, minor, found := proc.TerminalDevice(pid)
	if !found {
		return ""
	}

	path := LinuxTerminal(major, minor)
	if path == "" {
		return ""
	}

	var held unix.Stat_t

	err := unix.Stat(path, &held)
	if err != nil || held.Rdev != unix.Mkdev(major, minor) {
		return ""
	}

	return path
}

func (Table) Executables(_ context.Context, prefix string) ([]string, error) {
	return proc.Executables(prefix)
}
