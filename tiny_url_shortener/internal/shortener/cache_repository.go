package shortener

import (
	"context"
	"errors"
	"net/url"
	"time"
)

var ErrCacheMiss = errors.New("cache miss")

type CachedURL struct {
	LongURL        string
	LastAccessedAt time.Time
	Revision       int64
	Negative       bool
}

// URLCache is deliberately smaller than a Redis client so cache behavior can
// be tested without a running server and replaced without affecting the domain.
type URLCache interface {
	Get(ctx context.Context, shortKey string) (CachedURL, error)
	SetPositive(ctx context.Context, mapping URLMapping, ttl time.Duration) error
	SetNegativeIfAbsent(ctx context.Context, shortKey string, ttl time.Duration) (bool, error)
	Delete(ctx context.Context, shortKey string) error
	Invalidate(ctx context.Context, shortKey string, revision int64, positiveTTL time.Duration) error
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
	mapping = r.persistedMapping(ctx, mapping)
	_ = r.cache.SetPositive(ctx, mapping, r.positiveTTL)
	return nil
}

func (r *CachedRepository) SaveWithOwner(ctx context.Context, mapping URLMapping, owner URLOwnership) error {
	creator, ok := r.source.(URLMappingCreator)
	if !ok {
		return ErrOperationNotSupported
	}
	if err := creator.SaveWithOwner(ctx, mapping, owner); err != nil {
		return err
	}
	mapping = r.persistedMapping(ctx, mapping)
	_ = r.cache.SetPositive(ctx, mapping, r.positiveTTL)
	return nil
}

func (r *CachedRepository) AddOwner(ctx context.Context, ownership URLOwnership) error {
	return r.source.AddOwner(ctx, ownership)
}

func (r *CachedRepository) FindByShortKey(ctx context.Context, shortKey string) (URLMapping, error) {
	if cached, err := r.cache.Get(ctx, shortKey); err == nil {
		if cached.Negative {
			return URLMapping{}, ErrURLMappingNotFound
		}
		if mapping, ok := mappingFromCache(shortKey, cached); ok {
			return mapping, nil
		}
	}

	mapping, err := r.source.FindByShortKey(ctx, shortKey)
	if err != nil {
		if errors.Is(err, ErrURLMappingNotFound) {
			stored, cacheErr := r.cache.SetNegativeIfAbsent(ctx, shortKey, r.negativeTTL)
			if cacheErr == nil && !stored {
				if cached, getErr := r.cache.Get(ctx, shortKey); getErr == nil {
					if cached.Negative {
						return URLMapping{}, ErrURLMappingNotFound
					}
					if winner, ok := mappingFromCache(shortKey, cached); ok {
						return winner, nil
					}
				}
			}
		}
		return URLMapping{}, err
	}
	_ = r.cache.SetPositive(ctx, mapping, r.positiveTTL)
	return mapping, nil
}

func mappingFromCache(shortKey string, cached CachedURL) (URLMapping, bool) {
	parsed, err := url.Parse(cached.LongURL)
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" || cached.LastAccessedAt.IsZero() {
		return URLMapping{}, false
	}
	return URLMapping{
		ShortKey:       shortKey,
		LongURL:        parsed,
		LastAccessedAt: cached.LastAccessedAt,
		Revision:       cached.Revision,
	}, true
}

func (r *CachedRepository) persistedMapping(ctx context.Context, fallback URLMapping) URLMapping {
	if _, ok := r.source.(RevisionedRepository); !ok {
		return fallback
	}
	mapping, err := r.source.FindByShortKey(ctx, fallback.ShortKey)
	if err != nil || mapping.LongURL == nil || mapping.Revision <= fallback.Revision {
		return fallback
	}
	return mapping
}

func (r *CachedRepository) FindByLongURL(ctx context.Context, longURL *url.URL) (URLMapping, error) {
	return r.source.FindByLongURL(ctx, longURL)
}

func (r *CachedRepository) RecordAccess(ctx context.Context, shortKey string, at time.Time) error {
	if recorder, ok := r.source.(RevisionedAccessRecorder); ok {
		mapping, err := recorder.RecordAccessWithRevision(ctx, shortKey, at)
		if err != nil {
			return err
		}
		if err := r.cache.SetPositive(ctx, mapping, r.positiveTTL); err != nil {
			_ = r.cache.Invalidate(ctx, shortKey, mapping.Revision, r.positiveTTL)
		}
		return nil
	}
	recorder, ok := r.source.(URLAccessRecorder)
	if !ok {
		return nil
	}
	if err := recorder.RecordAccess(ctx, shortKey, at); err != nil {
		return err
	}
	if cached, err := r.cache.Get(ctx, shortKey); err == nil && !cached.Negative {
		if mapping, valid := mappingFromCache(shortKey, cached); valid {
			mapping.LastAccessedAt = at
			if err := r.cache.SetPositive(ctx, mapping, r.positiveTTL); err != nil {
				_ = r.cache.Delete(ctx, shortKey)
			}
		}
	}
	return nil
}

func (r *CachedRepository) Statistics(ctx context.Context, shortKey string) (URLStatistics, error) {
	provider, ok := r.source.(URLStatisticsRepository)
	if !ok {
		return URLStatistics{}, ErrOperationNotSupported
	}
	return provider.Statistics(ctx, shortKey)
}

func (r *CachedRepository) Update(ctx context.Context, userID, shortKey string, longURL *url.URL) error {
	if mutable, ok := r.source.(RevisionedMutableURLRepository); ok {
		mapping, err := mutable.UpdateWithRevision(ctx, userID, shortKey, longURL)
		if err != nil {
			return err
		}
		if err := r.cache.SetPositive(ctx, mapping, r.positiveTTL); err != nil {
			_ = r.cache.Invalidate(ctx, shortKey, mapping.Revision, r.positiveTTL)
		}
		return nil
	}
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
	if mutable, ok := r.source.(RevisionedMutableURLRepository); ok {
		revision, err := mutable.DeleteWithRevision(ctx, userID, shortKey)
		if err != nil {
			return err
		}
		_ = r.cache.Invalidate(ctx, shortKey, revision, r.positiveTTL)
		return nil
	}
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
