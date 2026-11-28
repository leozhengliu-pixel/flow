//go:build linux

package telemetry

import "os"

func platformCgroup() *cgroupReader {
	groups, _ := os.ReadFile("/proc/self/cgroup")
	mounts, _ := os.ReadFile("/proc/self/mountinfo")
	dir, root, version := locateCgroup(string(groups), string(mounts))
	c := openCgroup(dir, root, version)
	c.statm, _ = os.Open("/proc/self/statm")
	return c
}
