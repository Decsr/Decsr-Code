package automation

import (
	"strings"
	"testing"
	"time"
)

func TestParseCronErrors(t *testing.T) {
	for expr, want := range map[string]string{
		"* * * *":       "want 5 fields",
		"60 * * * *":    "outside 0-59",
		"* 24 * * *":    "outside 0-23",
		"* * 0 * *":     "outside 1-31",
		"* * * 13 *":    "outside 1-12",
		"* * * * 8":     "outside 0-7",
		"x * * * *":     "not a number",
		"*/0 * * * *":   "bad step",
		"5-1 * * * *":   "backwards",
		"1,,2 * * * *":  "empty list item",
		"* * * * 1-3-5": "not a number",
		"*/a * * * *":   "bad step",
		"* * * * * *":   "want 5 fields",
		"1-70 * * * *":  "outside",
		"@daily":        "want 5 fields",
	} {
		_, err := ParseCron(expr)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: error %v, want %q", expr, err, want)
		}
	}
}

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, time.UTC)
	if err != nil {
		panic(err)
	}
	return t
}

func TestCronNext(t *testing.T) {
	cases := []struct{ expr, from, want string }{
		{"* * * * *", "2026-11-16 10:00", "2026-11-16 10:01"},
		{"0 3 * * *", "2026-11-16 03:00", "2026-11-17 03:00"},
		{"0 3 * * *", "2026-11-16 02:59", "2026-11-16 03:00"},
		{"*/15 * * * *", "2026-11-16 10:07", "2026-11-16 10:15"},
		{"*/15 * * * *", "2026-11-16 10:45", "2026-11-16 11:00"},
		{"5,35 8-10 * * *", "2026-11-16 10:35", "2026-11-17 08:05"},
		// 2026-11-16 is a Monday; 1-5 is Monday to Friday.
		{"0 3 * * 1-5", "2026-11-20 04:00", "2026-11-23 03:00"}, // Friday -> Monday
		{"0 3 * * 6,0", "2026-11-16 00:00", "2026-11-21 03:00"}, // Saturday
		{"0 3 * * 7", "2026-11-16 00:00", "2026-11-22 03:00"},   // 7 is Sunday
		// month edges
		{"0 0 1 * *", "2026-11-16 00:00", "2026-12-01 00:00"},
		{"0 0 1 1 *", "2026-11-16 00:00", "2027-01-01 00:00"},
		{"30 12 31 * *", "2026-11-16 00:00", "2026-12-31 12:30"},
		{"0 0 31 * *", "2026-04-01 00:00", "2026-05-31 00:00"}, // April has 30 days
		{"0 0 29 2 *", "2026-03-01 00:00", "2028-02-29 00:00"}, // next leap day
		// both day fields restricted: either matches
		{"0 0 13 * 5", "2026-11-10 00:00", "2026-11-13 00:00"}, // Friday the 13th both
		{"0 0 15 * 1", "2026-11-10 00:00", "2026-11-15 00:00"}, // day 15 is a Sunday, Monday is the 16th: 15 first
		{"0 0 17 * 1", "2026-11-10 00:00", "2026-11-16 00:00"}, // Monday the 16th comes before the 17th
		// year edge
		{"59 23 31 12 *", "2026-12-31 23:59", "2027-12-31 23:59"},
		// ranges with steps
		{"10-30/10 * * * *", "2026-11-16 10:00", "2026-11-16 10:10"},
		{"10-30/10 * * * *", "2026-11-16 10:20", "2026-11-16 10:30"},
		{"0 0 */10 * *", "2026-11-16 00:00", "2026-11-21 00:00"},
	}
	for _, c := range cases {
		cr, err := ParseCron(c.expr)
		if err != nil {
			t.Errorf("%q: %v", c.expr, err)
			continue
		}
		got := cr.Next(at(c.from))
		if want := at(c.want); !got.Equal(want) {
			t.Errorf("%q after %s: got %s, want %s", c.expr, c.from, got.Format("2006-01-02 15:04"), c.want)
		}
	}
}

func TestCronFebruaryWildcardDay(t *testing.T) {
	cr, _ := ParseCron("0 0 * 2 *")
	// After 28 Feb 12:00 the schedule is February only, so the next run is 1 Feb next year.
	if got := cr.Next(at("2026-02-28 12:00")); !got.Equal(at("2027-02-01 00:00")) {
		t.Errorf("got %s, want 2027-02-01", got)
	}
}

func TestCronNeverFires(t *testing.T) {
	cr, err := ParseCron("0 0 31 2 *")
	if err != nil {
		t.Fatal(err)
	}
	if got := cr.Next(at("2026-01-01 00:00")); !got.IsZero() {
		t.Errorf("31 February fired at %s", got)
	}
	if !(Cron{}).Next(time.Now()).IsZero() {
		t.Error("zero Cron fired")
	}
}

func TestCronNextKeepsLocation(t *testing.T) {
	loc := time.FixedZone("X", 2*3600)
	cr, _ := ParseCron("0 3 * * *")
	got := cr.Next(time.Date(2026, 11, 16, 3, 0, 0, 0, loc))
	if got.Location() != loc || got.Hour() != 3 || got.Day() != 17 {
		t.Errorf("got %s", got)
	}
}
