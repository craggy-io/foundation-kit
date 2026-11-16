package trigger

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Cron is a parsed 5-field cron expression (min hour dom mon dow).
type Cron struct {
	min, hour, dom, mon, dow map[int]bool
	domAny, dowAny           bool
}

// ParseCron parses "M H DoM Mon DoW" with *, lists, ranges and steps.
func ParseCron(expr string) (*Cron, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron %q: want 5 fields", expr)
	}

	c := &Cron{}
	var err error
	if c.min, _, err = parseCronField(fields[0], 0, 59); err != nil {
		return nil, err
	}
	if c.hour, _, err = parseCronField(fields[1], 0, 23); err != nil {
		return nil, err
	}
	if c.dom, c.domAny, err = parseCronField(fields[2], 1, 31); err != nil {
		return nil, err
	}
	if c.mon, _, err = parseCronField(fields[3], 1, 12); err != nil {
		return nil, err
	}
	if c.dow, c.dowAny, err = parseCronField(fields[4], 0, 7); err != nil {
		return nil, err
	}
	if c.dow[7] { // 7 == Sunday == 0
		c.dow[0] = true
		delete(c.dow, 7)
	}
	return c, nil
}

func inSet(set map[int]bool, v int) bool { return set == nil || set[v] }

// Next returns the first matching minute strictly after t (search ≤ 5 years).
func (c *Cron) Next(t time.Time) time.Time {
	t = t.UTC().Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(5, 0, 0)
	for t.Before(limit) {
		if !inSet(c.mon, int(t.Month())) {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, time.UTC)
			continue
		}
		domOK := c.domAny || c.dom[t.Day()]
		dowOK := c.dowAny || c.dow[int(t.Weekday())]
		// Standard cron: when both dom and dow are restricted, either matches.
		dayOK := domOK && dowOK
		if !c.domAny && !c.dowAny {
			dayOK = domOK || dowOK
		}
		if !dayOK {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, time.UTC)
			continue
		}
		if !inSet(c.hour, t.Hour()) {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, time.UTC)
			continue
		}
		if !inSet(c.min, t.Minute()) {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

// ParseDuration accepts Go durations plus d/w suffixes ("119d", "1w").
func ParseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if strings.HasSuffix(s, "d") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64)
		if err != nil {
			return 0, err
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	if strings.HasSuffix(s, "w") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(s, "w"), 64)
		if err != nil {
			return 0, err
		}
		return time.Duration(n * float64(7*24*time.Hour)), nil
	}
	return time.ParseDuration(s)
}

func parseCronField(s string, lo, hi int) (map[int]bool, bool, error) {
	if s == "*" {
		return nil, true, nil
	}
	out := map[int]bool{}
	for _, part := range strings.Split(s, ",") {
		step := 1
		base := part
		if i := strings.Index(part, "/"); i >= 0 {
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n <= 0 {
				return nil, false, fmt.Errorf("cron: bad step in %q", part)
			}
			step, base = n, part[:i]
		}
		a, b := lo, hi
		if base != "*" {
			if i := strings.Index(base, "-"); i >= 0 {
				var err error
				if a, err = strconv.Atoi(base[:i]); err != nil {
					return nil, false, fmt.Errorf("cron: bad range %q", base)
				}
				if b, err = strconv.Atoi(base[i+1:]); err != nil {
					return nil, false, fmt.Errorf("cron: bad range %q", base)
				}
			} else {
				v, err := strconv.Atoi(base)
				if err != nil {
					return nil, false, fmt.Errorf("cron: bad value %q", base)
				}
				a, b = v, v
			}
		}
		if a < lo || b > hi || a > b {
			return nil, false, fmt.Errorf("cron: value %q out of range %d-%d", part, lo, hi)
		}
		for v := a; v <= b; v += step {
			out[v] = true
		}
	}
	return out, false, nil
}
