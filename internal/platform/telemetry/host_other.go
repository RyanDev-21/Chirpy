//go:build !linux

package telemetry

// Host gauges are only reported on Linux (the servers).
func hostMemoryAvailable() int64     { return 0 }
func hostDiskFree(path string) int64 { return 0 }
