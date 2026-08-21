package shortener

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"sync"
)

const (
	base62Alphabet        = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	shortKeySize          = 7
	MaxBase62ID    uint64 = 3_521_614_606_207
)

type RandomKeyGenerator struct{ Reader io.Reader }

func NewRandomKeyGenerator() *RandomKeyGenerator { return &RandomKeyGenerator{Reader: rand.Reader} }

func (g *RandomKeyGenerator) Generate() (string, error) {
	reader := g.Reader
	if reader == nil {
		reader = rand.Reader
	}
	result := make([]byte, shortKeySize)
	buffer := []byte{0}
	for index := range result {
		for {
			if _, err := io.ReadFull(reader, buffer); err != nil {
				return "", err
			}
			// Rejection sampling prevents modulo bias (248 is divisible by 62).
			if buffer[0] < 248 {
				result[index] = base62Alphabet[int(buffer[0])%62]
				break
			}
		}
	}
	return string(result), nil
}

type IDRange struct{ Start, End uint64 }

type RangeAllocator interface {
	Allocate(context.Context, uint64) (IDRange, error)
}

type MemoryAllocatorState struct {
	mu     sync.Mutex
	NextID uint64
}

type MemoryRangeAllocator struct{ state *MemoryAllocatorState }

func NewMemoryRangeAllocator(state *MemoryAllocatorState) *MemoryRangeAllocator {
	if state == nil {
		state = &MemoryAllocatorState{}
	}
	return &MemoryRangeAllocator{state: state}
}

func (a *MemoryRangeAllocator) Allocate(ctx context.Context, size uint64) (IDRange, error) {
	if err := ctx.Err(); err != nil {
		return IDRange{}, err
	}
	if size == 0 {
		return IDRange{}, errors.New("range size must be positive")
	}
	a.state.mu.Lock()
	defer a.state.mu.Unlock()
	if a.state.NextID > MaxBase62ID || size-1 > MaxBase62ID-a.state.NextID {
		return IDRange{}, ErrKeyGenerationFailed
	}
	allocated := IDRange{Start: a.state.NextID, End: a.state.NextID + size}
	a.state.NextID = allocated.End
	return allocated, nil
}

type DistributedIDGenerator struct {
	mu        sync.Mutex
	allocator RangeAllocator
	rangeSize uint64
	next, end uint64
}

func NewDistributedIDGenerator(allocator RangeAllocator, rangeSize uint64) *DistributedIDGenerator {
	return &DistributedIDGenerator{allocator: allocator, rangeSize: rangeSize}
}

func (g *DistributedIDGenerator) NextID(ctx context.Context) (uint64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.next == g.end {
		allocated, err := g.allocator.Allocate(ctx, g.rangeSize)
		if err != nil {
			return 0, err
		}
		if allocated.Start >= allocated.End || allocated.End-allocated.Start != g.rangeSize {
			return 0, errors.New("allocator returned an invalid range")
		}
		g.next, g.end = allocated.Start, allocated.End
	}
	id := g.next
	g.next++
	return id, nil
}

type Base62Encoder struct{}

func NewBase62Encoder() Base62Encoder { return Base62Encoder{} }
func (Base62Encoder) Encode(id uint64) (string, error) {
	if id > MaxBase62ID {
		return "", fmt.Errorf("ID %d exceeds seven-character Base62 space", id)
	}
	result := [shortKeySize]byte{}
	for index := shortKeySize - 1; index >= 0; index-- {
		result[index] = base62Alphabet[id%62]
		id /= 62
	}
	return string(result[:]), nil
}

type IDKeyGenerator struct {
	IDs interface {
		NextID(context.Context) (uint64, error)
	}
	Encoder Base62Encoder
}

func NewIDKeyGenerator(ids interface {
	NextID(context.Context) (uint64, error)
}) *IDKeyGenerator {
	return &IDKeyGenerator{IDs: ids, Encoder: NewBase62Encoder()}
}
func (g *IDKeyGenerator) Generate() (string, error) {
	id, err := g.IDs.NextID(context.Background())
	if err != nil {
		return "", err
	}
	return g.Encoder.Encode(id)
}
