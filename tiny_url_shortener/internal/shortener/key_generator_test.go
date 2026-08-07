package shortener

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"testing"

	"kyoku.dev/system-design-playground/tiny_url_shortener/internal/handler"
)

const shortKeyLength = 7

type keyGeneratorContract interface {
	Generate() (string, error)
}

func newBase62KeyGenerator() keyGeneratorContract {
	return NewRandomKeyGenerator()
}

func newShortenerWithDependencies(
	repository URLRepository,
	generator keyGeneratorContract,
) handler.URLService {
	return New(repository, generator)
}

func TestKeyGenerator_GeneratesFixedLengthBase62Key(t *testing.T) {
	skipExpectedFailure(t)

	generator := newBase62KeyGenerator()
	base62Pattern := regexp.MustCompile(`^[0-9A-Za-z]{7}$`)

	for range 100 {
		shortKey, err := generator.Generate()
		if err != nil {
			t.Fatalf("Generate() returned an unexpected error: %v", err)
		}
		if len(shortKey) != shortKeyLength {
			t.Fatalf("Generate() key length = %d, want %d", len(shortKey), shortKeyLength)
		}
		if !base62Pattern.MatchString(shortKey) {
			t.Fatalf("Generate() = %q, want only Base62 characters", shortKey)
		}
	}
}

func TestGetShortURL_RetriesThreeTimesAfterKeyCollisions(t *testing.T) {
	skipExpectedFailure(t)

	repository := &collisionRepository{conflictsRemaining: 3}
	generator := &sequenceKeyGenerator{
		keys: []string{"Collide", "Collide", "Collide", "Success"},
	}
	service := newShortenerWithDependencies(repository, generator)

	shortKey, created, err := service.GetShortURL(testUserID, parseURL(t, "https://example.com/alpha"))
	if err != nil {
		t.Fatalf("GetShortURL() returned an unexpected error: %v", err)
	}
	if shortKey != "Success" {
		t.Fatalf("GetShortURL() = %q, want %q", shortKey, "Success")
	}
	if !created {
		t.Fatal("GetShortURL() created = false, want true")
	}
	if generator.calls != 4 {
		t.Fatalf("Generate() calls = %d, want 4", generator.calls)
	}
}

func TestGetShortURL_ReturnsDomainErrorAfterRetryLimit(t *testing.T) {
	skipExpectedFailure(t)

	repository := &collisionRepository{conflictsRemaining: 4}
	generator := &sequenceKeyGenerator{
		keys: []string{"Collide", "Collide", "Collide", "Collide"},
	}
	service := newShortenerWithDependencies(repository, generator)

	_, _, err := service.GetShortURL(testUserID, parseURL(t, "https://example.com/alpha"))
	if !errors.Is(err, ErrKeyGenerationFailed) {
		t.Fatalf("GetShortURL() error = %v, want ErrKeyGenerationFailed", err)
	}
	if generator.calls != 4 {
		t.Fatalf("Generate() calls = %d, want 4", generator.calls)
	}
}

type sequenceKeyGenerator struct {
	keys  []string
	calls int
}

func (g *sequenceKeyGenerator) Generate() (string, error) {
	if g.calls >= len(g.keys) {
		return "", errors.New("test key sequence exhausted")
	}

	shortKey := g.keys[g.calls]
	g.calls++
	return shortKey, nil
}

type collisionRepository struct {
	conflictsRemaining int
	saveCalls          int
}

func (r *collisionRepository) Save(_ context.Context, _ URLMapping) error {
	r.saveCalls++
	if r.conflictsRemaining > 0 {
		r.conflictsRemaining--
		return ErrShortURLConflict
	}
	return nil
}

func (r *collisionRepository) AddOwner(_ context.Context, _ URLOwnership) error {
	return nil
}

func (r *collisionRepository) FindByShortKey(
	_ context.Context,
	_ string,
) (URLMapping, error) {
	return URLMapping{}, ErrURLMappingNotFound
}

func (r *collisionRepository) FindByLongURL(
	_ context.Context,
	_ *url.URL,
) (URLMapping, error) {
	return URLMapping{}, ErrURLMappingNotFound
}
