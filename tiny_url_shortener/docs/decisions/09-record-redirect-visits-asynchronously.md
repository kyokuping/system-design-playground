# 09. Record Redirect Visits Asynchronously in Batches

- Status: Proposed
- Created: 2026-08-10
- Related documents:
  - [Tiny URL Shortener Design](../design.md)
  - [01. Use PostgreSQL as the Source of Truth for URL Mappings](01-use-postgresql-as-source-of-truth.md)
  - [02. Use Redis for the URL Lookup Cache](02-use-redis-for-url-cache.md)
  - [06. Preserve Expiration and Version Metadata in the URL Cache](06-preserve-expiration-metadata-in-url-cache.md)
  - [07. Separate the Public Redirect Path from the Management API](07-define-public-api-paths-and-responses.md)

## Context

Public redirects are the highest-traffic path in this service. The current
implementation synchronously performs the following for every redirect:

```text
1. Look up the mapping in the cache or PostgreSQL
2. PostgreSQL UPDATE: visits, last_accessed_at, updated_at, revision
3. Rewrite the positive Redis entry with the mapping returned by step 2
```

Steps 2 and 3 cause the following problems:

- Every request creates one PostgreSQL write, regardless of whether the lookup
  hits the cache. The read cache reduces read load but not write load.
- One popular short key becomes a write-contention point on a single row. The
  better the cache works, the more traffic concentrates on that row.
- `revision` increases on every lookup. ADR 05 and ADR 06 introduced it for
  cache consistency, but a lookup that changes no content triggers its own
  cache rewrite.
- A visit-recording failure becomes a redirect failure. A request that already
  found the mapping returns `500` because of a statistics-write error.

The three values updated by this statement require different consistency
levels.

| Value | Purpose | Required consistency |
| --- | --- | --- |
| `visits` | Statistics lookup | An approximation is sufficient |
| `last_accessed_at` | Six-month expiration decision | Hour-level error is acceptable |
| `revision` | Cache consistency | Meaningful only when content changes |

None requires read-your-writes within the request. A redirect response needs
only the lookup result; the visit can be applied after the response.

## Decision

Remove visit recording from the redirect response path. Accumulate visits in an
in-memory buffer and have a background worker periodically apply them to
PostgreSQL in one batch statement. Increase `revision` only when mapping content
changes.

```text
redirect request
  |
  | 1. Look up mapping (cache first)
  | 2. Accumulate visit in memory (non-blocking)
  v
307 response

background worker
  |
  | Periodically swap the buffer and apply a batch
  v
PostgreSQL: visits += delta, last_accessed_at = GREATEST(...)
```

### Visit-Recording Path

The redirect path uses only this contract:

- Visit recording does not block.
- Visit recording returns no error. Observe failures only through counters and
  logs.
- Its result does not affect the response.

The buffer is a map keyed by short key that stores an accumulated count and the
latest visit time. Consecutive visits to the same key are combined in the
buffer, so the application cost for a popular key scales with unique keys
rather than request count.

Bound buffer memory. If the number of distinct keys exceeds the limit within
one period, request an immediate flush; if it still exceeds the limit, drop
visits and increment a loss counter. Preventing statistics from delaying the
redirect path or consuming unbounded memory takes priority.

### Batch Application

Apply one period's accumulated deltas with one statement.

```sql
UPDATE url_mappings AS m
SET visits = m.visits + d.delta,
    last_accessed_at = GREATEST(m.last_accessed_at, d.last_seen)
FROM unnest($1::text[], $2::bigint[], $3::timestamptz[])
    AS d(short_key, delta, last_seen)
WHERE m.short_key = d.short_key
```

- Add each `visits` delta. Results accumulate even when application instances
  flush their own buffers independently.
- Update `last_accessed_at` with `GREATEST`, so the timestamp never moves
  backward when instance batches arrive out of order.
- Do not update `updated_at` or `revision`; a visit does not change mapping
  content.
- Ignore short keys absent from the update target. Visits for a mapping deleted
  before the flush disappear silently, as intended.
- Sort the batch by short key before sending it to align lock order between
  instances and reduce deadlocks. Retry a deadlock (`40P01`) only for a batch
  confirmed not to have committed.

### Narrowing the Meaning of `revision`

Increase `revision` only in `UpdateWithRevision` and `DeleteWithRevision`.
Lookups do not change it.

The cache-consistency rules from ADR 05 and ADR 06 remain unchanged. Cache
writes still conditionally accept only greater revisions, and invalidation uses
the same comparison. The only change is that lookups no longer create a new
revision. Consequently, Redis writes occur only when filling a cache miss or
applying an actual change.

### Cache TTL and Expiration Boundary (Revision to ADR 06)

ADR 06 decided to update `last_accessed_at` in the positive Redis entry after
every successful lookup. This prevented a request near expiration from
extending the PostgreSQL boundary while leaving an old cache timestamp that
caused the next request to incorrectly return `410 Gone`.

This decision supersedes that part of ADR 06. Because lookups no longer update
the cache, prevent the same problem by limiting a positive entry's TTL to its
expiration boundary.

```text
ttl = min(positiveTTL, lastAccessedAt + 6 months - now)
do not cache when ttl <= 0
```

An entry cannot live past its own expiration boundary, so a cache hit never
has to determine that it has expired. An already-expired mapping is not cached
and is evaluated through PostgreSQL, which also continues preventing the
reactivation problem addressed by ADR 06. The cache still stores the exact
`last_accessed_at` returned by PostgreSQL and does not overwrite it with lookup
time.

ADR 06 considered and rejected this alternative. At the time, every lookup was
already writing Redis, so a write-through update was accurate at no additional
cost, while limiting TTL caused an unnecessary miss at the expiration boundary.
Once lookups stop writing Redis, the premise changes: write-through becomes the
only reason to retain a Redis write in the lookup path, and the cost of limiting
TTL is a negligible cache miss approximately once every six months per mapping.

The remaining ADR 06 decisions are unchanged: the cache preserves metadata
needed for expiration, retains the serialization format, and treats invalid
entries as misses. The value format does not change, so neither does the key
version.

## Failure Handling

- An abnormal application exit loses visits since the last flush. The flush
  period bounds the loss.
- On graceful shutdown, flush the remaining buffer before exiting.
- If a batch fails, retry it in the next period. Redirects remain unaffected.
- If repeated failures fill the buffer to its limit, discard the oldest deltas.
  Statistics loss has lower priority than redirect availability.
- Resume applying batches in the next period after PostgreSQL recovers.
- Do not store the visit buffer in the cache Redis instance. For the same reason
  as ADR 03, do not add state to a cache that permits data loss and eviction.

## Consequences

### Advantages

- PostgreSQL writes disappear from the redirect path. Write load scales with
  unique short keys and the flush period rather than request count.
- Write contention on rows for popular short keys disappears.
- Statistics-recording failures do not affect redirect responses.
- Lookups do not write Redis. Cache writes occur only on miss fills and actual
  changes.
- `revision` represents the version of mapping content as its name implies.
- Removing lookup-oriented access-recording paths and related repository
  interfaces simplifies the storage layer.

### Disadvantages

- `visits` becomes approximate. An abnormal exit loses up to one flush period
  of visits.
- Statistics lag by the flush period and buffers held by other instances.
- The application must manage visit-buffer state and depend on graceful
  shutdown handling.
- Approximately one extra cache miss occurs per mapping every six months at the
  expiration boundary.
- Concurrent batch updates from multiple instances require a deadlock-retry
  path.

## Alternatives Considered

### Keep Synchronous Updates on Every Lookup

This keeps `visits` exact and the implementation simple. However, the read cache
does not reduce write load at all, and contention on one row increases with key
popularity. It was rejected because read traffic is this service's scaling axis.

### Accumulate with Redis `INCR` and Drain

Counts survive process failure and combine naturally between instances.
However, it adds one Redis round trip to the redirect path and requires a
single-run guarantee for the worker draining into PostgreSQL. ADR 03 also
decided not to store durable counters in a cache that allows loss. Reconsider
this with a separate Redis instance if accuracy requirements increase.

### Send Visit Events to a Log or Queue for Separate Aggregation

This is closer to the right design at very large scale and enables analysis by
time and referrer. However, it requires a message broker and aggregation
pipeline, while the current needs are only total visits and last access time.
It is excessive at this stage.

### Append Visit Events to a Separate Table and Aggregate

This removes contention on `url_mappings` rows but retains one PostgreSQL write
per request. It does not solve the core problem and was rejected.

### Remove Visit Recording

This is simplest, but `last_accessed_at` is required for the six-month
expiration policy. Giving up statistics does not eliminate the access-time
update.

### Sampling

Recording only a percentage of visits reduces write volume. However, an
infrequently accessed link may not update `last_accessed_at` and could expire
while still in use. It was rejected because the value determines expiration.

## Follow-up Work

- Define the visit-buffer contract. The redirect path uses only a non-blocking
  record operation, and storage exposes only an operation that applies
  accumulated deltas.
- Implement batch application in PostgreSQL and in-memory stores.
- Remove lookup-oriented access-recording interfaces and the access-recording
  path from the cache layer.
- Add a regression test proving that lookups do not increase `revision`.
- Limit positive-cache TTL to the expiration boundary and test that mappings
  beyond that boundary are not cached.
- Make the flush period and buffer limit configurable and choose defaults by
  load testing.
- Expose manual flushing to enable deterministic tests without timing.
- Update server shutdown to flush the remaining buffer gracefully.
- Expose lost-buffer count, flush latency, and batch size as metrics.
- Mark the ADR 06 section on access time and cache TTL as superseded by this
  decision.
