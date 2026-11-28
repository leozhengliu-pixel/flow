package telemetry

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

type cgroupReader struct {
	leaf, root, dir string
	version         int
	current, high   *os.File // high is the v2 peak descriptor, kept across resets
	statm           *os.File
	peakMu          sync.Mutex
	resettable      bool
}

var mountEscapes = strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)

func locateCgroup(groups, mounts string) (string, string, int) {
	var v1, v2 string
	for line := range strings.SplitSeq(groups, "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 || !filepath.IsAbs(parts[2]) {
			continue
		}
		switch {
		case parts[0] == "0" && parts[1] == "":
			v2 = parts[2]
		case strings.Contains(","+parts[1]+",", ",memory,"):
			v1 = parts[2]
		}
	}
	versions := [2]int{2, 1}
	if v1 != "" {
		// Hybrid systems list both hierarchies. The explicit memory
		// controller in /proc/self/cgroup is the authoritative one.
		versions = [2]int{1, 2}
	}
	for _, version := range versions {
		var namespaceRoot string
		for line := range strings.SplitSeq(mounts, "\n") {
			before, after, ok := strings.Cut(line, " - ")
			mount, fs := strings.Fields(before), strings.Fields(after)
			if !ok || len(mount) < 5 || len(fs) < 3 {
				continue
			}
			var group string
			switch {
			case version == 2 && fs[0] == "cgroup2":
				group = v2
			case version == 1 && fs[0] == "cgroup" && strings.Contains(","+fs[2]+",", ",memory,"):
				group = v1
			}
			if group == "" {
				continue
			}
			root := mountEscapes.Replace(mount[4])
			rel, err := filepath.Rel(mountEscapes.Replace(mount[3]), group)
			if err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
				return filepath.Join(root, rel), root, version
			}
			if group == "/" && namespaceRoot == "" {
				namespaceRoot = root // the mount may report the host-side root
			}
		}
		if namespaceRoot != "" {
			return namespaceRoot, namespaceRoot, version
		}
	}
	return "", "", 0
}

func openCgroup(dir, root string, version int) *cgroupReader {
	c := &cgroupReader{leaf: dir, root: root, version: version}
	if version != 0 {
		selected, _ := c.limitingGroup()
		c.setGroup(selected)
	}
	return c
}

func (c *cgroupReader) setGroup(dir string) {
	if c.dir == dir {
		return
	}
	c.peakMu.Lock()
	defer c.peakMu.Unlock()
	for _, f := range []*os.File{c.current, c.high} {
		if f != nil {
			_ = f.Close()
		}
	}
	c.dir = dir
	switch c.version {
	case 2:
		c.current, _ = os.Open(filepath.Join(dir, "memory.current"))
		name := filepath.Join(dir, "memory.peak")
		c.high, _ = os.OpenFile(name, os.O_RDWR, 0)
		c.resettable = c.high != nil
		if c.high == nil {
			c.high, _ = os.Open(name)
		}
	case 1:
		c.current, _ = os.Open(filepath.Join(dir, "memory.usage_in_bytes"))
		c.high, _ = os.Open(filepath.Join(dir, "memory.max_usage_in_bytes"))
	}
}

func (c *cgroupReader) close() {
	c.peakMu.Lock()
	defer c.peakMu.Unlock()
	for _, f := range []*os.File{c.current, c.high, c.statm} {
		if f != nil {
			_ = f.Close()
		}
	}
}

func fileNumber(f *os.File) int64 {
	if f == nil {
		return -1
	}
	var raw [48]byte
	n, _ := f.ReadAt(raw[:], 0)
	return parseNumber(raw[:n])
}

func parseNumber(raw []byte) int64 {
	n, err := strconv.ParseInt(string(bytes.TrimSpace(raw)), 10, 64)
	if err != nil || n < 0 {
		return -1
	}
	return n
}

func (c *cgroupReader) peak(reset bool) (int64, int) {
	c.peakMu.Lock()
	defer c.peakMu.Unlock()
	value := fileNumber(c.high)
	if reset && c.resettable {
		_, err := c.high.Seek(0, 0)
		if err == nil {
			_, err = c.high.WriteString("0")
		}
		if err != nil {
			c.resettable = false
		}
	}
	if c.resettable {
		return value, 1
	}
	return value, 0
}

func (c *cgroupReader) fastSample(s *readings) {
	if n := fileNumber(c.current); n >= 0 {
		s.set(cgroupCurrent, n)
	}
	if c.statm != nil {
		var raw [128]byte
		n, _ := c.statm.ReadAt(raw[:], 0)
		if _, resident, ok := bytes.Cut(raw[:n], []byte(" ")); ok {
			pages, _, _ := bytes.Cut(resident, []byte(" "))
			if p := parseNumber(pages); p >= 0 {
				s.set(rss, p*int64(os.Getpagesize()))
			}
		}
	}
}

func (c *cgroupReader) limitingGroup() (string, int64) {
	name := "memory.max"
	if c.version == 1 {
		name = "memory.limit_in_bytes"
	}
	selected, limit := c.leaf, int64(-1)
	for dir := c.leaf; ; dir = filepath.Dir(dir) {
		raw, _ := os.ReadFile(filepath.Join(dir, name))
		// An ancestor with the same limit also charges all sibling usage.
		if n := parseNumber(raw); n > 0 && n < 1<<60 && (limit < 0 || n <= limit) {
			selected, limit = dir, n
		}
		if dir == c.root || dir == "/" || dir == "." {
			break
		}
	}
	return selected, limit
}

func (c *cgroupReader) slowSample(s *readings) (float64, float64) {
	if c.version == 0 {
		return math.NaN(), math.NaN()
	}
	dir, limit := c.limitingGroup()
	c.setGroup(dir)
	if limit >= 0 {
		s.set(cgroupLimit, limit)
	}
	if c.version == 1 {
		for name, k := range map[string]field{"memory.soft_limit_in_bytes": cgroupHigh, "memory.failcnt": cgroupEventsMax} {
			raw, _ := os.ReadFile(filepath.Join(c.dir, name))
			if n := parseNumber(raw); n >= 0 && n < 1<<60 {
				s.set(k, n)
			}
		}
		raw, _ := os.ReadFile(filepath.Join(c.dir, "memory.oom_control"))
		readCounters(s, raw, map[string]field{"oom_kill": cgroupOOMKill})
		return math.NaN(), math.NaN()
	}
	raw, _ := os.ReadFile(filepath.Join(c.dir, "memory.high"))
	if n := parseNumber(raw); n >= 0 {
		s.set(cgroupHigh, n)
	}
	raw, _ = os.ReadFile(filepath.Join(c.dir, "memory.events"))
	readCounters(s, raw, map[string]field{"high": cgroupEventsHigh, "max": cgroupEventsMax, "oom": cgroupOOM, "oom_kill": cgroupOOMKill})
	raw, _ = os.ReadFile(filepath.Join(c.dir, "memory.pressure"))
	return readPSI(s, raw)
}

func readCounters(s *readings, raw []byte, keys map[string]field) {
	for line := range strings.SplitSeq(string(raw), "\n") {
		name, value, ok := strings.Cut(line, " ")
		if k, listed := keys[name]; ok && listed {
			if n := parseNumber([]byte(value)); n >= 0 {
				s.set(k, n)
			}
		}
	}
}

func readPSI(s *readings, raw []byte) (float64, float64) {
	some, full := math.NaN(), math.NaN()
	for line := range strings.SplitSeq(string(raw), "\n") {
		words := strings.Fields(line)
		if len(words) == 0 || words[0] != "some" && words[0] != "full" {
			continue
		}
		k := cgroupSomeTotal
		if words[0] == "full" {
			k = cgroupFullTotal
		}
		for _, word := range words[1:] {
			if value, ok := strings.CutPrefix(word, "avg10="); ok {
				if n, err := strconv.ParseFloat(value, 64); err == nil && n >= 0 && n <= 100 {
					if k == cgroupSomeTotal {
						some = n
					} else {
						full = n
					}
				}
			}
			if value, ok := strings.CutPrefix(word, "total="); ok {
				if n := parseNumber([]byte(value)); n >= 0 {
					s.set(k, n)
				}
			}
		}
	}
	return some, full
}
