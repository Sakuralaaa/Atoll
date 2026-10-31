package cronexpr

import (
	"testing"
	"time"
)

func TestParseErrors(t *testing.T) {
	for _, bad := range []string{"", "* * * *", "60 * * * *", "* 24 * * *", "* * 0 * *", "*/0 * * * *", "a * * * *", "5-1 * * * *"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) accepted", bad)
		}
	}
}

func TestNextPrev(t *testing.T) {
	sh, _ := time.LoadLocation("Asia/Shanghai")
	cases := []struct {
		expr string
		from string
		next string
		prev string
	}{
		{"0 3 * * *", "2026-10-30T21:47:00+08:00", "2026-10-31T03:00:00+08:00", "2026-10-30T03:00:00+08:00"},
		{"@daily", "2026-10-30T00:00:00+08:00", "2026-10-31T00:00:00+08:00", "2026-10-30T00:00:00+08:00"},
		{"*/15 * * * *", "2026-10-30T10:07:30+08:00", "2026-10-30T10:15:00+08:00", "2026-10-30T10:00:00+08:00"},
		{"30 2 * * sun", "2026-10-30T21:47:00+08:00", "2026-11-01T02:30:00+08:00", "2026-10-25T02:30:00+08:00"},
		{"0 4 1 * *", "2026-10-30T21:47:00+08:00", "2026-11-01T04:00:00+08:00", "2026-10-01T04:00:00+08:00"},
		{"0 0 29 2 *", "2026-10-30T21:47:00+08:00", "2028-02-29T00:00:00+08:00", "2024-02-29T00:00:00+08:00"},
		{"0 12 * jan-mar mon-fri", "2026-10-30T21:47:00+08:00", "2027-01-01T12:00:00+08:00", "2026-03-31T12:00:00+08:00"},
	}
	for _, c := range cases {
		s := MustParse(c.expr)
		from, _ := time.Parse(time.RFC3339, c.from)
		if got := s.Next(from, sh).Format(time.RFC3339); got != c.next {
			t.Errorf("%s Next(%s) = %s, want %s", c.expr, c.from, got, c.next)
		}
		if got := s.Prev(from, sh).Format(time.RFC3339); got != c.prev {
			t.Errorf("%s Prev(%s) = %s, want %s", c.expr, c.from, got, c.prev)
		}
	}
}

func TestDomOrDow(t *testing.T) {
	s := MustParse("0 0 13 * fri")                        // the 13th OR any Friday
	from := time.Date(2026, 10, 30, 1, 0, 0, 0, time.UTC) // Friday 30th
	if got := s.Next(from, time.UTC); !got.Equal(time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("next = %s", got)
	}
	if got := s.Prev(from, time.UTC); !got.Equal(time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("prev = %s", got)
	}
}

func TestDST(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	s := MustParse("30 2 * * *") // 02:30 does not exist on 2026-03-08 in New York
	from := time.Date(2026, 3, 7, 12, 0, 0, 0, ny)
	n := s.Next(from, ny)
	if n.Day() != 9 || n.Hour() != 2 || n.Minute() != 30 {
		t.Fatalf("next across spring-forward = %s", n)
	}
	if s.Never() {
		t.Fatal("never")
	}
}

func TestNever(t *testing.T) {
	if !MustParse("0 0 30 2 *").Never() {
		t.Fatal("Feb 30 should never fire")
	}
}
