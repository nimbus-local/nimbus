package scheduler

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// nextFireTime returns the next time after 'from' that the expression should fire.
// Supports rate(N unit) and cron(min hour dom month dow year) (AWS Scheduler format).
func nextFireTime(expr string, from time.Time) (time.Time, error) {
	expr = strings.TrimSpace(expr)
	switch {
	case strings.HasPrefix(expr, "rate("):
		d, err := parseRateDuration(expr)
		if err != nil {
			return time.Time{}, err
		}
		return from.Add(d), nil
	case strings.HasPrefix(expr, "cron("):
		spec, err := parseCronExpression(expr)
		if err != nil {
			return time.Time{}, err
		}
		return nextCronTime(spec, from)
	default:
		return time.Time{}, fmt.Errorf("unsupported expression: %s", expr)
	}
}

// --- Rate expressions ---

func parseRateDuration(expr string) (time.Duration, error) {
	inner := strings.TrimSuffix(strings.TrimPrefix(expr, "rate("), ")")
	parts := strings.Fields(inner)
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid rate expression: %s", expr)
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("invalid rate value: %s", parts[0])
	}
	unit := strings.ToLower(strings.TrimSuffix(parts[1], "s")) // strip trailing 's' for plural
	switch unit {
	case "minute":
		return time.Duration(n) * time.Minute, nil
	case "hour":
		return time.Duration(n) * time.Hour, nil
	case "day":
		return time.Duration(n) * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unknown rate unit: %s", parts[1])
	}
}

// --- Cron expressions ---

// cronSpec holds the parsed fields of an AWS Scheduler cron expression.
// A nil slice means "any value in range".
type cronSpec struct {
	minutes []int // 0-59
	hours   []int // 0-23
	doms    []int // 1-31; nil when domWild
	domWild bool
	months  []int // 1-12
	dows    []int // Go weekday 0-6 (0=Sunday); nil when dowWild
	dowWild bool
	years   []int // 1970-2199
}

// parseCronExpression parses "cron(min hour dom month dow year)".
func parseCronExpression(expr string) (*cronSpec, error) {
	inner := strings.TrimSuffix(strings.TrimPrefix(expr, "cron("), ")")
	fields := strings.Fields(inner)
	if len(fields) != 6 {
		return nil, fmt.Errorf("cron expression requires 6 fields, got %d: %s", len(fields), expr)
	}

	spec := &cronSpec{}
	var err error

	spec.minutes, err = expandField(fields[0], 0, 59)
	if err != nil {
		return nil, fmt.Errorf("minutes: %w", err)
	}
	spec.hours, err = expandField(fields[1], 0, 23)
	if err != nil {
		return nil, fmt.Errorf("hours: %w", err)
	}

	spec.domWild = fields[2] == "*" || fields[2] == "?"
	spec.doms, err = expandField(fields[2], 1, 31)
	if err != nil {
		return nil, fmt.Errorf("day-of-month: %w", err)
	}

	spec.months, err = expandMonthField(fields[3])
	if err != nil {
		return nil, fmt.Errorf("month: %w", err)
	}

	spec.dowWild = fields[4] == "*" || fields[4] == "?"
	spec.dows, err = expandDOWField(fields[4])
	if err != nil {
		return nil, fmt.Errorf("day-of-week: %w", err)
	}

	spec.years, err = expandField(fields[5], 1970, 2199)
	if err != nil {
		return nil, fmt.Errorf("year: %w", err)
	}

	return spec, nil
}

var monthNames = map[string]int{
	"JAN": 1, "FEB": 2, "MAR": 3, "APR": 4, "MAY": 5, "JUN": 6,
	"JUL": 7, "AUG": 8, "SEP": 9, "OCT": 10, "NOV": 11, "DEC": 12,
}

// dowNames maps AWS day names to AWS 1-7 values (1=SUN).
var dowNames = map[string]int{
	"SUN": 1, "MON": 2, "TUE": 3, "WED": 4, "THU": 5, "FRI": 6, "SAT": 7,
}

func expandMonthField(s string) ([]int, error) {
	upper := strings.ToUpper(s)
	for name, num := range monthNames {
		upper = strings.ReplaceAll(upper, name, strconv.Itoa(num))
	}
	return expandField(upper, 1, 12)
}

// expandDOWField returns Go weekday values (0=Sunday).
func expandDOWField(s string) ([]int, error) {
	if s == "*" || s == "?" {
		return nil, nil
	}
	upper := strings.ToUpper(s)
	for name, num := range dowNames {
		upper = strings.ReplaceAll(upper, name, strconv.Itoa(num))
	}
	// AWS DOW is 1-7 (1=SUN); convert to Go 0-6 (0=SUN).
	result, err := expandField(upper, 1, 7)
	if err != nil {
		return nil, err
	}
	for i, v := range result {
		result[i] = v - 1
	}
	return result, nil
}

// expandField parses a cron field token into a sorted, deduplicated set of ints.
// Returns nil for "*" or "?" (meaning "any").
func expandField(s string, min, max int) ([]int, error) {
	if s == "*" || s == "?" {
		return nil, nil
	}
	seen := map[int]bool{}
	for _, part := range strings.Split(s, ",") {
		vals, err := expandPart(part, min, max)
		if err != nil {
			return nil, err
		}
		for _, v := range vals {
			seen[v] = true
		}
	}
	result := make([]int, 0, len(seen))
	for v := range seen {
		result = append(result, v)
	}
	sort.Ints(result)
	return result, nil
}

func expandPart(s string, min, max int) ([]int, error) {
	if idx := strings.Index(s, "/"); idx >= 0 {
		// Step: base/step
		step, err := strconv.Atoi(s[idx+1:])
		if err != nil || step <= 0 {
			return nil, fmt.Errorf("invalid step: %s", s)
		}
		base := s[:idx]
		start, end := min, max
		if base != "*" {
			if dash := strings.Index(base, "-"); dash >= 0 {
				a, e1 := strconv.Atoi(base[:dash])
				b, e2 := strconv.Atoi(base[dash+1:])
				if e1 != nil || e2 != nil {
					return nil, fmt.Errorf("invalid range in step: %s", s)
				}
				start, end = a, b
			} else {
				v, err := strconv.Atoi(base)
				if err != nil {
					return nil, fmt.Errorf("invalid base: %s", s)
				}
				start = v
			}
		}
		var result []int
		for v := start; v <= end; v += step {
			result = append(result, v)
		}
		return result, nil
	}
	if idx := strings.Index(s, "-"); idx >= 0 {
		// Range: start-end
		a, e1 := strconv.Atoi(s[:idx])
		b, e2 := strconv.Atoi(s[idx+1:])
		if e1 != nil || e2 != nil || a > b {
			return nil, fmt.Errorf("invalid range: %s", s)
		}
		result := make([]int, 0, b-a+1)
		for v := a; v <= b; v++ {
			result = append(result, v)
		}
		return result, nil
	}
	// Single value
	v, err := strconv.Atoi(s)
	if err != nil {
		return nil, fmt.Errorf("invalid value: %s", s)
	}
	return []int{v}, nil
}

// nextCronTime finds the next time >= from+1min that matches the cron spec.
func nextCronTime(spec *cronSpec, from time.Time) (time.Time, error) {
	// Always work in UTC; advance past the current minute.
	t := from.UTC().Truncate(time.Minute).Add(time.Minute)
	deadline := from.UTC().Add(4 * 366 * 24 * time.Hour)

	for t.Before(deadline) {
		// Year
		y := nextInSet(spec.years, t.Year(), 2199)
		if y < 0 {
			return time.Time{}, fmt.Errorf("no matching year in cron spec")
		}
		if y > t.Year() {
			t = time.Date(y, time.January, 1, 0, 0, 0, 0, time.UTC)
			continue
		}

		// Month
		m := nextInSet(spec.months, int(t.Month()), 12)
		if m < 0 {
			// No more matching months this year.
			y2 := nextInSet(spec.years, t.Year()+1, 2199)
			if y2 < 0 {
				return time.Time{}, fmt.Errorf("no matching time within 4 years")
			}
			t = time.Date(y2, time.January, 1, 0, 0, 0, 0, time.UTC)
			continue
		}
		if m > int(t.Month()) {
			t = time.Date(t.Year(), time.Month(m), 1, 0, 0, 0, 0, time.UTC)
			continue
		}

		// Day
		maxDOM := daysInMonth(t.Year(), t.Month())
		dayOK := false
		if !spec.domWild && spec.dowWild {
			// DOM constrained only.
			d := nextInSet(spec.doms, t.Day(), maxDOM)
			if d < 0 {
				t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, time.UTC)
				continue
			}
			if d > t.Day() {
				t = time.Date(t.Year(), t.Month(), d, 0, 0, 0, 0, time.UTC)
				continue
			}
			dayOK = true
		} else if spec.domWild && !spec.dowWild {
			// DOW constrained only.
			if !containsInt(spec.dows, int(t.Weekday())) {
				t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, time.UTC)
				continue
			}
			dayOK = true
		} else {
			// Both wild (any day) or both constrained (OR semantics).
			if !spec.domWild && !spec.dowWild {
				if !containsInt(spec.doms, t.Day()) && !containsInt(spec.dows, int(t.Weekday())) {
					t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, time.UTC)
					continue
				}
			}
			dayOK = true
		}
		if !dayOK {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, time.UTC)
			continue
		}

		// Hour
		h := nextInSet(spec.hours, t.Hour(), 23)
		if h < 0 {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, time.UTC)
			continue
		}
		if h > t.Hour() {
			t = time.Date(t.Year(), t.Month(), t.Day(), h, 0, 0, 0, time.UTC)
			continue
		}

		// Minute
		min := nextInSet(spec.minutes, t.Minute(), 59)
		if min < 0 {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, time.UTC)
			continue
		}
		if min > t.Minute() {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), min, 0, 0, time.UTC)
			continue
		}

		return t, nil
	}
	return time.Time{}, fmt.Errorf("no matching time found within 4 years")
}

// --- Helpers ---

// nextInSet returns the smallest value in set that is >= v, or -1 if none.
// A nil set means "any" — returns v if v <= max, else -1.
func nextInSet(set []int, v, max int) int {
	if set == nil {
		if v <= max {
			return v
		}
		return -1
	}
	for _, s := range set {
		if s >= v {
			return s
		}
	}
	return -1
}

func containsInt(set []int, v int) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}
