package shortener

import (
	"context"
	"sync"
	"testing"
)

const (
	testIDRangeSize uint64 = 3
	maxBase62ID     uint64 = 3_521_614_606_207 // 62^7 - 1
)

type idRange struct {
	Start uint64
	End   uint64 // exclusive
}

type rangeAllocatorContract interface {
	Allocate(ctx context.Context, size uint64) (idRange, error)
}

type distributedIDGeneratorContract interface {
	NextID(ctx context.Context) (uint64, error)
}

type base62EncoderContract interface {
	Encode(id uint64) (string, error)
}

type allocatorState struct {
	NextID uint64
}

func newRangeAllocator(_ *allocatorState) rangeAllocatorContract {
	return unimplementedRangeAllocator{}
}

func newDistributedIDGenerator(
	_ rangeAllocatorContract,
	_ uint64,
) distributedIDGeneratorContract {
	return unimplementedDistributedIDGenerator{}
}

func newBase62Encoder() base62EncoderContract {
	return unimplementedBase62Encoder{}
}

func TestRangeAllocator_ReturnsNonOverlappingRanges(t *testing.T) {
	skipExpectedFailure(t)

	allocator := newRangeAllocator(&allocatorState{})
	first := allocateRange(t, allocator, testIDRangeSize)
	second := allocateRange(t, allocator, testIDRangeSize)

	if first.End > second.Start {
		t.Fatalf("allocated ranges overlap: first = %+v, second = %+v", first, second)
	}
	if first.End-first.Start != testIDRangeSize {
		t.Fatalf("first range size = %d, want %d", first.End-first.Start, testIDRangeSize)
	}
	if second.End-second.Start != testIDRangeSize {
		t.Fatalf("second range size = %d, want %d", second.End-second.Start, testIDRangeSize)
	}
}

func TestRangeAllocator_ConcurrentRequestsDoNotOverlap(t *testing.T) {
	skipExpectedFailure(t)

	const requestCount = 100
	allocator := newRangeAllocator(&allocatorState{})
	results := make(chan rangeAllocationResult, requestCount)

	var waitGroup sync.WaitGroup
	for range requestCount {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			allocated, err := allocator.Allocate(context.Background(), testIDRangeSize)
			results <- rangeAllocationResult{allocated: allocated, err: err}
		}()
	}
	waitGroup.Wait()
	close(results)

	seenIDs := make(map[uint64]bool, requestCount*int(testIDRangeSize))
	for result := range results {
		if result.err != nil {
			t.Fatalf("Allocate() returned an unexpected error: %v", result.err)
		}
		for id := result.allocated.Start; id < result.allocated.End; id++ {
			if seenIDs[id] {
				t.Fatalf("ID %d belongs to more than one allocated range", id)
			}
			seenIDs[id] = true
		}
	}
}

func TestRangeAllocator_RestartDoesNotReuseAllocatedRange(t *testing.T) {
	skipExpectedFailure(t)

	state := &allocatorState{}
	beforeRestart := newRangeAllocator(state)
	first := allocateRange(t, beforeRestart, testIDRangeSize)

	afterRestart := newRangeAllocator(state)
	second := allocateRange(t, afterRestart, testIDRangeSize)

	if first.End > second.Start {
		t.Fatalf("range was reused after restart: first = %+v, second = %+v", first, second)
	}
}

func TestDistributedIDGenerator_DoesNotRefillBeforeRangeIsExhausted(t *testing.T) {
	skipExpectedFailure(t)

	allocator := &sequenceRangeAllocator{
		ranges: []idRange{{Start: 100, End: 103}},
	}
	generator := newDistributedIDGenerator(allocator, testIDRangeSize)

	for _, want := range []uint64{100, 101, 102} {
		if got := nextID(t, generator); got != want {
			t.Fatalf("NextID() = %d, want %d", got, want)
		}
	}
	if allocator.calls != 1 {
		t.Fatalf("Allocate() calls = %d, want 1", allocator.calls)
	}
}

func TestDistributedIDGenerator_RefillsAfterRangeIsExhausted(t *testing.T) {
	skipExpectedFailure(t)

	allocator := &sequenceRangeAllocator{
		ranges: []idRange{
			{Start: 100, End: 103},
			{Start: 200, End: 203},
		},
	}
	generator := newDistributedIDGenerator(allocator, testIDRangeSize)

	for range testIDRangeSize {
		nextID(t, generator)
	}
	if got := nextID(t, generator); got != 200 {
		t.Fatalf("NextID() after refill = %d, want 200", got)
	}
	if allocator.calls != 2 {
		t.Fatalf("Allocate() calls = %d, want 2", allocator.calls)
	}
}

func TestBase62Encoder_PadsShortKeysToSevenCharacters(t *testing.T) {
	skipExpectedFailure(t)

	got, err := newBase62Encoder().Encode(0)
	if err != nil {
		t.Fatalf("Encode(0) returned an unexpected error: %v", err)
	}
	if got != "0000000" {
		t.Fatalf("Encode(0) = %q, want %q", got, "0000000")
	}
}

func TestBase62Encoder_EncodesMaximumSevenCharacterID(t *testing.T) {
	skipExpectedFailure(t)

	got, err := newBase62Encoder().Encode(maxBase62ID)
	if err != nil {
		t.Fatalf("Encode(maxBase62ID) returned an unexpected error: %v", err)
	}
	if got != "zzzzzzz" {
		t.Fatalf("Encode(maxBase62ID) = %q, want %q", got, "zzzzzzz")
	}
}

func TestBase62Encoder_RejectsIDOutsideSevenCharacterSpace(t *testing.T) {
	skipExpectedFailure(t)

	if _, err := newBase62Encoder().Encode(maxBase62ID + 1); err == nil {
		t.Fatal("Encode(maxBase62ID + 1) error = nil, want an error")
	}
}

func allocateRange(
	t *testing.T,
	allocator rangeAllocatorContract,
	size uint64,
) idRange {
	t.Helper()

	allocated, err := allocator.Allocate(context.Background(), size)
	if err != nil {
		t.Fatalf("Allocate(%d) returned an unexpected error: %v", size, err)
	}
	return allocated
}

func nextID(t *testing.T, generator distributedIDGeneratorContract) uint64 {
	t.Helper()

	id, err := generator.NextID(context.Background())
	if err != nil {
		t.Fatalf("NextID() returned an unexpected error: %v", err)
	}
	return id
}

type sequenceRangeAllocator struct {
	ranges []idRange
	calls  int
}

type rangeAllocationResult struct {
	allocated idRange
	err       error
}

func (a *sequenceRangeAllocator) Allocate(_ context.Context, _ uint64) (idRange, error) {
	allocated := a.ranges[a.calls]
	a.calls++
	return allocated, nil
}

type unimplementedRangeAllocator struct{}

func (unimplementedRangeAllocator) Allocate(context.Context, uint64) (idRange, error) {
	panic("TODO: wire the range allocator implementation")
}

type unimplementedDistributedIDGenerator struct{}

func (unimplementedDistributedIDGenerator) NextID(context.Context) (uint64, error) {
	panic("TODO: wire the distributed ID generator implementation")
}

type unimplementedBase62Encoder struct{}

func (unimplementedBase62Encoder) Encode(uint64) (string, error) {
	panic("TODO: wire the Base62 encoder implementation")
}
