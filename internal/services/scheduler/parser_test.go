package scheduler

import (
	"testing"
	"time"
)

func TestNextFireTimeRate(t *testing.T) {
	base := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		expr string
		want time.Duration
	}{
		{"rate(1 minute)", time.Minute},
		{"rate(5 minutes)", 5 * time.Minute},
		{"rate(1 hour)", time.Hour},
		{"rate(2 hours)", 2 * time.Hour},
		{"rate(1 day)", 24 * time.Hour},
	}
	for _, c := range cases {
		got, err := nextFireTime(c.expr, base)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", c.expr, err)
			continue
		}
		if got != base.Add(c.want) {
			t.Errorf("%s: got %v, want %v", c.expr, got, base.Add(c.want))
		}
	}
}

func TestNextFireTimeCron(t *testing.T) {
	// Every day at noon UTC.
	base := time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC)
	got, err := nextFireTime("cron(0 12 * * ? *)", base)
	if err != nil {
		t.Fatal(err)
	}
	// Next noon is tomorrow (Jan 2).
	want := time.Date(2025, 1, 2, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNextFireTimeCronMinutes(t *testing.T) {
	// Every 15 minutes.
	base := time.Date(2025, 6, 1, 10, 7, 0, 0, time.UTC)
	got, err := nextFireTime("cron(0/15 * * * ? *)", base)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2025, 6, 1, 10, 15, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNextFireTimeCronDOW(t *testing.T) {
	// Every Monday at 9 AM.
	// 2025-06-02 is a Monday.
	base := time.Date(2025, 6, 1, 9, 0, 0, 0, time.UTC) // Sunday
	got, err := nextFireTime("cron(0 9 ? * MON *)", base)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2025, 6, 2, 9, 0, 0, 0, time.UTC) // Monday
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNextFireTimeCronMonthly(t *testing.T) {
	// 1st of every month at midnight.
	base := time.Date(2025, 3, 1, 0, 5, 0, 0, time.UTC)
	got, err := nextFireTime("cron(0 0 1 * ? *)", base)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExpandField(t *testing.T) {
	vals, err := expandField("*/5", 0, 59)
	if err != nil {
		t.Fatal(err)
	}
	if len(vals) != 12 || vals[0] != 0 || vals[1] != 5 || vals[11] != 55 {
		t.Errorf("unexpected vals: %v", vals)
	}
}
