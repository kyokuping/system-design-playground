package shortener

import (
	"context"
	"errors"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const postgresSchema = `
CREATE TABLE IF NOT EXISTS url_mappings (
    short_key TEXT PRIMARY KEY CHECK (char_length(short_key) = 7),
    normalized_url TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_accessed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    visits BIGINT NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS url_owners (
    user_id TEXT NOT NULL,
    short_key TEXT NOT NULL REFERENCES url_mappings(short_key) ON DELETE CASCADE,
    PRIMARY KEY (user_id, short_key)
);
CREATE TABLE IF NOT EXISTS id_allocators (
    name TEXT PRIMARY KEY,
    next_id BIGINT NOT NULL CHECK (next_id >= 0)
);`

type PostgresRepository struct{ pool *pgxpool.Pool }

func OpenPostgres(ctx context.Context, databaseURL string) (*PostgresRepository, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	repository := &PostgresRepository{pool: pool}
	if err := repository.EnsureSchema(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return repository, nil
}

func (r *PostgresRepository) Close() { r.pool.Close() }
func (r *PostgresRepository) Ping(ctx context.Context) error {
	return r.pool.Ping(ctx)
}
func (r *PostgresRepository) EnsureSchema(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, postgresSchema)
	return err
}

func (r *PostgresRepository) Save(ctx context.Context, mapping URLMapping) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO url_mappings (short_key, normalized_url, last_accessed_at) VALUES ($1,$2,$3)`, mapping.ShortKey, mapping.LongURL.String(), mapping.LastAccessedAt)
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return ErrShortURLConflict
	}
	return err
}

func (r *PostgresRepository) SaveWithOwner(ctx context.Context, mapping URLMapping, owner URLOwnership) error {
	transaction, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	if _, err = transaction.Exec(ctx, `INSERT INTO url_mappings (short_key, normalized_url, last_accessed_at) VALUES ($1,$2,$3)`, mapping.ShortKey, mapping.LongURL.String(), mapping.LastAccessedAt); err == nil {
		_, err = transaction.Exec(ctx, `INSERT INTO url_owners (user_id, short_key) VALUES ($1,$2)`, owner.UserID, owner.ShortKey)
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return ErrShortURLConflict
	}
	if err != nil {
		return err
	}
	return transaction.Commit(ctx)
}

func (r *PostgresRepository) AddOwner(ctx context.Context, owner URLOwnership) error {
	result, err := r.pool.Exec(ctx, `INSERT INTO url_owners (user_id, short_key) VALUES ($1,$2) ON CONFLICT DO NOTHING`, owner.UserID, owner.ShortKey)
	var postgresError *pgconn.PgError
	// SQLSTATE 23503 is foreign_key_violation: the referenced URL mapping does not exist.
	if errors.As(err, &postgresError) && postgresError.Code == "23503" {
		return ErrURLMappingNotFound
	}
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		// RowsAffected is also zero for an existing owner, which is successful.
		var exists bool
		if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM url_mappings WHERE short_key=$1)`, owner.ShortKey).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return ErrURLMappingNotFound
		}
	}
	return nil
}

func (r *PostgresRepository) FindByShortKey(ctx context.Context, shortKey string) (URLMapping, error) {
	return scanMapping(r.pool.QueryRow(ctx, `SELECT short_key, normalized_url, last_accessed_at FROM url_mappings WHERE short_key=$1`, shortKey))
}
func (r *PostgresRepository) FindByLongURL(ctx context.Context, longURL *url.URL) (URLMapping, error) {
	return scanMapping(r.pool.QueryRow(ctx, `SELECT short_key, normalized_url, last_accessed_at FROM url_mappings WHERE normalized_url=$1`, longURL.String()))
}

type rowScanner interface{ Scan(...any) error }

func scanMapping(row rowScanner) (URLMapping, error) {
	var mapping URLMapping
	var rawURL string
	if err := row.Scan(&mapping.ShortKey, &rawURL, &mapping.LastAccessedAt); errors.Is(err, pgx.ErrNoRows) {
		return URLMapping{}, ErrURLMappingNotFound
	} else if err != nil {
		return URLMapping{}, err
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return URLMapping{}, err
	}
	mapping.LongURL = parsed
	return mapping, nil
}

func (r *PostgresRepository) RecordAccess(ctx context.Context, shortKey string, at time.Time) error {
	result, err := r.pool.Exec(ctx, `UPDATE url_mappings SET visits=visits+1,last_accessed_at=$2,updated_at=$2 WHERE short_key=$1`, shortKey, at)
	if err == nil && result.RowsAffected() == 0 {
		return ErrURLMappingNotFound
	}
	return err
}
func (r *PostgresRepository) Statistics(ctx context.Context, shortKey string) (URLStatistics, error) {
	var statistics URLStatistics
	err := r.pool.QueryRow(ctx, `SELECT short_key,normalized_url,visits FROM url_mappings WHERE short_key=$1`, shortKey).Scan(&statistics.ShortKey, &statistics.LongURL, &statistics.Visits)
	if errors.Is(err, pgx.ErrNoRows) {
		return URLStatistics{}, ErrURLMappingNotFound
	}
	return statistics, err
}
func (r *PostgresRepository) Update(ctx context.Context, userID, shortKey string, longURL *url.URL) error {
	result, err := r.pool.Exec(ctx, `UPDATE url_mappings m SET normalized_url=$3,updated_at=now() WHERE m.short_key=$2 AND EXISTS(SELECT 1 FROM url_owners o WHERE o.user_id=$1 AND o.short_key=m.short_key)`, userID, shortKey, longURL.String())
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return ErrShortURLConflict
	}
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return r.missingOrForbidden(ctx, userID, shortKey)
	}
	return nil
}
func (r *PostgresRepository) Delete(ctx context.Context, userID, shortKey string) error {
	result, err := r.pool.Exec(ctx, `DELETE FROM url_mappings m WHERE m.short_key=$2 AND EXISTS(SELECT 1 FROM url_owners o WHERE o.user_id=$1 AND o.short_key=m.short_key)`, userID, shortKey)
	if err != nil {
		return err
	}
	if result.RowsAffected() == 0 {
		return r.missingOrForbidden(ctx, userID, shortKey)
	}
	return nil
}
func (r *PostgresRepository) missingOrForbidden(ctx context.Context, userID, shortKey string) error {
	var mapping, owner bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM url_mappings WHERE short_key=$1), EXISTS(SELECT 1 FROM url_owners WHERE user_id=$2 AND short_key=$1)`, shortKey, userID).Scan(&mapping, &owner); err != nil {
		return err
	}
	if !mapping {
		return ErrURLMappingNotFound
	}
	if !owner {
		return ErrForbidden
	}
	return nil
}

type PostgresRangeAllocator struct {
	pool *pgxpool.Pool
	name string
}

func NewPostgresRangeAllocator(repository *PostgresRepository, name string) *PostgresRangeAllocator {
	return &PostgresRangeAllocator{pool: repository.pool, name: name}
}
func (a *PostgresRangeAllocator) Allocate(ctx context.Context, size uint64) (IDRange, error) {
	if size == 0 || size > MaxBase62ID+1 {
		return IDRange{}, ErrKeyGenerationFailed
	}
	transaction, err := a.pool.Begin(ctx)
	if err != nil {
		return IDRange{}, err
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	if _, err := transaction.Exec(ctx, `INSERT INTO id_allocators(name,next_id) VALUES($1,0) ON CONFLICT DO NOTHING`, a.name); err != nil {
		return IDRange{}, err
	}
	var start, end int64
	maxStart := MaxBase62ID - (size - 1)
	err = transaction.QueryRow(ctx, `UPDATE id_allocators SET next_id=next_id+$2 WHERE name=$1 AND next_id <= $3 RETURNING next_id-$2,next_id`, a.name, int64(size), int64(maxStart)).Scan(&start, &end)
	if errors.Is(err, pgx.ErrNoRows) {
		return IDRange{}, ErrKeyGenerationFailed
	}
	if err != nil {
		return IDRange{}, err
	}
	if err := transaction.Commit(ctx); err != nil {
		return IDRange{}, err
	}
	return IDRange{Start: uint64(start), End: uint64(end)}, nil
}
