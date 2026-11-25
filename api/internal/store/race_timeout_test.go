package store

import "time"

// raceWatchdog scales a test's hang watchdog for the race detector. These
// deadlines only turn a pool-one deadlock or a hang into a prompt failure;
// they are not performance bounds. Under -race the JSON scanning and chunked
// SQL reads they cover run about 10-15x slower (measured 2026-11-17: the
// application access snapshot section 0.53s -> 8.2s, the bounded personal
// scrub 4.5s -> 45s), which crossed the fixed 10s/30s watchdogs on CI.
func raceWatchdog(d time.Duration) time.Duration {
	if raceDetector {
		return 10 * d
	}
	return d
}
