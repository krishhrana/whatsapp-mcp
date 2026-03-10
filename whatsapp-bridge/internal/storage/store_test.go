package storage

import (
	"testing"
	"time"
)

func TestNormalizeToUTCConvertsNonZeroTimestamp(t *testing.T) {
	input := time.Date(2026, 3, 2, 14, 30, 0, 0, time.FixedZone("PST", -8*60*60))
	got := normalizeToUTC(input)

	if got.IsZero() {
		t.Fatal("expected non-zero time")
	}
	if got.Location() != time.UTC {
		t.Fatalf("expected UTC location, got %s", got.Location())
	}
	if got.Unix() != input.Unix() {
		t.Fatalf("expected identical instant, got unix=%d want=%d", got.Unix(), input.Unix())
	}
}

func TestNormalizeToUTCKeepsZeroTimestamp(t *testing.T) {
	got := normalizeToUTC(time.Time{})
	if !got.IsZero() {
		t.Fatalf("expected zero timestamp, got %v", got)
	}
}
