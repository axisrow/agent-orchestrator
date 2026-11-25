package store

import (
	"testing"
	"time"
)

// The shared UTC-normalizing helpers are the single choke point every store
// timestamp write routes through (issue #383): a nameless fixed zone — what a
// client's foreign offset produces on JSON decode — must never reach the
// driver, whose text form for it ("+0700 +0700") nothing can parse back.
func TestTimeToNullTimeNormalizesNamelessZone(t *testing.T) {
	nameless := time.Date(2026, 11, 25, 1, 0, 25, 570831000, time.FixedZone("", 7*3600))

	got := timeToNullTime(nameless)
	if !got.Valid {
		t.Fatal("timeToNullTime: valid=false, want true")
	}
	if !got.Time.Equal(nameless) {
		t.Fatalf("instant changed: %v, want %v", got.Time, nameless)
	}
	if _, offset := got.Time.Zone(); offset != 0 || got.Time.Location() != time.UTC {
		t.Fatalf("zone = %v(%d), want UTC", got.Time.Location(), offset)
	}

	if got := timeToNullTime(time.Time{}); got.Valid {
		t.Fatal("zero time: valid=true, want false")
	}

	if got := timePtrToNullTime(nil); got.Valid {
		t.Fatal("nil pointer: valid=true, want false")
	}
	if got := timePtrToNullTime(&nameless); !got.Valid || got.Time.Location() != time.UTC || !got.Time.Equal(nameless) {
		t.Fatalf("pointer: %+v, want valid UTC instant %v", got, nameless)
	}
	if got := timePtrToNullTime(&time.Time{}); got.Valid {
		t.Fatal("zero pointer: valid=true, want false")
	}
}
