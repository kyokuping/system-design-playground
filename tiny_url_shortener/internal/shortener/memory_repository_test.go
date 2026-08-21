package shortener

import (
	"context"
	"testing"
	"time"
)

func TestSaveWithOwner_UsesMappingCreatorAsInitialOwner(t *testing.T) {
	repository := NewMemoryRepository()
	mapping := URLMapping{
		ShortKey:       testShortKey,
		LongURL:        parseURL(t, "https://example.com/path"),
		CreatorUserID:  testUserID,
		LastAccessedAt: time.Now(),
	}

	if err := repository.SaveWithOwner(context.Background(), mapping); err != nil {
		t.Fatalf("SaveWithOwner() error = %v", err)
	}
	if _, ok := repository.owners[mapping.ShortKey][mapping.CreatorUserID]; !ok {
		t.Fatalf("creator %q is not the initial owner", mapping.CreatorUserID)
	}
}
