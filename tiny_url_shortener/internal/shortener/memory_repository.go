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
	mu           sync.RWMutex
	byKey        map[string]*memoryRecord
	byURL        map[string]string
	owners       map[string]map[string]struct{}
	nextRevision int64
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{byKey: map[string]*memoryRecord{}, byURL: map[string]string{}, owners: map[string]map[string]struct{}{}}
}

func (*MemoryRepository) AssignsRevisions() {}

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
	copy.Revision = r.newRevision()
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
	copy.CreatorUserID = owner.UserID
	copy.LongURL = cloneURL(mapping.LongURL)
	copy.Revision = r.newRevision()
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
func (r *MemoryRepository) RecordVisit(key string, at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, exists := r.byKey[key]
	if !exists {
		return
	}
	record.visits++
	if record.mapping.LastAccessedAt.Before(at) {
		record.mapping.LastAccessedAt = at
	}
}
func (r *MemoryRepository) FlushVisits(_ context.Context, visits []URLVisitDelta) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, visit := range visits {
		record, exists := r.byKey[visit.ShortKey]
		if !exists {
			continue
		}
		record.visits += int(visit.Count)
		if record.mapping.LastAccessedAt.Before(visit.LastSeen) {
			record.mapping.LastAccessedAt = visit.LastSeen
		}
	}
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
	_, err := r.update(userID, key, longURL)
	return err
}
func (r *MemoryRepository) UpdateWithRevision(_ context.Context, userID, key string, longURL *url.URL) (URLMapping, error) {
	return r.update(userID, key, longURL)
}
func (r *MemoryRepository) update(userID, key string, longURL *url.URL) (URLMapping, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, exists := r.byKey[key]
	if !exists {
		return URLMapping{}, ErrURLMappingNotFound
	}
	if record.mapping.CreatorUserID != userID {
		return URLMapping{}, ErrForbidden
	}
	if existing, duplicate := r.byURL[longURL.String()]; duplicate && existing != key {
		return URLMapping{}, ErrShortURLConflict
	}
	delete(r.byURL, record.mapping.LongURL.String())
	record.mapping.LongURL = cloneURL(longURL)
	record.mapping.LastAccessedAt = time.Now()
	record.mapping.Revision = r.newRevision()
	r.byURL[longURL.String()] = key
	result := record.mapping
	result.LongURL = cloneURL(result.LongURL)
	return result, nil
}
func (r *MemoryRepository) Delete(_ context.Context, userID, key string) error {
	_, err := r.delete(userID, key)
	return err
}
func (r *MemoryRepository) DeleteWithRevision(_ context.Context, userID, key string) (int64, error) {
	return r.delete(userID, key)
}
func (r *MemoryRepository) delete(userID, key string) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, exists := r.byKey[key]
	if !exists {
		return 0, ErrURLMappingNotFound
	}
	if record.mapping.CreatorUserID != userID {
		return 0, ErrForbidden
	}
	delete(r.byURL, record.mapping.LongURL.String())
	delete(r.byKey, key)
	delete(r.owners, key)
	return r.newRevision(), nil
}

func (r *MemoryRepository) newRevision() int64 {
	r.nextRevision++
	return r.nextRevision
}
