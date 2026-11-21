//go:build linux

package telemetry

import "testing"

func TestHostGauges(t *testing.T) {
	if hostMemoryAvailable() <= 0 || hostDiskFree("/") <= 0 {
		t.Fatalf("memory %d, disk %d", hostMemoryAvailable(), hostDiskFree("/"))
	}
}
