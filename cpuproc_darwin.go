package cpuproc

import (
	"context"
	"time"
)

type proc struct {
	// set unix.CPUSet
	pid int32
}

func (p *proc) CPUPercent() (float64, error) {
	return 0, nil
}

// 空函数
func NewProcess(pid int32) *proc {
	var p proc
	// if err := unix.SchedGetaffinity(0, &p.set); err != nil {
	// 	return nil
	// }
	// p.pid = pid
	return &p
}

func PercentTotal(interval time.Duration) (float64, error) {
	return 0.0, nil
}

func TimesWithContext(ctx context.Context, percpu bool) (rv []TimesStat, err error) {
	return
}
