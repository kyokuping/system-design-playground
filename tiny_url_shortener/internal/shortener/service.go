package shortener

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"time"
)

const maxKeyCollisionRetries = 3

// KeyGenerator supplies a candidate seven-character Base62 key.
type KeyGenerator interface {
	Generate() (string, error)
}

type URLStatistics struct {
	ShortKey string
	LongURL  string
	Visits   int
}

// Shortener contains the domain rules and is independent of HTTP and storage.
type Shortener struct {
	repository URLRepository
	generator  KeyGenerator
	now        func() time.Time
}

func New(repository URLRepository, generator KeyGenerator) *Shortener {
	return NewWithClock(repository, generator, time.Now)
}

func NewWithClock(repository URLRepository, generator KeyGenerator, now func() time.Time) *Shortener {
	if now == nil {
		now = time.Now
	}
	return &Shortener{repository: repository, generator: generator, now: now}
}

func (s *Shortener) GetShortURL(userID string, longURL *url.URL) (string, bool, error) {
	if strings.TrimSpace(userID) == "" {
		return "", false, ErrInvalidUserID
	}
	normalized, err := NormalizeURL(longURL)
	if err != nil {
		return "", false, err
	}

	ctx := context.Background()
	if mapping, findErr := s.repository.FindByLongURL(ctx, normalized); findErr == nil {
		if err := s.repository.AddOwner(ctx, URLOwnership{UserID: userID, ShortKey: mapping.ShortKey}); err != nil {
			return "", false, err
		}
		mapping.LastAccessedAt = s.now()
		return mapping.ShortKey, false, nil
	} else if !errors.Is(findErr, ErrURLMappingNotFound) {
		return "", false, findErr
	}

	for attempt := 0; attempt <= maxKeyCollisionRetries; attempt++ {
		shortKey, generateErr := s.generator.Generate()
		if generateErr != nil {
			return "", false, errors.Join(ErrKeyGenerationFailed, generateErr)
		}
		mapping := URLMapping{ShortKey: shortKey, LongURL: normalized, CreatorUserID: userID, LastAccessedAt: s.now()}
		ownership := URLOwnership{UserID: userID, ShortKey: shortKey}
		if saveErr := s.saveWithOwner(ctx, mapping, ownership); saveErr == nil {
			return shortKey, true, nil
		} else if !errors.Is(saveErr, ErrShortURLConflict) {
			return "", false, saveErr
		}

		// A concurrent request may have inserted this URL. Reuse it instead of
		// consuming all key retries.
		if existing, findErr := s.repository.FindByLongURL(ctx, normalized); findErr == nil {
			if ownerErr := s.repository.AddOwner(ctx, URLOwnership{UserID: userID, ShortKey: existing.ShortKey}); ownerErr != nil {
				return "", false, ownerErr
			}
			return existing.ShortKey, false, nil
		} else if !errors.Is(findErr, ErrURLMappingNotFound) {
			return "", false, findErr
		}
	}
	return "", false, ErrKeyGenerationFailed
}

func (s *Shortener) saveWithOwner(ctx context.Context, mapping URLMapping, ownership URLOwnership) error {
	if creator, ok := s.repository.(URLMappingCreator); ok {
		return creator.SaveWithOwner(ctx, mapping, ownership)
	}
	if err := s.repository.Save(ctx, mapping); err != nil {
		return err
	}
	return s.repository.AddOwner(ctx, ownership)
}

func (s *Shortener) GetLongURL(shortKey string) (*url.URL, error) {
	mapping, err := s.findActiveMapping(shortKey)
	if err != nil {
		return nil, err
	}
	now := s.now()
	if recorder, ok := s.repository.(URLAccessRecorder); ok {
		if err := recorder.RecordAccess(context.Background(), shortKey, now); err != nil {
			return nil, err
		}
	}
	return cloneURL(mapping.LongURL), nil
}

// GetURLMapping returns an active mapping without recording a public-link visit.
func (s *Shortener) GetURLMapping(shortKey string) (*url.URL, error) {
	mapping, err := s.findActiveMapping(shortKey)
	if err != nil {
		return nil, err
	}
	return cloneURL(mapping.LongURL), nil
}

func (s *Shortener) findActiveMapping(shortKey string) (URLMapping, error) {
	ctx := context.Background()
	mapping, err := s.repository.FindByShortKey(ctx, shortKey)
	if err != nil {
		return URLMapping{}, err
	}
	now := s.now()
	if !mapping.LastAccessedAt.IsZero() && !now.Before(mapping.LastAccessedAt.AddDate(0, 6, 0)) {
		return URLMapping{}, ErrURLMappingExpired
	}
	return mapping, nil
}

func (s *Shortener) GetCustomShortURL(userID string, longURL *url.URL, customKey string) (string, bool, error) {
	if strings.TrimSpace(userID) == "" {
		return "", false, ErrInvalidUserID
	}
	if !validShortKey(customKey) {
		return "", false, ErrInvalidShortKey
	}
	normalized, err := NormalizeURL(longURL)
	if err != nil {
		return "", false, err
	}
	ctx := context.Background()
	if existing, findErr := s.repository.FindByLongURL(ctx, normalized); findErr == nil {
		if err := s.repository.AddOwner(ctx, URLOwnership{UserID: userID, ShortKey: existing.ShortKey}); err != nil {
			return "", false, err
		}
		return existing.ShortKey, false, nil
	} else if !errors.Is(findErr, ErrURLMappingNotFound) {
		return "", false, findErr
	}
	mapping := URLMapping{ShortKey: customKey, LongURL: normalized, CreatorUserID: userID, LastAccessedAt: s.now()}
	ownership := URLOwnership{UserID: userID, ShortKey: customKey}
	if err := s.saveWithOwner(ctx, mapping, ownership); err != nil {
		return "", false, err
	}
	return customKey, true, nil
}

func (s *Shortener) UpdateLongURL(userID, shortKey string, longURL *url.URL) error {
	if strings.TrimSpace(userID) == "" {
		return ErrInvalidUserID
	}
	normalized, err := NormalizeURL(longURL)
	if err != nil {
		return err
	}
	mutable, ok := s.repository.(MutableURLRepository)
	if !ok {
		return ErrOperationNotSupported
	}
	return mutable.Update(context.Background(), userID, shortKey, normalized)
}

func (s *Shortener) DeleteURL(userID, shortKey string) error {
	if strings.TrimSpace(userID) == "" {
		return ErrInvalidUserID
	}
	mutable, ok := s.repository.(MutableURLRepository)
	if !ok {
		return ErrOperationNotSupported
	}
	return mutable.Delete(context.Background(), userID, shortKey)
}

func (s *Shortener) Statistics(shortKey string) (URLStatistics, error) {
	provider, ok := s.repository.(URLStatisticsRepository)
	if !ok {
		return URLStatistics{}, ErrOperationNotSupported
	}
	return provider.Statistics(context.Background(), shortKey)
}

func NormalizeURL(input *url.URL) (*url.URL, error) {
	if input == nil {
		return nil, ErrInvalidURL
	}
	result := cloneURL(input)
	result.Scheme = strings.ToLower(result.Scheme)
	if result.Scheme != "http" && result.Scheme != "https" || result.Host == "" || result.User != nil {
		return nil, ErrInvalidURL
	}
	hostname := strings.ToLower(result.Hostname())
	if hostname == "" || strings.ContainsAny(hostname, " \t\r\n") {
		return nil, ErrInvalidURL
	}
	port := result.Port()
	if port != "" {
		if _, err := net.LookupPort("tcp", port); err != nil {
			return nil, ErrInvalidURL
		}
	}
	if port == "" || result.Scheme == "http" && port == "80" || result.Scheme == "https" && port == "443" {
		result.Host = hostname
		if strings.Contains(hostname, ":") {
			result.Host = "[" + hostname + "]"
		}
	} else {
		result.Host = net.JoinHostPort(hostname, port)
	}
	if result.Path == "" {
		result.Path = "/"
	}
	result.Fragment = ""
	return result, nil
}

func cloneURL(input *url.URL) *url.URL {
	if input == nil {
		return nil
	}
	copy := *input
	return &copy
}

func validShortKey(key string) bool {
	if len(key) != shortKeySize {
		return false
	}
	for _, character := range key {
		if !strings.ContainsRune(base62Alphabet, character) {
			return false
		}
	}
	return true
}
