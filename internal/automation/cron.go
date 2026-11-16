package automation

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Cron is a standard 5-field cron schedule: minute hour day-of-month month
// day-of-week. Fields take numbers, "*", "*/n", ranges "a-b", "a-b/n" and
// comma lists. Day-of-week is 0-7 (0 and 7 are Sunday).
type Cron struct {
	min, hour, dom, month, dow uint64 // bit sets
	domStar, dowStar           bool   // the field started with "*"
	valid                      bool
}

// ParseCron parses a 5-field cron expression.
func ParseCron(expr string) (Cron, error) {
	f := strings.Fields(expr)
	if len(f) != 5 {
		return Cron{}, fmt.Errorf("want 5 fields (minute hour day-of-month month day-of-week), got %d", len(f))
	}
	var c Cron
	var err error
	specs := []struct {
		name     string
		text     string
		lo, hi   int
		into     *uint64
		starFlag *bool
	}{
		{"minute", f[0], 0, 59, &c.min, nil},
		{"hour", f[1], 0, 23, &c.hour, nil},
		{"day-of-month", f[2], 1, 31, &c.dom, &c.domStar},
		{"month", f[3], 1, 12, &c.month, nil},
		{"day-of-week", f[4], 0, 7, &c.dow, &c.dowStar},
	}
	for _, s := range specs {
		if *s.into, err = parseField(s.text, s.lo, s.hi); err != nil {
			return Cron{}, fmt.Errorf("%s %q: %w", s.name, s.text, err)
		}
		if s.starFlag != nil {
			*s.starFlag = strings.HasPrefix(s.text, "*")
		}
	}
	if c.dow&(1<<7) != 0 { // 7 is Sunday too
		c.dow |= 1
	}
	c.valid = true
	return c, nil
}

func parseField(text string, lo, hi int) (uint64, error) {
	var set uint64
	for _, part := range strings.Split(text, ",") {
		if part == "" {
			return 0, fmt.Errorf("empty list item")
		}
		rng, step := part, 1
		if r, s, ok := strings.Cut(part, "/"); ok {
			n, err := strconv.Atoi(s)
			if err != nil || n < 1 {
				return 0, fmt.Errorf("bad step %q", s)
			}
			rng, step = r, n
		}
		a, b := lo, hi
		switch {
		case rng == "*":
		case strings.Contains(rng, "-"):
			x, y, _ := strings.Cut(rng, "-")
			var err error
			if a, err = atoiIn(x, lo, hi); err != nil {
				return 0, err
			}
			if b, err = atoiIn(y, lo, hi); err != nil {
				return 0, err
			}
			if a > b {
				return 0, fmt.Errorf("range %q runs backwards", rng)
			}
		default:
			v, err := atoiIn(rng, lo, hi)
			if err != nil {
				return 0, err
			}
			a, b = v, v
			if strings.Contains(part, "/") {
				b = hi // "a/n" means a through the end
			}
		}
		for v := a; v <= b; v += step {
			set |= 1 << uint(v)
		}
	}
	return set, nil
}

func atoiIn(s string, lo, hi int) (int, error) {
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", s)
	}
	if v < lo || v > hi {
		return 0, fmt.Errorf("%d is outside %d-%d", v, lo, hi)
	}
	return v, nil
}

func (c Cron) dayMatches(t time.Time) bool {
	domOK := c.dom&(1<<uint(t.Day())) != 0
	dowOK := c.dow&(1<<uint(t.Weekday())) != 0
	if c.domStar || c.dowStar {
		return domOK && dowOK // an unrestricted field matches everything
	}
	return domOK || dowOK // both restricted: either one, like cron
}

// Next is the first run time strictly after t, in t's location. It is the zero
// time when the schedule never fires (such as 31 February).
func (c Cron) Next(t time.Time) time.Time {
	if !c.valid {
		return time.Time{}
	}
	t = t.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(8, 0, 0)
	for t.Before(limit) {
		switch {
		case c.month&(1<<uint(t.Month())) == 0:
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
		case !c.dayMatches(t):
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
		case c.hour&(1<<uint(t.Hour())) == 0:
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
		case c.min&(1<<uint(t.Minute())) == 0:
			t = t.Add(time.Minute)
		default:
			return t
		}
	}
	return time.Time{}
}
