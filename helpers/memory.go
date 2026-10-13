package helpers

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

// ErrMemoryBudget is returned when a scan cannot fit in its memory budget.
var ErrMemoryBudget = errors.New("scan does not fit in the memory budget")

// ParseByteSize parses a size such as "512M", "2G", "1.5GiB" or a plain byte
// count. Units are binary (1K = 1024 bytes) and case-insensitive.
func ParseByteSize(s string) (int64, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	s = strings.TrimSuffix(strings.TrimSuffix(s, "B"), "I")
	if s == "" {
		return 0, errors.New("empty size")
	}
	shift := 0
	switch s[len(s)-1] {
	case 'K':
		shift = 10
	case 'M':
		shift = 20
	case 'G':
		shift = 30
	case 'T':
		shift = 40
	}
	if shift > 0 {
		s = s[:len(s)-1]
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(n) {
		return 0, fmt.Errorf("not a size: use a byte count or a number with K, M, G or T")
	}
	n *= float64(int64(1) << shift)
	if n < 0 || n >= math.MaxInt64 {
		return 0, errors.New("size out of range")
	}
	return int64(n), nil
}

// SystemMemoryBudget returns the default memory budget for a scan: half of the
// memory this process is allowed to use, so a scan cannot starve the machine or
// its container. It returns 0 (no budget) where that cannot be determined —
// the limits are read from Linux's /proc and cgroup files.
func SystemMemoryBudget() int64 {
	limit := int64(0)
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		for line := range strings.SplitSeq(string(data), "\n") {
			if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "MemTotal:" {
				if kb, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
					limit = kb << 10
				}
				break
			}
		}
	}
	// A container's limit is usually far below the host's memory.
	if data, err := os.ReadFile("/sys/fs/cgroup/memory.max"); err == nil {
		if cg, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64); err == nil && cg > 0 && (limit == 0 || cg < limit) {
			limit = cg
		}
	}
	return limit / 2
}
