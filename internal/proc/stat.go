package proc

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// ticksPerSecond is Linux's USER_HZ, the unit of /proc/<pid>/stat's start
// time, fixed at 100 for user space.
const ticksPerSecond = 100

// startTimeField is the index of starttime (field 22) among the fields
// after the parenthesized command name, which starts with field 3.
const startTimeField = 22 - 3

var errBadStat = errors.New("unexpected /proc format")

// parseStartTicks reads starttime from a /proc/<pid>/stat line. The
// command name may hold spaces and parentheses, so fields are counted from
// its last closing parenthesis.
func parseStartTicks(stat []byte) (int64, error) {
	_, rest, ok := bytes.CutLast(stat, []byte(")"))
	if !ok {
		return 0, fmt.Errorf("%w: no command name in stat", errBadStat)
	}
	fields := bytes.Fields(rest)
	if len(fields) <= startTimeField {
		return 0, fmt.Errorf("%w: %d fields in stat", errBadStat, len(fields))
	}
	ticks, err := strconv.ParseInt(string(fields[startTimeField]), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: starttime %q", errBadStat, fields[startTimeField])
	}
	return ticks, nil
}

// parseBootTime reads the btime line, seconds since the epoch, from
// /proc/stat.
func parseBootTime(system []byte) (time.Time, error) {
	sc := bufio.NewScanner(bytes.NewReader(system))
	for sc.Scan() {
		value, ok := bytes.CutPrefix(sc.Bytes(), []byte("btime "))
		if !ok {
			continue
		}
		secs, err := strconv.ParseInt(string(bytes.TrimSpace(value)), 10, 64)
		if err != nil {
			return time.Time{}, fmt.Errorf("%w: btime %q", errBadStat, value)
		}
		return time.Unix(secs, 0), nil
	}
	return time.Time{}, fmt.Errorf("%w: no btime in /proc/stat", errBadStat)
}

func ticksToDuration(ticks int64) time.Duration {
	return time.Duration(ticks) * time.Second / ticksPerSecond
}
