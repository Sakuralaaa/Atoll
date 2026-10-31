// Package cronexpr is a tiny standard 5-field cron parser ("min hour dom month dow")
// used by backup schedules. It supports *, lists (1,2), ranges (1-5), steps (*/15, 1-30/5),
// month/day names (jan, mon), and the macros @hourly @daily @midnight @weekly @monthly @yearly.
// As in Vixie cron, when both day-of-month and day-of-week are restricted a day matches if
// either one does.
package cronexpr

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "time/tzdata" // schedules name IANA zones; do not depend on the host's zoneinfo
)

// Schedule is a parsed cron expression.
type Schedule struct {
	min, hour, dom, month, dow uint64 // bit sets
	domStar, dowStar           bool
	expr                       string
}

var macros = map[string]string{
	"@yearly": "0 0 1 1 *", "@annually": "0 0 1 1 *", "@monthly": "0 0 1 * *",
	"@weekly": "0 0 * * 0", "@daily": "0 0 * * *", "@midnight": "0 0 * * *", "@hourly": "0 * * * *",
}

var monthNames = map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}
var dowNames = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}

// Parse parses a 5-field cron expression or macro.
func Parse(expr string) (*Schedule, error) {
	e := strings.TrimSpace(strings.ToLower(expr))
	if m, ok := macros[e]; ok {
		e = m
	}
	f := strings.Fields(e)
	if len(f) != 5 {
		return nil, fmt.Errorf("cron %q: want 5 fields (minute hour day-of-month month day-of-week)", expr)
	}
	s := &Schedule{expr: strings.TrimSpace(expr)}
	var err error
	if s.min, err = field(f[0], 0, 59, nil); err != nil {
		return nil, fmt.Errorf("cron %q minute: %w", expr, err)
	}
	if s.hour, err = field(f[1], 0, 23, nil); err != nil {
		return nil, fmt.Errorf("cron %q hour: %w", expr, err)
	}
	if s.dom, err = field(f[2], 1, 31, nil); err != nil {
		return nil, fmt.Errorf("cron %q day-of-month: %w", expr, err)
	}
	if s.month, err = field(f[3], 1, 12, monthNames); err != nil {
		return nil, fmt.Errorf("cron %q month: %w", expr, err)
	}
	if s.dow, err = field(f[4], 0, 7, dowNames); err != nil {
		return nil, fmt.Errorf("cron %q day-of-week: %w", expr, err)
	}
	if s.dow&(1<<7) != 0 { // 7 = Sunday
		s.dow |= 1
		s.dow &^= 1 << 7
	}
	s.domStar = f[2] == "*" || f[2] == "?"
	s.dowStar = f[4] == "*" || f[4] == "?"
	return s, nil
}

// MustParse panics on error (tests, constants).
func MustParse(expr string) *Schedule {
	s, err := Parse(expr)
	if err != nil {
		panic(err)
	}
	return s
}

func (s *Schedule) String() string { return s.expr }

func field(spec string, lo, hi int, names map[string]int) (uint64, error) {
	var bits uint64
	for _, part := range strings.Split(spec, ",") {
		if part == "" {
			return 0, fmt.Errorf("empty list item")
		}
		rng, step := part, 1
		if i := strings.IndexByte(part, '/'); i >= 0 {
			rng = part[:i]
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n <= 0 {
				return 0, fmt.Errorf("bad step %q", part[i+1:])
			}
			step = n
		}
		a, b := lo, hi
		switch {
		case rng == "*" || rng == "?":
		default:
			if i := strings.IndexByte(rng, '-'); i > 0 {
				x, err := num(rng[:i], names)
				if err != nil {
					return 0, err
				}
				y, err := num(rng[i+1:], names)
				if err != nil {
					return 0, err
				}
				a, b = x, y
			} else {
				x, err := num(rng, names)
				if err != nil {
					return 0, err
				}
				a, b = x, x
				if step > 1 { // "5/10" means 5-hi/10
					b = hi
				}
			}
		}
		if a < lo || b > hi || a > b {
			return 0, fmt.Errorf("%q out of range %d-%d", part, lo, hi)
		}
		for v := a; v <= b; v += step {
			bits |= 1 << uint(v)
		}
	}
	return bits, nil
}

func num(s string, names map[string]int) (int, error) {
	if n, ok := names[s]; ok {
		return n, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("bad value %q", s)
	}
	return n, nil
}

func (s *Schedule) dayMatches(t time.Time) bool {
	d := s.dom&(1<<uint(t.Day())) != 0
	w := s.dow&(1<<uint(t.Weekday())) != 0
	switch {
	case s.domStar && s.dowStar:
		return true
	case s.domStar:
		return w
	case s.dowStar:
		return d
	}
	return d || w
}

// Matches reports whether the minute containing t is a fire time (in t's location).
func (s *Schedule) Matches(t time.Time) bool {
	return s.month&(1<<uint(t.Month())) != 0 && s.dayMatches(t) &&
		s.hour&(1<<uint(t.Hour())) != 0 && s.min&(1<<uint(t.Minute())) != 0
}

// searchLimit bounds Next/Prev (a schedule like "0 0 30 2 *" never fires).
const searchLimit = 5 * 366 * 24 * time.Hour

// Next returns the first fire time strictly after t, evaluated in loc (nil = t's location).
// Zero time when the schedule never fires within five years.
func (s *Schedule) Next(t time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = t.Location()
	}
	t = t.In(loc).Truncate(time.Minute).Add(time.Minute)
	end := t.Add(searchLimit)
	for t.Before(end) {
		if s.month&(1<<uint(t.Month())) == 0 {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, loc)
			continue
		}
		if !s.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
			continue
		}
		if s.hour&(1<<uint(t.Hour())) == 0 {
			// Add, not Date(+1h): stays correct across DST changes
			t = t.Truncate(time.Hour).Add(time.Hour)
			if t.Minute() != 0 { // zones with 30/45 min offsets
				t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, loc)
			}
			continue
		}
		if s.min&(1<<uint(t.Minute())) == 0 {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

// Prev returns the latest fire time at or before t (in loc), zero if none within five years.
func (s *Schedule) Prev(t time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = t.Location()
	}
	t = t.In(loc).Truncate(time.Minute)
	end := t.Add(-searchLimit)
	for t.After(end) {
		if s.month&(1<<uint(t.Month())) == 0 {
			// last minute of the previous month
			t = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, loc).Add(-time.Minute)
			continue
		}
		if !s.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc).Add(-time.Minute)
			continue
		}
		if s.hour&(1<<uint(t.Hour())) == 0 {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), 0, 0, 0, loc).Add(-time.Minute)
			continue
		}
		if s.min&(1<<uint(t.Minute())) == 0 {
			t = t.Add(-time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

// LoadLocation resolves an IANA zone name; "" and "Local" mean UTC (agents and the control
// server may sit in different zones, so schedules always carry an explicit zone).
func LoadLocation(name string) (*time.Location, error) {
	if name == "" || name == "UTC" || name == "Local" {
		return time.UTC, nil
	}
	return time.LoadLocation(name)
}

// Never reports whether the schedule has no fire time in the next five years (e.g. Feb 30).
func (s *Schedule) Never() bool {
	return s.Next(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), time.UTC).IsZero() &&
		s.Next(time.Now(), time.UTC).IsZero()
}
