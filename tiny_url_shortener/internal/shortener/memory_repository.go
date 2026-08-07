package shortener

import (
	"context"
	"net/url"
	"sync"
	"time"
)

type memoryRecord struct {
	mapping URLMapping
	visits  int
}

type MemoryRepository struct {
	mu     sync.RWMutex
	byKey  map[string]*memoryRecord
	byURL  map[string]string
	owners map[string]map[string]struct{}
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{byKey: map[string]*memoryRecord{}, byURL: map[string]string{}, owners: map[string]map[string]struct{}{}}
}

func (r *MemoryRepository) Save(_ context.Context, mapping URLMapping) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byKey[mapping.ShortKey]; exists {
		return ErrShortURLConflict
	}
	if _, exists := r.byURL[mapping.LongURL.String()]; exists {
		return ErrShortURLConflict
	}
	copy := mapping
	copy.LongURL = cloneURL(mapping.LongURL)
	r.byKey[mapping.ShortKey] = &memoryRecord{mapping: copy}
	r.byURL[mapping.LongURL.String()] = mapping.ShortKey
	return nil
}
func (r *MemoryRepository) SaveWithOwner(_ context.Context, mapping URLMapping, owner URLOwnership) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byKey[mapping.ShortKey]; exists {
		return ErrShortURLConflict
	}
	if _, exists := r.byURL[mapping.LongURL.String()]; exists {
		return ErrShortURLConflict
	}
	copy := mapping
	copy.LongURL = cloneURL(mapping.LongURL)
	r.byKey[mapping.ShortKey] = &memoryRecord{mapping: copy}
	r.byURL[mapping.LongURL.String()] = mapping.ShortKey
	r.owners[mapping.ShortKey] = map[string]struct{}{owner.UserID: {}}
	return nil
}
func (r *MemoryRepository) AddOwner(_ context.Context, owner URLOwnership) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.byKey[owner.ShortKey]; !exists {
		return ErrURLMappingNotFound
	}
	if r.owners[owner.ShortKey] == nil {
		r.owners[owner.ShortKey] = map[string]struct{}{}
	}
	r.owners[owner.ShortKey][owner.UserID] = struct{}{}
	return nil
}
func (r *MemoryRepository) FindByShortKey(_ context.Context, key string) (URLMapping, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	record, exists := r.byKey[key]
	if !exists {
		return URLMapping{}, ErrURLMappingNotFound
	}
	result := record.mapping
	result.LongURL = cloneURL(result.LongURL)
	return result, nil
}
func (r *MemoryRepository) FindByLongURL(_ context.Context, longURL *url.URL) (URLMapping, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	key, exists := r.byURL[longURL.String()]
	if !exists {
		return URLMapping{}, ErrURLMappingNotFound
	}
	result := r.byKey[key].mapping
	result.LongURL = cloneURL(result.LongURL)
	return result, nil
}
func (r *MemoryRepository) RecordAccess(_ context.Context, key string, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, exists := r.byKey[key]
	if !exists {
		return ErrURLMappingNotFound
	}
	record.visits++
	record.mapping.LastAccessedAt = at
	return nil
}
func (r *MemoryRepository) Statistics(_ context.Context, key string) (URLStatistics, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	record, exists := r.byKey[key]
	if !exists {
		return URLStatistics{}, ErrURLMappingNotFound
	}
	return URLStatistics{ShortKey: key, LongURL: record.mapping.LongURL.String(), Visits: record.visits}, nil
}
func (r *MemoryRepository) Update(_ context.Context, userID, key string, longURL *url.URL) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, exists := r.byKey[key]
	if !exists {
		return ErrURLMappingNotFound
	}
	if _, owned := r.owners[key][userID]; !owned {
		return ErrForbidden
	}
	if existing, duplicate := r.byURL[longURL.String()]; duplicate && existing != key {
		return ErrShortURLConflict
	}
	delete(r.byURL, record.mapping.LongURL.String())
	record.mapping.LongURL = cloneURL(longURL)
	record.mapping.LastAccessedAt = time.Now()
	r.byURL[longURL.String()] = key
	return nil
}
func (r *MemoryRepository) Delete(_ context.Context, userID, key string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, exists := r.byKey[key]
	if !exists {
		return ErrURLMappingNotFound
	}
	if _, owned := r.owners[key][userID]; !owned {
		return ErrForbidden
	}
	delete(r.byURL, record.mapping.LongURL.String())
	delete(r.byKey, key)
	delete(r.owners, key)
	return nil
}
