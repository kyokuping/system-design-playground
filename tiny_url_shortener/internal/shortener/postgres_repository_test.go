package shortener

import (
	"slices"
	"testing"
	"time"
)

func TestVisitBatchArrays_SortsKeysWithoutSeparatingValues(t *testing.T) {
	earlier := time.Date(2026, time.August, 10, 1, 0, 0, 0, time.UTC)
	later := earlier.Add(time.Hour)

	keys, counts, lastSeen := visitBatchArrays([]URLVisitDelta{
		{ShortKey: "Zz99Zz9", Count: 2, LastSeen: later},
		{ShortKey: "Aa00Aa0", Count: 3, LastSeen: earlier},
	})

	if !slices.Equal(keys, []string{"Aa00Aa0", "Zz99Zz9"}) ||
		!slices.Equal(counts, []int64{3, 2}) ||
		!slices.Equal(lastSeen, []time.Time{earlier, later}) {
		t.Fatalf("visitBatchArrays() = %v, %v, %v", keys, counts, lastSeen)
	}
}
