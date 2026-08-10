package shortener

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresRepository struct{ pool *pgxpool.Pool }

func (*PostgresRepository) AssignsRevisions() {}

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
	return migratePostgres(ctx, r.pool)
}

func (r *PostgresRepository) Save(ctx context.Context, mapping URLMapping) error {
	_, err := r.pool.Exec(ctx, `INSERT INTO url_mappings (short_key, normalized_url, creator_user_id, last_accessed_at) VALUES ($1,$2,$3,$4)`, mapping.ShortKey, mapping.LongURL.String(), mapping.CreatorUserID, mapping.LastAccessedAt)
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return ErrShortURLConflict
	}
	return err
}

func (r *PostgresRepository) SaveWithOwner(ctx context.Context, mapping URLMapping) error {
	transaction, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	if _, err = transaction.Exec(ctx, `INSERT INTO url_mappings (short_key, normalized_url, creator_user_id, last_accessed_at) VALUES ($1,$2,$3,$4)`, mapping.ShortKey, mapping.LongURL.String(), mapping.CreatorUserID, mapping.LastAccessedAt); err == nil {
		_, err = transaction.Exec(ctx, `INSERT INTO url_owners (user_id, short_key) VALUES ($1,$2)`, mapping.CreatorUserID, mapping.ShortKey)
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
	return scanMapping(r.pool.QueryRow(ctx, `SELECT short_key, normalized_url, creator_user_id, last_accessed_at, revision FROM url_mappings WHERE short_key=$1`, shortKey))
}
func (r *PostgresRepository) FindByLongURL(ctx context.Context, longURL *url.URL) (URLMapping, error) {
	return scanMapping(r.pool.QueryRow(ctx, `SELECT short_key, normalized_url, creator_user_id, last_accessed_at, revision FROM url_mappings WHERE normalized_url=$1`, longURL.String()))
}

type rowScanner interface{ Scan(...any) error }

func scanMapping(row rowScanner) (URLMapping, error) {
	var mapping URLMapping
	var rawURL string
	if err := row.Scan(&mapping.ShortKey, &rawURL, &mapping.CreatorUserID, &mapping.LastAccessedAt, &mapping.Revision); errors.Is(err, pgx.ErrNoRows) {
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

func (r *PostgresRepository) FlushVisits(ctx context.Context, visits []URLVisitDelta) error {
	if len(visits) == 0 {
		return nil
	}

	shortKeys, counts, lastSeen := visitBatchArrays(visits)
	const statement = `UPDATE url_mappings AS m
SET visits = m.visits + d.delta,
    last_accessed_at = GREATEST(m.last_accessed_at, d.last_seen)
FROM unnest($1::text[], $2::bigint[], $3::timestamptz[])
    AS d(short_key, delta, last_seen)
WHERE m.short_key = d.short_key`
	for attempt := 0; ; attempt++ {
		_, err := r.pool.Exec(ctx, statement, shortKeys, counts, lastSeen)
		var postgresError *pgconn.PgError
		if err == nil || !errors.As(err, &postgresError) || postgresError.Code != "40P01" || attempt == 2 {
			return err
		}
	}
}

func visitBatchArrays(visits []URLVisitDelta) ([]string, []int64, []time.Time) {
	visits = slices.Clone(visits)
	slices.SortFunc(visits, func(a, b URLVisitDelta) int { return strings.Compare(a.ShortKey, b.ShortKey) })
	shortKeys := make([]string, len(visits))
	counts := make([]int64, len(visits))
	lastSeen := make([]time.Time, len(visits))
	for i, visit := range visits {
		shortKeys[i], counts[i], lastSeen[i] = visit.ShortKey, visit.Count, visit.LastSeen
	}
	return shortKeys, counts, lastSeen
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
	_, err := r.UpdateWithRevision(ctx, userID, shortKey, longURL)
	return err
}
func (r *PostgresRepository) UpdateWithRevision(ctx context.Context, userID, shortKey string, longURL *url.URL) (URLMapping, error) {
	mapping, err := scanMapping(r.pool.QueryRow(ctx, `UPDATE url_mappings SET normalized_url=$3,updated_at=now(),last_accessed_at=now(),revision=nextval('url_mapping_revision_seq') WHERE short_key=$2 AND creator_user_id=$1 RETURNING short_key,normalized_url,creator_user_id,last_accessed_at,revision`, userID, shortKey, longURL.String()))
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.Code == "23505" {
		return URLMapping{}, ErrShortURLConflict
	}
	if errors.Is(err, ErrURLMappingNotFound) {
		return URLMapping{}, r.missingOrForbidden(ctx, userID, shortKey)
	}
	return mapping, err
}
func (r *PostgresRepository) Delete(ctx context.Context, userID, shortKey string) error {
	_, err := r.DeleteWithRevision(ctx, userID, shortKey)
	return err
}
func (r *PostgresRepository) DeleteWithRevision(ctx context.Context, userID, shortKey string) (int64, error) {
	var revision int64
	err := r.pool.QueryRow(ctx, `DELETE FROM url_mappings WHERE short_key=$2 AND creator_user_id=$1 RETURNING nextval('url_mapping_revision_seq')`, userID, shortKey).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, r.missingOrForbidden(ctx, userID, shortKey)
	}
	return revision, err
}
func (r *PostgresRepository) missingOrForbidden(ctx context.Context, userID, shortKey string) error {
	var mapping, creator bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM url_mappings WHERE short_key=$1), EXISTS(SELECT 1 FROM url_mappings WHERE short_key=$1 AND creator_user_id=$2)`, shortKey, userID).Scan(&mapping, &creator); err != nil {
		return err
	}
	if !mapping {
		return ErrURLMappingNotFound
	}
	if !creator {
		return ErrForbidden
	}
	return ErrConcurrentModification
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
