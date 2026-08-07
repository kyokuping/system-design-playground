package shortener

import (
	"context"
	"net/url"
	"time"

	"kyoku.dev/system-design-playground/tiny_url_shortener/internal/domain"
)

var (
	ErrInvalidUserID         = domain.ErrInvalidUserID
	ErrInvalidURL            = domain.ErrInvalidURL
	ErrInvalidShortKey       = domain.ErrInvalidShortKey
	ErrURLMappingNotFound    = domain.ErrURLMappingNotFound
	ErrURLMappingExpired     = domain.ErrURLMappingExpired
	ErrShortURLConflict      = domain.ErrShortURLConflict
	ErrKeyGenerationFailed   = domain.ErrKeyGenerationFailed
	ErrForbidden             = domain.ErrForbidden
	ErrOperationNotSupported = domain.ErrOperationNotSupported
)

type URLMapping struct {
	ShortKey       string
	LongURL        *url.URL
	CreatorUserID  string
	LastAccessedAt time.Time
}

type URLOwnership struct {
	UserID   string
	ShortKey string
}

type URLRepository interface {
	Save(ctx context.Context, mapping URLMapping) error
	AddOwner(ctx context.Context, ownership URLOwnership) error
	FindByShortKey(ctx context.Context, shortKey string) (URLMapping, error)
	FindByLongURL(ctx context.Context, longURL *url.URL) (URLMapping, error)
}

// URLMappingCreator lets durable repositories commit the mapping and its first
// owner atomically. Shortener falls back to the base interface for simple test
// repositories.
type URLMappingCreator interface {
	SaveWithOwner(ctx context.Context, mapping URLMapping, ownership URLOwnership) error
}

type URLAccessRecorder interface {
	RecordAccess(ctx context.Context, shortKey string, at time.Time) error
}

type URLStatisticsRepository interface {
	Statistics(ctx context.Context, shortKey string) (URLStatistics, error)
}

type MutableURLRepository interface {
	Update(ctx context.Context, userID, shortKey string, longURL *url.URL) error
	Delete(ctx context.Context, userID, shortKey string) error
}
