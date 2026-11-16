package runner

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// rateLimitNow is the clock used when a rate-limit error is classified. Tests
// replace it.
var rateLimitNow = time.Now

// Reset-time sources, all of them data the providers already return:
//
//   - HTTP headers: Retry-After (seconds or HTTP date) and the
//     x-ratelimit-reset-* / anthropic-ratelimit-*-reset family.
//   - JSON error payloads carrying resets_at / reset_at (RFC3339 or unix
//     seconds), and the legacy Claude CLI "usage limit reached|<unix>" text.
//   - Free text: Claude Code ("resets 2pm (Asia/Ho_Chi_Minh)"), Codex
//     ("Try again at 2:30 PM." or "Try again at Nov 17th, 2025 2:30 PM.", see
//     UsageLimitReachedError in codex-rs/protocol/src/error.rs; exec --json
//     events only carry that text in ThreadErrorEvent.message, see
//     codex-rs/exec/src/exec_events.rs, there is no rate-limit payload),
//     and relative phrases such as "try again in 3h 2m".

const monthNames = "jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec"

const durUnit = `\d+(?:\.\d+)?\s*(?:days?|hours?|hrs?|minutes?|mins?|seconds?|secs?|d|h|m|s)\b`

var (
	reJSONTime    = regexp.MustCompile(`(?i)reset(?:s)?_?(?:at|time)"?\s*[:=]\s*"?(\d{9,13}(?:\.\d+)?|\d{4}-\d{2}-\d{2}T[0-9:.]+(?:Z|[+-]\d{2}:?\d{2})?)`)
	reJSONSeconds = regexp.MustCompile(`(?i)resets?_in_seconds"?\s*[:=]\s*"?(\d+(?:\.\d+)?)`)
	rePipeTime    = regexp.MustCompile(`\|\s*(\d{10})\b`)
	reRelative    = regexp.MustCompile(`(?i)\b(?:in|after)\s+(` + durUnit + `(?:[\s,]*(?:and\s+)?` + durUnit + `)*)`)
	reDurPart     = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)\s*(days?|hours?|hrs?|minutes?|mins?|seconds?|secs?|d|h|m|s)\b`)
	reAbsolute    = regexp.MustCompile(`(?i)\b(?:resets?|try again|retry)(?:\s+(?:at|on|after))?\s+` +
		`(?:(?:on\s+)?(` + monthNames + `)[a-z]*\.?\s+(\d{1,2})(?:st|nd|rd|th)?,?\s+(?:(\d{4}),?\s+)?(?:at\s+)?)?` +
		`(\d{1,2})(?::(\d{2}))?\s*([ap]\.?m\.?)?(?:\s*\(([^)]+)\)|\s+(UTC|GMT)\b)?`)
)

// parseResetAt extracts a reset time from error text. now is injected so the
// result is deterministic. It returns the zero time when nothing parses.
func parseResetAt(now time.Time, text string) time.Time {
	if text == "" {
		return time.Time{}
	}
	if m := reJSONTime.FindStringSubmatch(text); m != nil {
		if t := parseTimestamp(m[1]); !t.IsZero() {
			return t
		}
	}
	if m := rePipeTime.FindStringSubmatch(text); m != nil {
		if t := parseTimestamp(m[1]); !t.IsZero() {
			return t
		}
	}
	if m := reJSONSeconds.FindStringSubmatch(text); m != nil {
		if f, err := strconv.ParseFloat(m[1], 64); err == nil {
			return now.Add(time.Duration(f * float64(time.Second)))
		}
	}
	if m := reRelative.FindStringSubmatch(text); m != nil {
		if d := sumDurations(m[1]); d > 0 {
			return now.Add(d)
		}
	}
	for _, m := range reAbsolute.FindAllStringSubmatch(text, -1) {
		if t := absoluteTime(now, m); !t.IsZero() {
			return t
		}
	}
	return time.Time{}
}

// parseTimestamp reads unix seconds (or milliseconds) or an RFC3339 value.
func parseTimestamp(v string) time.Time {
	if strings.Contains(v, "T") {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t
		}
		return time.Time{}
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 {
		return time.Time{}
	}
	if f > 1e12 {
		f /= 1000
	}
	return time.Unix(int64(f), 0)
}

func sumDurations(s string) time.Duration {
	var total time.Duration
	for _, m := range reDurPart.FindAllStringSubmatch(s, -1) {
		n, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			continue
		}
		var unit time.Duration
		switch strings.ToLower(m[2])[0] {
		case 'd':
			unit = 24 * time.Hour
		case 'h':
			unit = time.Hour
		case 'm':
			unit = time.Minute
		case 's':
			unit = time.Second
		}
		total += time.Duration(n * float64(unit))
	}
	return total
}

// absoluteTime turns a reAbsolute match into a time. Without an explicit date
// a clock time that is not in the future means tomorrow.
func absoluteTime(now time.Time, m []string) time.Time {
	month, day, year, hourS, minS, ampm, tzParen, tzBare := m[1], m[2], m[3], m[4], m[5], strings.ToLower(m[6]), m[7], m[8]
	if minS == "" && ampm == "" {
		return time.Time{} // a bare number such as "resets 5" is not a time
	}
	hour, err := strconv.Atoi(hourS)
	if err != nil {
		return time.Time{}
	}
	mins := 0
	if minS != "" {
		mins, _ = strconv.Atoi(minS)
	}
	if mins > 59 {
		return time.Time{}
	}
	if ampm != "" {
		if hour < 1 || hour > 12 {
			return time.Time{}
		}
		hour %= 12
		if strings.HasPrefix(ampm, "p") {
			hour += 12
		}
	} else if hour > 23 {
		return time.Time{}
	}

	loc := now.Location()
	tz := strings.TrimSpace(tzParen)
	if tz == "" {
		tz = tzBare
	}
	if tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	local := now.In(loc)
	y, mo, d := local.Date()
	explicitDate := month != ""
	if explicitDate {
		mo = monthIndex(month)
		d, _ = strconv.Atoi(day)
		if year != "" {
			y, _ = strconv.Atoi(year)
		}
	}
	t := time.Date(y, mo, d, hour, mins, 0, 0, loc)
	if !t.After(now) {
		switch {
		case !explicitDate:
			t = t.AddDate(0, 0, 1)
		case year == "":
			t = t.AddDate(1, 0, 0)
		}
	}
	return t
}

func monthIndex(abbr string) time.Month {
	i := strings.Index(monthNames, strings.ToLower(abbr[:3]))
	return time.Month(i/4 + 1)
}

// resetFromHeaders reads the reset time from rate-limit response headers.
// Retry-After wins; otherwise the latest of the x-ratelimit-reset-* style
// values is used, since the exhausted bucket is not known.
func resetFromHeaders(now time.Time, h http.Header) time.Time {
	if h == nil {
		return time.Time{}
	}
	if v := strings.TrimSpace(h.Get("Retry-After")); v != "" {
		if secs, err := strconv.ParseFloat(v, 64); err == nil {
			return now.Add(time.Duration(secs * float64(time.Second)))
		}
		if t, err := http.ParseTime(v); err == nil {
			return t
		}
	}
	var latest time.Time
	for key, vals := range h {
		k := strings.ToLower(key)
		isReset := strings.HasPrefix(k, "x-ratelimit-reset") ||
			(strings.HasPrefix(k, "anthropic-ratelimit-") && strings.HasSuffix(k, "-reset"))
		if !isReset || len(vals) == 0 {
			continue
		}
		if t := parseHeaderReset(now, strings.TrimSpace(vals[0])); t.After(latest) {
			latest = t
		}
	}
	return latest
}

// parseHeaderReset reads "6m0s", "1h2m", plain seconds, unix seconds or RFC3339.
func parseHeaderReset(now time.Time, v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	if strings.Contains(v, "T") {
		return parseTimestamp(v)
	}
	if d, err := time.ParseDuration(v); err == nil && d >= 0 {
		return now.Add(d)
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 {
		return time.Time{}
	}
	if f >= 1e9 {
		return parseTimestamp(v)
	}
	return now.Add(time.Duration(f * float64(time.Second)))
}
