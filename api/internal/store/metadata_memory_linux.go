package store

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// physicalMemoryBytes is the machine's physical memory, or 0.
func physicalMemoryBytes() int64 {
	var info syscall.Sysinfo_t
	if err := syscall.Sysinfo(&info); err != nil {
		return 0
	}
	return int64(min(uint64(info.Totalram)*uint64(info.Unit), 1<<62))
}

func cgroupMemoryLimit() int64 {
	groups, _ := os.ReadFile("/proc/self/cgroup")
	mounts, _ := os.ReadFile("/proc/self/mountinfo")
	return cgroupMemoryLimitAt(string(groups), string(mounts), os.ReadFile)
}

// Walk from the process's group to its mount root. A child may say "max"
// while an ancestor limits the combined memory use of its descendants.
func cgroupMemoryLimitAt(groups, mounts string, read func(string) ([]byte, error)) int64 {
	var v1, v2 string
	for line := range strings.SplitSeq(groups, "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 || !filepath.IsAbs(parts[2]) {
			continue
		}
		if parts[0] == "0" && parts[1] == "" {
			v2 = parts[2]
		} else if strings.Contains(","+parts[1]+",", ",memory,") {
			v1 = parts[2]
		}
	}
	var limit int64
	for line := range strings.SplitSeq(mounts, "\n") {
		before, after, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		mount, fs := strings.Fields(before), strings.Fields(after)
		if len(mount) < 5 || len(fs) < 3 {
			continue
		}
		var group, file string
		switch {
		case fs[0] == "cgroup2":
			group, file = v2, "memory.max"
		case fs[0] == "cgroup" && strings.Contains(","+fs[2]+",", ",memory,"):
			group, file = v1, "memory.limit_in_bytes"
		}
		if group == "" {
			continue
		}
		root, dir := cgroupUnescape.Replace(mount[3]), cgroupUnescape.Replace(mount[4])
		rel, err := filepath.Rel(root, group)
		if err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			continue
		}
		for path := filepath.Join(dir, rel); ; path = filepath.Dir(path) {
			raw, err := read(filepath.Join(path, file))
			if err == nil {
				// v1 uses a very large sentinel for unlimited.
				n, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
				if err == nil && n > 0 && n < 1<<60 && (limit == 0 || n < limit) {
					limit = n
				}
			}
			if path == dir || path == "/" {
				break
			}
		}
	}
	return limit
}

var cgroupUnescape = strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
