//go:build !linux

package telemetry

func platformCgroup() *cgroupReader { return nil }
