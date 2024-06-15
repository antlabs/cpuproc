package cpuproc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/cpu"
	"golang.org/x/sys/unix"
)

const prioProcess = 0 // linux/resource.h

var clockTicks = 100 // default value

var enableBootTimeCache bool

var ClocksPerSec = float64(100)

type PageFaultsStat struct {
	MinorFaults      uint64 `json:"minorFaults"`
	MajorFaults      uint64 `json:"majorFaults"`
	ChildMinorFaults uint64 `json:"childMinorFaults"`
	ChildMajorFaults uint64 `json:"childMajorFaults"`
}

type proc struct {
	set unix.CPUSet
	pid int32
}

func NewProcess(pid int32) *proc {
	var p proc
	if err := unix.SchedGetaffinity(0, &p.set); err != nil {
		return nil
	}
	p.pid = pid
	return &p
}

func parseStatLine(line string) (*TimesStat, error) {
	fields := strings.Fields(line)

	if len(fields) < 8 {
		return nil, errors.New("stat does not contain cpu info")
	}

	if !strings.HasPrefix(fields[0], "cpu") {
		return nil, errors.New("not contain cpu")
	}

	cpu := fields[0]
	if cpu == "cpu" {
		cpu = "cpu-total"
	}
	user, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return nil, err
	}
	nice, err := strconv.ParseFloat(fields[2], 64)
	if err != nil {
		return nil, err
	}
	system, err := strconv.ParseFloat(fields[3], 64)
	if err != nil {
		return nil, err
	}
	idle, err := strconv.ParseFloat(fields[4], 64)
	if err != nil {
		return nil, err
	}
	iowait, err := strconv.ParseFloat(fields[5], 64)
	if err != nil {
		return nil, err
	}
	irq, err := strconv.ParseFloat(fields[6], 64)
	if err != nil {
		return nil, err
	}
	softirq, err := strconv.ParseFloat(fields[7], 64)
	if err != nil {
		return nil, err
	}

	ct := &TimesStat{
		CPU:     cpu,
		User:    user / ClocksPerSec,
		Nice:    nice / ClocksPerSec,
		System:  system / ClocksPerSec,
		Idle:    idle / ClocksPerSec,
		Iowait:  iowait / ClocksPerSec,
		Irq:     irq / ClocksPerSec,
		Softirq: softirq / ClocksPerSec,
	}
	if len(fields) > 8 { // Linux >= 2.6.11
		steal, err := strconv.ParseFloat(fields[8], 64)
		if err != nil {
			return nil, err
		}
		ct.Steal = steal / ClocksPerSec
	}
	if len(fields) > 9 { // Linux >= 2.6.24
		guest, err := strconv.ParseFloat(fields[9], 64)
		if err != nil {
			return nil, err
		}
		ct.Guest = guest / ClocksPerSec
	}
	if len(fields) > 10 { // Linux >= 3.2.0
		guestNice, err := strconv.ParseFloat(fields[10], 64)
		if err != nil {
			return nil, err
		}
		ct.GuestNice = guestNice / ClocksPerSec
	}

	return ct, nil
}

func TimesWithContext(ctx context.Context, percpu bool) ([]TimesStat, error) {
	filename := HostProcWithContext(ctx, "stat")
	lines := []string{}
	if percpu {
		statlines, err := ReadLines(filename)
		if err != nil || len(statlines) < 2 {
			return []TimesStat{}, nil
		}
		for _, line := range statlines[1:] {
			if !strings.HasPrefix(line, "cpu") {
				break
			}
			lines = append(lines, line)
		}
	} else {
		lines, _ = ReadLinesOffsetN(filename, 0, 1)
	}

	ret := make([]TimesStat, 0, len(lines))

	for _, line := range lines {
		ct, err := parseStatLine(line)
		if err != nil {
			continue
		}
		ret = append(ret, *ct)

	}
	return ret, nil
}

func (p *proc) fillFromTIDStatWithContext(ctx context.Context, tid int32) (uint64, int32, *cpu.TimesStat, int64, uint32, int32, *PageFaultsStat, error) {
	pid := p.pid
	var statPath string

	if tid == -1 {
		statPath = HostProcWithContext(ctx, strconv.Itoa(int(pid)), "stat")
	} else {
		statPath = HostProcWithContext(ctx, strconv.Itoa(int(pid)), "task", strconv.Itoa(int(tid)), "stat")
	}

	contents, err := os.ReadFile(statPath)
	if err != nil {
		return 0, 0, nil, 0, 0, 0, nil, err
	}
	// Indexing from one, as described in `man proc` about the file /proc/[pid]/stat
	fields := splitProcStat(contents)

	terminal, err := strconv.ParseUint(fields[7], 10, 64)
	if err != nil {
		return 0, 0, nil, 0, 0, 0, nil, err
	}

	ppid, err := strconv.ParseInt(fields[4], 10, 32)
	if err != nil {
		return 0, 0, nil, 0, 0, 0, nil, err
	}
	utime, err := strconv.ParseFloat(fields[14], 64)
	if err != nil {
		return 0, 0, nil, 0, 0, 0, nil, err
	}

	stime, err := strconv.ParseFloat(fields[15], 64)
	if err != nil {
		return 0, 0, nil, 0, 0, 0, nil, err
	}

	// There is no such thing as iotime in stat file.  As an approximation, we
	// will use delayacct_blkio_ticks (aggregated block I/O delays, as per Linux
	// docs).  Note: I am assuming at least Linux 2.6.18
	var iotime float64
	if len(fields) > 42 {
		iotime, err = strconv.ParseFloat(fields[42], 64)
		if err != nil {
			iotime = 0 // Ancient linux version, most likely
		}
	} else {
		iotime = 0 // e.g. SmartOS containers
	}

	cpuTimes := &cpu.TimesStat{
		CPU:    "cpu",
		User:   utime / float64(clockTicks),
		System: stime / float64(clockTicks),
		Iowait: iotime / float64(clockTicks),
	}

	bootTime, _ := BootTimeWithContext(ctx, enableBootTimeCache)
	t, err := strconv.ParseUint(fields[22], 10, 64)
	if err != nil {
		return 0, 0, nil, 0, 0, 0, nil, err
	}
	ctime := (t / uint64(clockTicks)) + uint64(bootTime)
	createTime := int64(ctime * 1000)

	rtpriority, err := strconv.ParseInt(fields[18], 10, 32)
	if err != nil {
		return 0, 0, nil, 0, 0, 0, nil, err
	}
	if rtpriority < 0 {
		rtpriority = rtpriority*-1 - 1
	} else {
		rtpriority = 0
	}

	//	p.Nice = mustParseInt32(fields[18])
	// use syscall instead of parse Stat file
	snice, _ := unix.Getpriority(prioProcess, int(pid))
	nice := int32(snice) // FIXME: is this true?

	minFault, err := strconv.ParseUint(fields[10], 10, 64)
	if err != nil {
		return 0, 0, nil, 0, 0, 0, nil, err
	}
	cMinFault, err := strconv.ParseUint(fields[11], 10, 64)
	if err != nil {
		return 0, 0, nil, 0, 0, 0, nil, err
	}
	majFault, err := strconv.ParseUint(fields[12], 10, 64)
	if err != nil {
		return 0, 0, nil, 0, 0, 0, nil, err
	}
	cMajFault, err := strconv.ParseUint(fields[13], 10, 64)
	if err != nil {
		return 0, 0, nil, 0, 0, 0, nil, err
	}

	faults := &PageFaultsStat{
		MinorFaults:      minFault,
		MajorFaults:      majFault,
		ChildMinorFaults: cMinFault,
		ChildMajorFaults: cMajFault,
	}

	return terminal, int32(ppid), cpuTimes, createTime, uint32(rtpriority), nice, faults, nil
}

func (p *proc) fillFromStatWithContext(ctx context.Context) (uint64, int32, *cpu.TimesStat, int64, uint32, int32, *PageFaultsStat, error) {
	return p.fillFromTIDStatWithContext(ctx, -1)
}

func (p *proc) TimesWithContext(ctx context.Context) (*cpu.TimesStat, error) {
	_, _, cpuTimes, _, _, _, _, err := p.fillFromStatWithContext(ctx)
	if err != nil {
		return nil, err
	}
	return cpuTimes, nil
}

// Sleep awaits for provided interval.
// Can be interrupted by context cancellation.
func Sleep(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	select {
	case <-ctx.Done():
		if !timer.Stop() {
			<-timer.C
		}
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func Percent(interval time.Duration, percpu bool) ([]float64, error) {
	return PercentWithContext(context.Background(), interval, percpu)
}

func PercentTotal(interval time.Duration) (float64, error) {
	rv, err := Percent(interval, false)
	if err != nil {
		return 0, err
	}
	return rv[0], nil
}

func percentUsedFromLastCallWithContext(ctx context.Context, percpu bool) ([]float64, error) {
	cpuTimes, err := TimesWithContext(ctx, percpu)
	if err != nil {
		return nil, err
	}
	lastCPUPercent.Lock()
	defer lastCPUPercent.Unlock()
	var lastTimes []TimesStat
	if percpu {
		lastTimes = lastCPUPercent.lastPerCPUTimes
		lastCPUPercent.lastPerCPUTimes = cpuTimes
	} else {
		lastTimes = lastCPUPercent.lastCPUTimes
		lastCPUPercent.lastCPUTimes = cpuTimes
	}

	if lastTimes == nil {
		return nil, fmt.Errorf("error getting times for cpu percent. lastTimes was nil")
	}
	return calculateAllBusy(lastTimes, cpuTimes)
}

func PercentWithContext(ctx context.Context, interval time.Duration, percpu bool) ([]float64, error) {
	if interval <= 0 {
		return percentUsedFromLastCallWithContext(ctx, percpu)
	}

	// Get CPU usage at the start of the interval.
	cpuTimes1, err := TimesWithContext(ctx, percpu)
	if err != nil {
		return nil, err
	}

	if err := Sleep(ctx, interval); err != nil {
		return nil, err
	}

	// And at the end of the interval.
	cpuTimes2, err := TimesWithContext(ctx, percpu)
	if err != nil {
		return nil, err
	}

	return calculateAllBusy(cpuTimes1, cpuTimes2)
}

// CPUPercent returns how many percent of the CPU time this process uses
func (p *proc) cpuPercent() (float64, error) {
	return p.CPUPercentWithContext(context.Background())
}

func (p *proc) createTimeWithContext(ctx context.Context) (int64, error) {
	_, _, _, createTime, _, _, _, err := p.fillFromStatWithContext(ctx)
	if err != nil {
		return 0, err
	}
	return createTime, nil
}

func (p *proc) CPUPercentWithContext(ctx context.Context) (float64, error) {
	crt_time, err := p.createTimeWithContext(ctx)
	if err != nil {
		return 0, err
	}

	cput, err := p.TimesWithContext(ctx)
	if err != nil {
		return 0, err
	}

	created := time.Unix(0, crt_time*int64(time.Millisecond))
	totalTime := time.Since(created).Seconds()
	if totalTime <= 0 {
		return 0, nil
	}

	return 100 * cput.Total() / totalTime, nil
}

func (p *proc) CPUPercent() (float64, error) {

	total := p.set.Count()
	// proc, err := process.NewProcess(p.pid)
	// if err != nil {
	// 	return 0, err
	// }
	cpuPercent, err := p.cpuPercent()
	if err != nil {
		return 0, err
	}

	// fmt.Printf("total:%v, cpuPercent:%v\n", total, cpuPercent)
	return cpuPercent / (float64(total) * float64(100)), nil
}

func splitProcStat(content []byte) []string {
	nameStart := bytes.IndexByte(content, '(')
	nameEnd := bytes.LastIndexByte(content, ')')
	restFields := strings.Fields(string(content[nameEnd+2:])) // +2 skip ') '
	name := content[nameStart+1 : nameEnd]
	pid := strings.TrimSpace(string(content[:nameStart]))
	fields := make([]string, 3, len(restFields)+3)
	fields[1] = string(pid)
	fields[2] = string(name)
	fields = append(fields, restFields...)
	return fields
}
