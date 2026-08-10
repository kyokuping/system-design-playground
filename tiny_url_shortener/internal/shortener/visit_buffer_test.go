package shortener

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type stubVisitFlusher struct {
	mu      sync.Mutex
	batches [][]URLVisitDelta
	err     error
}

func (f *stubVisitFlusher) FlushVisits(_ context.Context, visits []URLVisitDelta) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batches = append(f.batches, append([]URLVisitDelta(nil), visits...))
	return f.err
}

func TestVisitBuffer_AggregatesByKey(t *testing.T) {
	flusher := &stubVisitFlusher{}
	buffer := NewVisitBuffer(flusher, time.Hour, 10)
	t.Cleanup(func() { _ = buffer.Close(context.Background()) })
	earlier := time.Date(2026, time.August, 10, 1, 0, 0, 0, time.UTC)
	later := earlier.Add(time.Hour)

	buffer.RecordVisit("Ab12Cd3", later)
	buffer.RecordVisit("Ab12Cd3", earlier)
	buffer.RecordVisit("Zy98Xw7", earlier)
	buffer.mu.Lock()
	aggregated := buffer.pending["Ab12Cd3"]
	buffer.mu.Unlock()
	if aggregated.Count != 2 || !aggregated.LastSeen.Equal(later) {
		t.Fatalf("request-path aggregation = %+v", aggregated)
	}
	if len(flusher.batches) != 0 {
		t.Fatal("visits flushed before Flush()")
	}
	if err := buffer.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	got := make(map[string]URLVisitDelta)
	for _, visit := range flusher.batches[0] {
		got[visit.ShortKey] = visit
	}
	if got["Ab12Cd3"].Count != 2 || !got["Ab12Cd3"].LastSeen.Equal(later) {
		t.Fatalf("aggregated visit = %+v", got["Ab12Cd3"])
	}
	if got["Zy98Xw7"].Count != 1 {
		t.Fatalf("second visit = %+v", got["Zy98Xw7"])
	}
}

func TestVisitBuffer_ConcurrentVisitsAreNotDropped(t *testing.T) {
	const workers, perWorker = 8, 250
	flusher := &stubVisitFlusher{}
	buffer := NewVisitBuffer(flusher, time.Hour, 10)
	t.Cleanup(func() { _ = buffer.Close(context.Background()) })

	var group sync.WaitGroup
	for worker := range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			at := time.Now()
			for range perWorker {
				buffer.RecordVisit(fmt.Sprintf("key%04d", worker), at)
			}
		}()
	}
	group.Wait()

	if err := buffer.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	var total int64
	for _, visit := range flusher.batches[0] {
		total += visit.Count
	}
	if total != workers*perWorker {
		t.Fatalf("flushed visits = %d, want %d", total, workers*perWorker)
	}
	if dropped := buffer.DroppedVisits(); dropped != 0 {
		t.Fatalf("dropped visits = %d, want 0", dropped)
	}
}

func TestVisitBuffer_RetainsFailedBatchForRetry(t *testing.T) {
	flusher := &stubVisitFlusher{err: errors.New("postgres unavailable")}
	buffer := NewVisitBuffer(flusher, time.Hour, 10)
	t.Cleanup(func() { _ = buffer.Close(context.Background()) })
	buffer.RecordVisit("Ab12Cd3", time.Now())

	if err := buffer.Flush(context.Background()); err == nil {
		t.Fatal("first Flush() error = nil")
	}
	flusher.mu.Lock()
	flusher.err = nil
	flusher.mu.Unlock()
	if err := buffer.Flush(context.Background()); err != nil {
		t.Fatalf("second Flush() error = %v", err)
	}
	if len(flusher.batches) != 2 || flusher.batches[1][0].Count != 1 {
		t.Fatalf("batches = %+v", flusher.batches)
	}
}

func TestMemoryVisitFlushDoesNotChangeRevision(t *testing.T) {
	repository := NewMemoryRepository()
	mapping := URLMapping{
		ShortKey:       "Ab12Cd3",
		LongURL:        mustCacheURL(t, "https://example.com/"),
		LastAccessedAt: time.Date(2026, time.August, 10, 1, 0, 0, 0, time.UTC),
	}
	if err := repository.Save(context.Background(), mapping); err != nil {
		t.Fatal(err)
	}
	before, _ := repository.FindByShortKey(context.Background(), mapping.ShortKey)
	later := mapping.LastAccessedAt.Add(time.Hour)
	if err := repository.FlushVisits(context.Background(), []URLVisitDelta{{ShortKey: mapping.ShortKey, Count: 3, LastSeen: later}}); err != nil {
		t.Fatal(err)
	}
	after, _ := repository.FindByShortKey(context.Background(), mapping.ShortKey)
	statistics, _ := repository.Statistics(context.Background(), mapping.ShortKey)
	if after.Revision != before.Revision || statistics.Visits != 3 || !after.LastAccessedAt.Equal(later) {
		t.Fatalf("after flush: revision=%d visits=%d last_accessed_at=%v", after.Revision, statistics.Visits, after.LastAccessedAt)
	}
}
