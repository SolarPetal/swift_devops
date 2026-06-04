package service

import "testing"

func TestParseHostMetricsOutput(t *testing.T) {
	out := `__CPU1__
cpu  100 0 100 800 0 0 0 0 0 0
__CPU2__
cpu  120 0 120 860 0 0 0 0 0 0
__MEM__
MemTotal:        8000000 kB
MemFree:         1000000 kB
MemAvailable:    6000000 kB
Buffers:          100000 kB
__DISK__
Filesystem     1024-blocks    Used Available Capacity Mounted on
/dev/root          10000000 4200000   5800000      42% /
__LOAD__
1.23 0.80 0.41 1/234 5678
`
	got, err := parseHostMetricsOutput(out)
	if err != nil {
		t.Fatalf("parse metrics: %v", err)
	}
	if got.CPUUsage < 39.9 || got.CPUUsage > 40.1 {
		t.Fatalf("cpu usage = %.2f, want 40.0", got.CPUUsage)
	}
	if got.MemoryUsage < 24.9 || got.MemoryUsage > 25.1 {
		t.Fatalf("memory usage = %.2f, want 25.0", got.MemoryUsage)
	}
	if got.DiskUsage != 42 {
		t.Fatalf("disk usage = %.2f, want 42", got.DiskUsage)
	}
	if got.Load1 != 1.23 {
		t.Fatalf("load1 = %.2f, want 1.23", got.Load1)
	}
}

func TestParseHostMetricsOutputRejectsIncompleteSnapshot(t *testing.T) {
	_, err := parseHostMetricsOutput(`__CPU1__
cpu  1 0 1 8 0 0 0 0
__CPU2__
cpu  2 0 2 9 0 0 0 0
`)
	if err == nil {
		t.Fatal("incomplete metrics output should fail")
	}
}
