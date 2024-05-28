package cpuproc

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// cachedBootTime must be accessed via atomic.Load/StoreUint64
var cachedBootTime uint64

var (
	cachedVirtMap   map[string]string
	cachedVirtMutex sync.RWMutex
	cachedVirtOnce  sync.Once
)

func PathExists(filename string) bool {
	if _, err := os.Stat(filename); err == nil {
		return true
	}
	return false
}

// ReadLines reads contents from a file and splits them by new lines.
// A convenience wrapper to ReadLinesOffsetN(filename, 0, -1).
func ReadLines(filename string) ([]string, error) {
	return ReadLinesOffsetN(filename, 0, -1)
}

// ReadLinesOffsetN reads contents from file and splits them by new line.
// The offset tells at which line number to start.
// The count determines the number of lines to read (starting from offset):
// n >= 0: at most n lines
// n < 0: whole file
func ReadLinesOffsetN(filename string, offset uint, n int) ([]string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return []string{""}, err
	}
	defer f.Close()

	var ret []string

	r := bufio.NewReader(f)
	for i := 0; i < n+int(offset) || n < 0; i++ {
		line, err := r.ReadString('\n')
		if err != nil {
			if err == io.EOF && len(line) > 0 {
				ret = append(ret, strings.Trim(line, "\n"))
			}
			break
		}
		if i < int(offset) {
			continue
		}
		ret = append(ret, strings.Trim(line, "\n"))
	}

	return ret, nil
}

func ReadLine(filename string, prefix string) (string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", err
		}
		if strings.HasPrefix(line, prefix) {
			return line, nil
		}
	}

	return "", nil
}

func handleBootTimeFileReadErr(err error) (uint64, error) {
	if os.IsPermission(err) {
		var info syscall.Sysinfo_t
		err := syscall.Sysinfo(&info)
		if err != nil {
			return 0, err
		}

		currentTime := time.Now().UnixNano() / int64(time.Second)
		t := currentTime - int64(info.Uptime)
		return uint64(t), nil
	}
	return 0, err
}

func readBootTimeStat(ctx context.Context) (uint64, error) {
	filename := HostProcWithContext(ctx, "stat")
	line, err := ReadLine(filename, "btime")
	if err != nil {
		return handleBootTimeFileReadErr(err)
	}
	if strings.HasPrefix(line, "btime") {
		f := strings.Fields(line)
		if len(f) != 2 {
			return 0, fmt.Errorf("wrong btime format")
		}
		b, err := strconv.ParseInt(f[1], 10, 64)
		if err != nil {
			return 0, err
		}
		t := uint64(b)
		return t, nil
	}
	return 0, fmt.Errorf("could not find btime")
}

func BootTimeWithContext(ctx context.Context, enableCache bool) (uint64, error) {
	if enableCache {
		t := atomic.LoadUint64(&cachedBootTime)
		if t != 0 {
			return t, nil
		}
	}

	system, role, err := VirtualizationWithContext(ctx)
	if err != nil {
		return 0, err
	}

	useStatFile := true
	if system == "lxc" && role == "guest" {
		// if lxc, /proc/uptime is used.
		useStatFile = false
	} else if system == "docker" && role == "guest" {
		// also docker, guest
		useStatFile = false
	}

	if useStatFile {
		t, err := readBootTimeStat(ctx)
		if err != nil {
			return 0, err
		}
		if enableCache {
			atomic.StoreUint64(&cachedBootTime, t)
		}
	}

	filename := HostProcWithContext(ctx, "uptime")
	lines, err := ReadLines(filename)
	if err != nil {
		return handleBootTimeFileReadErr(err)
	}
	if len(lines) != 1 {
		return 0, fmt.Errorf("wrong uptime format")
	}
	f := strings.Fields(lines[0])
	b, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0, err
	}
	currentTime := float64(time.Now().UnixNano()) / float64(time.Second)
	t := currentTime - b

	if enableCache {
		atomic.StoreUint64(&cachedBootTime, uint64(t))
	}

	return uint64(t), nil
}

func VirtualizationWithContext(ctx context.Context) (string, string, error) {
	var system, role string

	// if cached already, return from cache
	cachedVirtMutex.RLock() // unlock won't be deferred so concurrent reads don't wait for long
	if cachedVirtMap != nil {
		cachedSystem, cachedRole := cachedVirtMap["system"], cachedVirtMap["role"]
		cachedVirtMutex.RUnlock()
		return cachedSystem, cachedRole, nil
	}
	cachedVirtMutex.RUnlock()

	filename := HostProcWithContext(ctx, "xen")
	if PathExists(filename) {
		system = "xen"
		role = "guest" // assume guest

		if PathExists(filepath.Join(filename, "capabilities")) {
			contents, err := ReadLines(filepath.Join(filename, "capabilities"))
			if err == nil {
				if slices.Contains(contents, "control_d") {
					role = "host"
				}
			}
		}
	}

	filename = HostProcWithContext(ctx, "modules")
	if PathExists(filename) {
		contents, err := ReadLines(filename)
		if err == nil {
			if slices.Contains(contents, "kvm") {
				system = "kvm"
				role = "host"
			} else if slices.Contains(contents, "hv_util") {
				system = "hyperv"
				role = "guest"
			} else if slices.Contains(contents, "vboxdrv") {
				system = "vbox"
				role = "host"
			} else if slices.Contains(contents, "vboxguest") {
				system = "vbox"
				role = "guest"
			} else if slices.Contains(contents, "vmware") {
				system = "vmware"
				role = "guest"
			}
		}
	}

	filename = HostProcWithContext(ctx, "cpuinfo")
	if PathExists(filename) {
		contents, err := ReadLines(filename)
		if err == nil {
			if slices.Contains(contents, "QEMU Virtual CPU") ||
				slices.Contains(contents, "Common KVM processor") ||
				slices.Contains(contents, "Common 32-bit KVM processor") {
				system = "kvm"
				role = "guest"
			}
		}
	}

	filename = HostProcWithContext(ctx, "bus/pci/devices")
	if PathExists(filename) {
		contents, err := ReadLines(filename)
		if err == nil {
			if slices.Contains(contents, "virtio-pci") {
				role = "guest"
			}
		}
	}

	filename = HostProcWithContext(ctx)
	if PathExists(filepath.Join(filename, "bc", "0")) {
		system = "openvz"
		role = "host"
	} else if PathExists(filepath.Join(filename, "vz")) {
		system = "openvz"
		role = "guest"
	}

	// not use dmidecode because it requires root
	if PathExists(filepath.Join(filename, "self", "status")) {
		contents, err := ReadLines(filepath.Join(filename, "self", "status"))
		if err == nil {
			if slices.Contains(contents, "s_context:") ||
				slices.Contains(contents, "VxID:") {
				system = "linux-vserver"
			}
			// TODO: guest or host
		}
	}

	if PathExists(filepath.Join(filename, "1", "environ")) {
		contents, err := ReadFile(filepath.Join(filename, "1", "environ"))

		if err == nil {
			if strings.Contains(contents, "container=lxc") {
				system = "lxc"
				role = "guest"
			}
		}
	}

	if PathExists(filepath.Join(filename, "self", "cgroup")) {
		contents, err := ReadLines(filepath.Join(filename, "self", "cgroup"))
		if err == nil {
			if slices.Contains(contents, "lxc") {
				system = "lxc"
				role = "guest"
			} else if slices.Contains(contents, "docker") {
				system = "docker"
				role = "guest"
			} else if slices.Contains(contents, "machine-rkt") {
				system = "rkt"
				role = "guest"
			} else if PathExists("/usr/bin/lxc-version") {
				system = "lxc"
				role = "host"
			}
		}
	}

	if PathExists(HostEtcWithContext(ctx, "os-release")) {
		p, _, err := GetOSReleaseWithContext(ctx)
		if err == nil && p == "coreos" {
			system = "rkt" // Is it true?
			role = "host"
		}
	}

	if PathExists(HostRootWithContext(ctx, ".dockerenv")) {
		system = "docker"
		role = "guest"
	}

	// before returning for the first time, cache the system and role
	cachedVirtOnce.Do(func() {
		cachedVirtMutex.Lock()
		defer cachedVirtMutex.Unlock()
		cachedVirtMap = map[string]string{
			"system": system,
			"role":   role,
		}
	})

	return system, role, nil
}

// Remove quotes of the source string
func trimQuotes(s string) string {
	if len(s) >= 2 {
		if s[0] == '"' && s[len(s)-1] == '"' {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func GetOSReleaseWithContext(ctx context.Context) (platform string, version string, err error) {
	contents, err := ReadLines(HostEtcWithContext(ctx, "os-release"))
	if err != nil {
		return "", "", nil // return empty
	}
	for _, line := range contents {
		field := strings.Split(line, "=")
		if len(field) < 2 {
			continue
		}
		switch field[0] {
		case "ID": // use ID for lowercase
			platform = trimQuotes(field[1])
		case "VERSION_ID":
			version = trimQuotes(field[1])
		}
	}

	// cleanup amazon ID
	if platform == "amzn" {
		platform = "amazon"
	}

	return platform, version, nil
}
