package anikohub

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// System is how the computer the hub runs on is doing, for its dashboard.
// Read from Linux's /proc and /sys (elsewhere what can't be read is 0).
type System struct {
	Model    string  `json:"model"`    // "Raspberry Pi 4 Model B Rev 1.4"
	Hostname string  `json:"hostname"` // the computer's name
	Arch     string  `json:"arch"`
	Cores    int     `json:"cores"`
	CPU      float64 `json:"cpu"`      // all cores, 0-100, since the last look
	AnikoCPU float64 `json:"anikoCpu"` // the hub's own share, 0-100
	Load     float64 `json:"load"`     // 1-minute load average
	MemTotal int64   `json:"memTotal"`
	MemUsed  int64   `json:"memUsed"`
	AnikoMem int64   `json:"anikoMem"` // the hub's own (resident)
	Temp     float64 `json:"temp"`     // °C (0: unknown)
	// Throttled: the Pi slows itself down (too hot, or too little power).
	Throttled bool    `json:"throttled"`
	Uptime    int64   `json:"uptime"`  // seconds the computer has been on
	Started   int64   `json:"started"` // when the hub started (ms)
	NetRx     float64 `json:"netRx"`   // bytes a second, since the last look
	NetTx     float64 `json:"netTx"`
	Disks     []Disk  `json:"disks"`
}

// Disk is a library folder's (or the hub's data's) disk.
type Disk struct {
	Path  string `json:"path"`
	Total int64  `json:"total"`
	Free  int64  `json:"free"`
}

// Sampler keeps the last look, to tell rates (CPU, network) since then.
type Sampler struct {
	mu       sync.Mutex
	started  time.Time
	at       time.Time
	cpuIdle  uint64
	cpuAll   uint64
	anikoCPU uint64 // ticks
	rx, tx   uint64
}

func NewSampler() *Sampler { return &Sampler{started: time.Now()} }

// Look reads the system now (folders: whose disks to tell).
func (s *Sampler) Look(folders []string) System {
	sys := System{Arch: runtime.GOARCH, Cores: runtime.NumCPU(), Started: s.started.UnixMilli()}
	sys.Hostname, _ = os.Hostname()
	if b, err := os.ReadFile("/proc/device-tree/model"); err == nil {
		sys.Model = strings.TrimRight(string(b), "\x00\n ")
	}
	idle, all := cpuTicks()
	aniko := selfTicks()
	rx, tx := netBytes()
	now := time.Now()

	s.mu.Lock()
	if !s.at.IsZero() {
		if d := all - s.cpuAll; d > 0 && all >= s.cpuAll {
			sys.CPU = round1(100 * float64(d-(idle-s.cpuIdle)) / float64(d))
			// The hub's ticks against all of the cores' ticks.
			sys.AnikoCPU = round1(100 * float64(aniko-s.anikoCPU) / float64(d))
		}
		if secs := now.Sub(s.at).Seconds(); secs > 0 && rx >= s.rx && tx >= s.tx {
			sys.NetRx = float64(rx-s.rx) / secs
			sys.NetTx = float64(tx-s.tx) / secs
		}
	}
	s.at, s.cpuIdle, s.cpuAll, s.anikoCPU, s.rx, s.tx = now, idle, all, aniko, rx, tx
	s.mu.Unlock()

	sys.MemTotal, sys.MemUsed = memory()
	sys.AnikoMem = selfMemory()
	sys.Temp = temperature()
	sys.Throttled = throttled()
	sys.Load = loadAverage()
	sys.Uptime = uptime()
	seen := map[uint64]bool{}
	for _, f := range folders {
		if d, id, ok := diskOf(f); ok && !seen[id] {
			seen[id] = true
			sys.Disks = append(sys.Disks, d)
		}
	}
	if sys.Disks == nil {
		sys.Disks = []Disk{}
	}
	return sys
}

func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }

// cpuTicks: the idle ticks and all ticks of all cores (/proc/stat).
func cpuTicks() (idle, all uint64) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return 0, 0
	}
	fields := strings.Fields(sc.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0
	}
	for i, v := range fields[1:] {
		n, _ := strconv.ParseUint(v, 10, 64)
		all += n
		if i == 3 || i == 4 { // idle, iowait
			idle += n
		}
	}
	return idle, all
}

// selfTicks: the hub's own CPU ticks (user + system).
func selfTicks() uint64 {
	b, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0
	}
	// After the name (in parentheses, may hold spaces).
	s := string(b)
	if i := strings.LastIndexByte(s, ')'); i >= 0 {
		s = s[i+1:]
	}
	f := strings.Fields(s)
	if len(f) < 13 {
		return 0
	}
	u, _ := strconv.ParseUint(f[11], 10, 64)
	k, _ := strconv.ParseUint(f[12], 10, 64)
	return u + k
}

func memory() (total, used int64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	var avail int64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		n, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(v), " kB"), 10, 64)
		switch k {
		case "MemTotal":
			total = n << 10
		case "MemAvailable":
			avail = n << 10
		}
	}
	return total, max(0, total-avail)
}

func selfMemory() int64 {
	b, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return int64(m.Sys)
	}
	f := strings.Fields(string(b))
	if len(f) < 2 {
		return 0
	}
	pages, _ := strconv.ParseInt(f[1], 10, 64)
	return pages * int64(os.Getpagesize())
}

func temperature() float64 {
	b, err := os.ReadFile("/sys/class/thermal/thermal_zone0/temp")
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0
	}
	return round1(float64(n) / 1000)
}

// throttled: the Pi's firmware says it's slowing down now (under-voltage or
// heat), as Raspberry Pi OS tells it (no vcgencmd needed).
func throttled() bool {
	b, err := os.ReadFile("/sys/devices/platform/soc/soc:firmware/get_throttled")
	if err != nil {
		return false
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 16, 32)
	return err == nil && n&0b0111 != 0
}

func loadAverage() float64 {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return v
}

func uptime() int64 {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0
	}
	v, _ := strconv.ParseFloat(f[0], 64)
	return int64(v)
}

// netBytes: received and sent on the network cards (not loopback).
func netBytes() (rx, tx uint64) {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		name, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		if name == "lo" || strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "br-") {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) < 9 {
			continue
		}
		r, _ := strconv.ParseUint(fields[0], 10, 64)
		t, _ := strconv.ParseUint(fields[8], 10, 64)
		rx += r
		tx += t
	}
	return rx, tx
}
