package shortener

import (
	"context"
	"errors"
	"net/url"
	"time"
)

var ErrCacheMiss = errors.New("cache miss")

// URLCache is deliberately smaller than a Redis client so cache behavior can
// be tested without a running server and replaced without affecting the domain.
type URLCache interface {
	Get(ctx context.Context, shortKey string) (longURL string, negative bool, err error)
	Set(ctx context.Context, shortKey, longURL string, ttl time.Duration) error
	SetNegative(ctx context.Context, shortKey string, ttl time.Duration) error
	Delete(ctx context.Context, shortKey string) error
}

// CachedRepository applies cache-aside reads. Cache failures are best-effort:
// PostgreSQL (the wrapped source) remains authoritative.
type CachedRepository struct {
	source      URLRepository
	cache       URLCache
	positiveTTL time.Duration
	negativeTTL time.Duration
}

func NewCachedRepository(source URLRepository, cache URLCache, positiveTTL, negativeTTL time.Duration) *CachedRepository {
	return &CachedRepository{source: source, cache: cache, positiveTTL: positiveTTL, negativeTTL: negativeTTL}
}

func (r *CachedRepository) Save(ctx context.Context, mapping URLMapping) error {
	if err := r.source.Save(ctx, mapping); err != nil {
		return err
	}
	_ = r.cache.Delete(ctx, mapping.ShortKey)
	return nil
}

func (r *CachedRepository) SaveWithOwner(ctx context.Context, mapping URLMapping, owner URLOwnership) error {
	if creator, ok := r.source.(URLMappingCreator); ok {
		if err := creator.SaveWithOwner(ctx, mapping, owner); err != nil {
			return err
		}
	} else {
		if err := r.source.Save(ctx, mapping); err != nil {
			return err
		}
		if err := r.source.AddOwner(ctx, owner); err != nil {
			return err
		}
	}
	_ = r.cache.Delete(ctx, mapping.ShortKey)
	return nil
}

func (r *CachedRepository) AddOwner(ctx context.Context, ownership URLOwnership) error {
	return r.source.AddOwner(ctx, ownership)
}

func (r *CachedRepository) FindByShortKey(ctx context.Context, shortKey string) (URLMapping, error) {
	if rawURL, negative, err := r.cache.Get(ctx, shortKey); err == nil {
		if negative {
			return URLMapping{}, ErrURLMappingNotFound
		}
		parsed, parseErr := url.Parse(rawURL)
		if parseErr == nil {
			return URLMapping{ShortKey: shortKey, LongURL: parsed}, nil
		}
	}

	mapping, err := r.source.FindByShortKey(ctx, shortKey)
	if err != nil {
		if errors.Is(err, ErrURLMappingNotFound) {
			_ = r.cache.SetNegative(ctx, shortKey, r.negativeTTL)
		}
		return URLMapping{}, err
	}
	_ = r.cache.Set(ctx, shortKey, mapping.LongURL.String(), r.positiveTTL)
	return mapping, nil
}

func (r *CachedRepository) FindByLongURL(ctx context.Context, longURL *url.URL) (URLMapping, error) {
	return r.source.FindByLongURL(ctx, longURL)
}

func (r *CachedRepository) RecordAccess(ctx context.Context, shortKey string, at time.Time) error {
	recorder, ok := r.source.(URLAccessRecorder)
	if !ok {
		return nil
	}
	return recorder.RecordAccess(ctx, shortKey, at)
}

func (r *CachedRepository) Statistics(ctx context.Context, shortKey string) (URLStatistics, error) {
	provider, ok := r.source.(URLStatisticsRepository)
	if !ok {
		return URLStatistics{}, ErrOperationNotSupported
	}
	return provider.Statistics(ctx, shortKey)
}

func (r *CachedRepository) Update(ctx context.Context, userID, shortKey string, longURL *url.URL) error {
	mutable, ok := r.source.(MutableURLRepository)
	if !ok {
		return ErrOperationNotSupported
	}
	if err := mutable.Update(ctx, userID, shortKey, longURL); err != nil {
		return err
	}
	_ = r.cache.Delete(ctx, shortKey)
	return nil
}

func (r *CachedRepository) Delete(ctx context.Context, userID, shortKey string) error {
	mutable, ok := r.source.(MutableURLRepository)
	if !ok {
		return ErrOperationNotSupported
	}
	if err := mutable.Delete(ctx, userID, shortKey); err != nil {
		return err
	}
	_ = r.cache.Delete(ctx, shortKey)
	return nil
}
