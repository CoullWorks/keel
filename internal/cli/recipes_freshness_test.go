package cli

import (
	"testing"
	"time"
)

func TestFreshnessStatus(t *testing.T) {
	now := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		updated string
		want    string
	}{
		{"", "unknown"},
		{"not-a-date", "unknown"},
		{"2026-08-01", "fresh"},      // 6 days
		{"2026-01-01", "review-due"}, // >180 days
		{"2025-01-01", "review-due"}, // way old
	}
	for _, c := range cases {
		got, _ := freshnessStatus(c.updated, now, 180)
		if got != c.want {
			t.Errorf("freshnessStatus(%q) = %q, want %q", c.updated, got, c.want)
		}
	}
}
