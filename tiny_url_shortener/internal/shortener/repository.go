package shortener

import (
	"context"
	"errors"
	"net/url"
)

var (
	ErrInvalidUserID       = errors.New("invalid user id")
	ErrInvalidURL          = errors.New("invalid url")
	ErrURLMappingNotFound  = errors.New("url mapping not found")
	ErrURLMappingExpired   = errors.New("url mapping expired")
	ErrShortURLConflict    = errors.New("short URL already exists")
	ErrKeyGenerationFailed = errors.New("short key generation failed")
)

type URLMapping struct {
	ShortKey string
	LongURL  *url.URL
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
