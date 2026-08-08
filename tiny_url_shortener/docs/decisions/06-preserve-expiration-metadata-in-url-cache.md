# 06. Preserve Expiration and Version Metadata in the URL Cache

- Status: Accepted
- Decision date: 2026-08-07
- Related documents:
  - [Tiny URL Shortener Design](../design.md)
  - [02. Use Redis for the URL Lookup Cache](02-use-redis-for-url-cache.md)
  - [05. Prefer Positive Cache Entries with Conditional Negative Caching](05-prioritize-positive-cache-over-negative-cache.md)

## Context

A short URL expires six months after its last access. The service layer compares
the `last_accessed_at` returned by the repository with the current time to
distinguish active URLs from expired URLs.

The Redis cache initially defined in ADR 02 stored only the original URL string.

```text
key:   cache:url:v1:{shortKey}
value: originalURL
```

A PostgreSQL lookup returns both the original URL and `last_accessed_at`, but a
v1 cache hit reconstructs a `URLMapping` from only the original URL. In that
case, `LastAccessedAt` has Go's zero value and the service layer cannot determine
whether the mapping has expired.

In particular, reading an expired mapping from PostgreSQL on a cache miss can
cause the following problem:

1. The application reads an expired mapping from PostgreSQL.
2. Before the service layer checks expiration, the original URL is stored in
   Redis.
3. The first request treats the mapping as expired based on the PostgreSQL
   access timestamp.
4. The next request reads a mapping without an access timestamp from Redis.
5. It can skip expiration, update the access record, and reactivate the expired
   mapping.

The cache is not the source of truth, but cache hits and misses must produce the
same domain decision. Therefore, the cache must preserve the metadata required
by the service layer's expiration decision.

## Decision

Continue determining URL expiration in the service layer. The positive Redis
cache stores both the original URL and the last-access timestamp required for
the service to make the same decision.

The conceptual positive-cache format is:

```json
{
  "state": "positive",
  "long_url": "https://example.com/",
  "last_accessed_at": "2026-08-07T12:00:00Z",
  "revision": 1024
}
```

Represent the negative state from ADR 05 in the same serialization format.

```json
{
  "state": "negative"
}
```

The cache contract follows these rules:

- A positive cache hit returns a `URLMapping` containing `long_url` and
  `last_accessed_at`.
- The service layer applies the same expiration rules to PostgreSQL and Redis
  results.
- Do not record visits or the last-access timestamp for an expired mapping.
- Treat a positive value with an empty or invalid URL, or a zero
  `last_accessed_at`, as a cache miss.
- Treat invalid JSON or an unknown `state` as a cache miss.
- Recover from Redis errors and invalid cache entries by querying PostgreSQL.
- Because cache data is derived, never change PostgreSQL data in response to a
  corrupted Redis value.
- Store the PostgreSQL-issued `revision` in positive values and restore it on a
  cache hit.

## Cache Key

Although the value format changes from a string to JSON, retain the
`cache:url:v1:` key prefix. Record the cache-format revision as a correction to
the v1 design rather than a new key version.

```text
before: cache:url:v1:{shortKey}          value: originalURL
after:  cache:url:v1:{shortKey}:entry    value: JSON
```

Use the same rule for the key that stores the revision of the same short key.

```text
cache:url:v1:{shortKey}:revision   value: last applied revision
```

`{shortKey}` is the Redis Cluster hash tag, placing both keys in the same slot.
A suffix identifying each purpose follows the hash tag. The new key string does
not overlap the old key. There is no need to bulk-delete old keys before
deployment; they are no longer read and are eventually removed by TTL or
eviction.

Even if keys did overlap, a value that cannot be decoded as JSON is treated as
a cache miss, so the old string format is not misinterpreted as JSON. Because
the cache is derived data and undecodable values recover through PostgreSQL,
do not increment the key version for every format revision.

## Access Time and Cache TTL

After a successful URL lookup, update PostgreSQL `last_accessed_at`. Also update
`last_accessed_at` in the positive Redis value to the same time on every lookup.
Otherwise, a successful lookup may extend expiration in PostgreSQL while the
next request incorrectly returns `410 Gone` based on the cache's older
timestamp.

Update access records in this order:

```text
1. Update visits and last_accessed_at in PostgreSQL
2. If a positive entry exists in Redis, update it with the same last_accessed_at
3. A Redis update failure does not roll back the PostgreSQL access record
```

Reapply the approximately one-hour TTL and jitter when rewriting the positive
cache value. This decision adds one Redis write to every successful lookup, but
prioritizes keeping the same sliding-expiration basis for cache hits and
PostgreSQL results.

## Rejecting Stale Cache Writes

Reflecting access timestamps in the cache increases write frequency and the
chance that writes arrive out of order. Another request can write between the
time a read path fetches a value from PostgreSQL and writes it back to the cache.

1. Request A reads a mapping from PostgreSQL after a cache miss.
2. Request B updates or deletes the same mapping and invalidates the cache.
3. Request A writes the value read in step 1 to the cache afterward.
4. The cache returns the old URL until its TTL expires. If the mapping was
   deleted, redirects continue to a URL that no longer exists in the source.

Deleting the cache after a write cannot prevent this ordering. B's deletion
arrives before A's write-back, so the invalidation succeeds only for the stale
value to be repopulated immediately. Wall-clock timestamps are not guaranteed
to increase monotonically across application instances and PostgreSQL, so they
cannot determine which value is newer.

Use a monotonically increasing revision issued by PostgreSQL as the mapping
version.

```sql
CREATE SEQUENCE url_mapping_revision_seq;

ALTER TABLE url_mappings
    ADD COLUMN revision BIGINT NOT NULL DEFAULT nextval('url_mapping_revision_seq');
```

Creation, updates, deletion, and access recording each issue a new revision and
return it to the caller. Because deletion removes the row, it returns only the
revision issued at deletion time.

Cache writes use a Lua script to atomically operate on the entry and revision
keys. If the incoming revision is less than the stored revision, do nothing.

```text
SetPositive: if newer, update the revision and store the entry with its TTL
Invalidate:  if newer, update the revision and delete the entry
```

Compare revision strings by length first and lexicographically only when their
lengths match. Lua numbers are double-precision floating-point values and may
lose precision for large integers, so do not rely on `tonumber`.

- Invalidate the cache with the issued revision after an update or deletion.
- If a positive write fails, invalidate with the same revision so no old value
  remains.
- Stores that do not support revisions continue using the existing invalidation
  path.
- A cache-write failure does not roll back a PostgreSQL write.

## Consequences

### Advantages

- Cache hits and PostgreSQL results apply the same expiration policy.
- An expired mapping cannot be reactivated because an access timestamp was
  omitted.
- Treating the old value format as a cache miss keeps mixed-format deployments
  safe.
- The cache-key name is not coupled to format history through key-version
  management.
- Invalid cache entries recover from the source store as cache misses.
- Keeping expiration policy in the service layer avoids duplicating it across
  repository implementations.
- A late write-back cannot overwrite a newer value.
- A stale read cannot repopulate a deleted mapping in the cache.
- Recency depends on a monotonically increasing PostgreSQL sequence instead of
  wall-clock time.

### Disadvantages

- JSON serialization and deserialization add cost compared with a plain string.
- Storing state and an access timestamp in every positive entry increases memory
  usage.
- Every successful lookup adds a Redis write in addition to the PostgreSQL
  write.
- Old-format values remain immediately after deployment, temporarily increasing
  cache misses and PostgreSQL lookups.
- Every write, including access recording, consumes a sequence value.
- Two keys per short key increase memory use and key count.
- The revision key has no TTL and remains after the entry disappears.
- The cache-write path depends on Lua scripts, making behavior harder to trace.

## Alternatives Considered

### Cache Only the Original URL and Skip Expiration Checks

This simplifies the cache format and lookup path, but cache hits and misses
would return different results. It can reactivate expired mappings and was
therefore rejected.

### Move the Expiration Check into the PostgreSQL Query

If PostgreSQL does not return expired mappings, the cache does not need to store
the access timestamp. However, cache hits still require separate expiration
information, and distinguishing `404 Not Found` from `410 Gone` would require
additional state or another query. This was rejected to preserve the service
layer's domain-error contract.

### Cache Only the Absolute Expiration Time

The service would not need to calculate `last_accessed_at + 6 months` on every
request. However, this freezes the calendar-month calculation when the cache is
created and requires managing both source and derived fields. Preserving the
source metadata is clearer.

### Delete the Cache Only After a Write

This is simple and adds no key. However, if deletion arrives before a write-back
in the ordering above, the stale value is immediately repopulated. It leaves the
problem unresolved and was rejected.

### Determine Recency from the Last-Access Timestamp

Comparing the already-cached `last_accessed_at` would avoid adding a column.
However, updates and deletion without access records would have no comparison
basis, and clock differences between application instances could reverse the
order. It was therefore rejected.

### Increment the Key-Prefix Version to Separate Formats

Incrementing the prefix to `cache:url:v2:` for each format change prevents
different formats from sharing a key. However, undecodable values are already
treated as misses, so this separation prevents no additional problem and would
require updating versions scattered across code and documentation for every
format adjustment. Exposing format history in cache-key names is also
undesirable.

### Limit Cache TTL to the Existing Expiration Boundary

Limiting the TTL to `last_accessed_at + 6 months` would avoid updating Redis on
every access. However, immediately after an access, the old boundary would
still cause a cache miss and PostgreSQL requery, and TTL calculation would
duplicate expiration policy. Use explicit write-through updates instead.

## Follow-up Work

- Retain the `cache:url:v1:` key prefix and distinguish purposes with suffixes.
- Define explicit serialization types for positive and negative cache values.
- Store `long_url` and `last_accessed_at` in positive values.
- Reconstruct `URLMapping` with `LastAccessedAt` on a cache hit.
- After successfully recording access in PostgreSQL, also update
  `last_accessed_at` in the positive Redis value.
- Treat a positive value without `last_accessed_at` as a cache miss.
- Add a regression test proving repeated lookups of an expired mapping all
  return `ErrURLMappingExpired` without recording access.
- Test that invalid JSON, unknown states, and missing required fields are treated
  as cache misses.
- Measure cache-entry size and JSON-serialization cost under load.
- Give revision keys a TTL sufficiently longer than the entry TTL to prevent
  unbounded growth.
- Observe the count of rejected stale writes to measure actual race frequency.
- Decide when to remove repository paths that do not support revisions.
