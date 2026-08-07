package domain

import "errors"

var (
	ErrInvalidUserID         = errors.New("invalid user id")
	ErrInvalidURL            = errors.New("invalid url")
	ErrInvalidShortKey       = errors.New("invalid short key")
	ErrURLMappingNotFound    = errors.New("url mapping not found")
	ErrURLMappingExpired     = errors.New("url mapping expired")
	ErrShortURLConflict      = errors.New("short URL already exists")
	ErrKeyGenerationFailed   = errors.New("short key generation failed")
	ErrForbidden             = errors.New("URL mapping is not owned by user")
	ErrOperationNotSupported = errors.New("operation not supported")
)
