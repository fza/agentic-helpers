package process

import (
	"context"
	"os"
	"time"
)

var proc = Procfs{Root: os.DirFS("/proc")}

func (Table) ParentAndName(_ context.Context, pid int) (int, string) {
	return proc.ParentAndName(pid)
}

func (Table) StartTime(_ context.Context, pid int) (time.Time, bool) {
	return proc.StartTime(pid)
}
