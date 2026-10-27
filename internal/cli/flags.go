package cli

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// parseDays reads a retention such as 36h, 1h30m or 14d. Go durations have no day unit, so a whole number of days
// with the suffix d is accepted as well. Negative values are an error; zero is allowed and means "off".
func parseDays(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty duration")
	}
	if num, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.ParseUint(num, 10, 63)
		if err != nil {
			return 0, fmt.Errorf("%q is not a whole number of days", s)
		}
		if days > uint64(math.MaxInt64/int64(24*time.Hour)) {
			return 0, fmt.Errorf("%q is too long", s)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration such as 36h or 14d", s)
	}
	if d < 0 {
		return 0, fmt.Errorf("%q is negative", s)
	}
	return d, nil
}

// sizeUnits are the binary units parseSize accepts, in lower case.
var sizeUnits = []struct {
	suffix string
	bytes  float64
}{{"kib", 1 << 10}, {"mib", 1 << 20}, {"gib", 1 << 30}, {"tib", 1 << 40}}

// parseSize reads a size in bytes such as 1073741824, 512MiB or 1.5GiB. The units are the binary ones (KiB, MiB,
// GiB, TiB), in any case. Negative values and sizes that do not fit in an int64 are an error; zero is allowed.
func parseSize(s string) (int64, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	num, unit := s, 1.0
	for _, u := range sizeUnits {
		if n, ok := strings.CutSuffix(s, u.suffix); ok {
			num, unit = strings.TrimSpace(n), u.bytes
			break
		}
	}
	if num == "" || strings.Trim(num, "0123456789.") != "" {
		return 0, fmt.Errorf("%q is not a size such as 512MiB or 1GiB", s)
	}
	value, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a size such as 512MiB or 1GiB", s)
	}
	bytes := value * unit
	if bytes >= math.MaxInt64 {
		return 0, fmt.Errorf("%q is too large", s)
	}
	return int64(bytes), nil
}
