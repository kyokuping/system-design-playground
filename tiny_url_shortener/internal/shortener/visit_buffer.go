package shortener

import (
	"context"
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"
)

var ErrVisitBufferClosed = errors.New("visit buffer closed")

type visitFlushRequest struct {
	ctx  context.Context
	done chan error
}

// VisitBuffer aggregates visits on the request path without waiting. The
// worker only swaps the active map and flushes complete batches.
type VisitBuffer struct {
	flusher  URLVisitFlusher
	interval time.Duration
	maxKeys  int

	mu        sync.Mutex
	pending   map[string]URLVisitDelta
	order     []string
	dropped   atomic.Uint64
	flushErrs atomic.Uint64
	buffered  atomic.Int64
	closed    atomic.Bool
	closeOnce sync.Once
	closeErr  error

	flushNow chan struct{}
	flush    chan visitFlushRequest
	stop     chan context.Context
	done     chan struct{}
}

func NewVisitBuffer(flusher URLVisitFlusher, interval time.Duration, maxKeys int) *VisitBuffer {
	if interval <= 0 {
		interval = time.Second
	}
	if maxKeys <= 0 {
		maxKeys = 10_000
	}
	b := &VisitBuffer{
		flusher: flusher, interval: interval, maxKeys: maxKeys,
		pending: make(map[string]URLVisitDelta, maxKeys), order: make([]string, 0, maxKeys),
		flushNow: make(chan struct{}, 1), flush: make(chan visitFlushRequest),
		stop: make(chan context.Context, 1), done: make(chan struct{}),
	}
	go b.run()
	return b
}

func (b *VisitBuffer) RecordVisit(shortKey string, at time.Time) {
	// The critical section is a single map update, so waiting for the lock costs
	// far less than the visit that dropping it would lose.
	b.mu.Lock()
	if b.closed.Load() {
		b.mu.Unlock()
		b.dropped.Add(1)
		return
	}
	_, exists := b.pending[shortKey]
	fullBeforeMerge := len(b.pending) == b.maxKeys
	dropped := mergeVisit(b.pending, &b.order, URLVisitDelta{ShortKey: shortKey, Count: 1, LastSeen: at}, b.maxKeys)
	if !exists && !fullBeforeMerge {
		b.buffered.Add(1)
	}
	full := len(b.pending) == b.maxKeys
	b.mu.Unlock()
	b.dropped.Add(uint64(dropped))
	if full {
		select {
		case b.flushNow <- struct{}{}:
		default:
		}
	}
}

func (b *VisitBuffer) DroppedVisits() uint64 { return b.dropped.Load() }

func (b *VisitBuffer) FlushFailures() uint64 { return b.flushErrs.Load() }

func (b *VisitBuffer) PendingKeys() int { return int(b.buffered.Load()) }

func (b *VisitBuffer) Flush(ctx context.Context) error {
	if b.closed.Load() {
		return ErrVisitBufferClosed
	}
	request := visitFlushRequest{ctx: ctx, done: make(chan error, 1)}
	select {
	case b.flush <- request:
	case <-b.done:
		return ErrVisitBufferClosed
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-request.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *VisitBuffer) Close(ctx context.Context) error {
	b.closeOnce.Do(func() {
		b.closed.Store(true)
		b.stop <- ctx
	})
	select {
	case <-b.done:
		return b.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *VisitBuffer) takePending() (map[string]URLVisitDelta, []string) {
	// Allocate the replacements before locking. Holding the lock across these
	// makes every concurrent visit wait on an allocation.
	pending := make(map[string]URLVisitDelta, b.maxKeys)
	order := make([]string, 0, b.maxKeys)
	b.mu.Lock()
	pending, b.pending = b.pending, pending
	order, b.order = b.order, order
	b.mu.Unlock()
	return pending, order
}

func (b *VisitBuffer) run() {
	ticker := time.NewTicker(b.interval)
	defer ticker.Stop()
	retry := make(map[string]URLVisitDelta, b.maxKeys)
	retryOrder := make([]string, 0, b.maxKeys)

	flush := func(ctx context.Context) error {
		pending, order := b.takePending()
		for _, shortKey := range order {
			_, exists := retry[shortKey]
			fullBeforeMerge := len(retry) == b.maxKeys
			b.dropped.Add(uint64(mergeVisit(retry, &retryOrder, pending[shortKey], b.maxKeys)))
			if exists || fullBeforeMerge {
				b.buffered.Add(-1)
			}
		}
		if len(retry) == 0 {
			return nil
		}
		visits := make([]URLVisitDelta, 0, len(retry))
		for _, shortKey := range retryOrder {
			visits = append(visits, retry[shortKey])
		}
		if err := b.flusher.FlushVisits(ctx, visits); err != nil {
			b.flushErrs.Add(1)
			return err
		}
		b.buffered.Add(-int64(len(retry)))
		clear(retry)
		retryOrder = retryOrder[:0]
		return nil
	}
	retryAfterTick := false
	flushSoon := func() {
		ctx, cancel := context.WithTimeout(context.Background(), b.interval)
		if err := flush(ctx); err != nil {
			log.Printf("flush URL visits: %v", err)
			retryAfterTick = true
		} else {
			retryAfterTick = false
		}
		cancel()
	}

	for {
		select {
		case <-b.flushNow:
			if !retryAfterTick {
				flushSoon()
			}
		case <-ticker.C:
			retryAfterTick = false
			flushSoon()
		case request := <-b.flush:
			request.done <- flush(request.ctx)
		case ctx := <-b.stop:
			b.closeErr = flush(ctx)
			close(b.done)
			return
		}
	}
}

func mergeVisit(visits map[string]URLVisitDelta, order *[]string, incoming URLVisitDelta, maxKeys int) int64 {
	if current, ok := visits[incoming.ShortKey]; ok {
		current.Count += incoming.Count
		if current.LastSeen.Before(incoming.LastSeen) {
			current.LastSeen = incoming.LastSeen
		}
		visits[incoming.ShortKey] = current
		return 0
	}
	var dropped int64
	if len(visits) == maxKeys {
		oldest := (*order)[0]
		dropped = visits[oldest].Count
		delete(visits, oldest)
		*order = (*order)[1:]
	}
	visits[incoming.ShortKey] = incoming
	*order = append(*order, incoming.ShortKey)
	return dropped
}
