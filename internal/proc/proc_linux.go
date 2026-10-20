package proc

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// startTime reads the process's start, in clock ticks since boot, from
// /proc/<pid>/stat, and the boot time from /proc/stat.
func startTime(pid int) (time.Time, error) {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return time.Time{}, fmt.Errorf("reading process %d: %w", pid, err)
	}
	ticks, err := parseStartTicks(stat)
	if err != nil {
		return time.Time{}, fmt.Errorf("reading process %d: %w", pid, err)
	}
	system, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, fmt.Errorf("reading boot time: %w", err)
	}
	boot, err := parseBootTime(system)
	if err != nil {
		return time.Time{}, err
	}
	return boot.Add(ticksToDuration(ticks)), nil
}
