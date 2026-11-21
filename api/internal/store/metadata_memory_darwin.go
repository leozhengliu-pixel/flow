package store

import "golang.org/x/sys/unix"

// physicalMemoryBytes is the machine's physical memory (hw.memsize), or 0.
func physicalMemoryBytes() int64 {
	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return int64(min(total, 1<<62))
}
