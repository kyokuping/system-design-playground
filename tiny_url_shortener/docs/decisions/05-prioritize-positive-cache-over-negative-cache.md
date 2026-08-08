# 05. Prefer Positive Cache Entries with Conditional Negative Caching

- Status: Accepted
- Decision date: 2026-08-07
- Related documents:
  - [Tiny URL Shortener Design](../design.md)
  - [02. Use Redis for the URL Lookup Cache](02-use-redis-for-url-cache.md)
  - [03. Generate Short-URL IDs with a Distributed Counter](03-use-distributed-counter-for-url-ids.md)
  - [06. Preserve Expiration and Version Metadata in the URL Cache](06-preserve-expiration-metadata-in-url-cache.md)

## Context

ADR 02 selected Redis cache-aside with negative caching for approximately 30
seconds. Lookups and creation currently update the cache in this order:

```text
lookup: Redis miss -> PostgreSQL not found -> Redis negative SET
create: PostgreSQL INSERT/COMMIT -> Redis DEL
```

Because PostgreSQL and Redis do not participate in one transaction, the
following race is possible:

1. A lookup observes a Redis miss and PostgreSQL not found.
2. A creation request commits the same short key to PostgreSQL.
3. The creation request deletes the Redis key, but no cache entry exists yet.
4. The lookup writes a negative entry afterward.
5. Subsequent lookups cannot see the mapping in PostgreSQL until the negative
   TTL expires.

Keys produced by Base62-encoding the distributed counter make the next value
easy to guess, so lookups for not-yet-issued keys must also be considered.
Simply shortening the negative TTL reduces only the duration of an incorrect
404; it does not eliminate the race.

At large read scale, negative caching is needed to keep repeated lookups and
scans for missing keys from reaching PostgreSQL. Instead of removing negative
caching, use Redis atomic conditional writes so a positive mapping has higher
priority than a negative marker.

## Decision

Define cache-state priority as follows:

```text
positive mapping > negative marker > no cache entry
```

After the PostgreSQL commit, URL creation writes through the positive value
instead of deleting the cache key. Write negative values with Redis `SET ... NX`
only when no cache entry exists.

- PostgreSQL remains the source of truth for URL mappings.
- A successful URL creation is determined by the PostgreSQL transaction commit.
- After the creation commit, write the positive cache value with a normal `SET`.
- A positive `SET` may overwrite an existing negative value.
- Write a negative value only with `SET NX`, so it cannot overwrite an existing
  positive value.
- If the negative `SET NX` is rejected, read the cache entry written by the
  winner.
- A Redis write failure does not roll back the PostgreSQL transaction.
- Fall back to PostgreSQL while Redis is unavailable.
- Retain the ADR 02 jitter policy for positive and negative TTLs.

## Cache Format

Represent positive and negative states with one versioned cache format. Follow
[ADR 06](06-preserve-expiration-metadata-in-url-cache.md) for expiration
metadata, serialization, and key-version policy.

## Write Protocol

### When the Lookup Result Exists

```text
1. Redis GET
2. On a cache miss, PostgreSQL SELECT
3. If the mapping exists, Redis SET positive
4. Return the mapping
```

### When the Lookup Result Does Not Exist

```text
1. Redis GET
2. On a cache miss, PostgreSQL SELECT
3. If no mapping exists, Redis SET negative NX
4. If SET NX succeeds, return not found
5. If SET NX fails, use Redis GET to recheck the current positive/negative state
```

If Redis still returns no value after `SET NX` fails, return not found based on
the PostgreSQL result. If creation ran concurrently with this request's
PostgreSQL lookup, the lookup can be considered to have been handled before the
creation.

### When Creating a New Mapping

```text
1. Transactionally store the mapping and initial owner in PostgreSQL
2. Commit succeeds
3. Redis SET positive
4. Return creation success
```

If the lookup writes a negative value first, the creation's positive `SET`
overwrites it. If creation writes the positive value first, the lookup's
negative `SET NX` fails. Therefore, while Redis operates normally, a late
negative write cannot overwrite a new positive mapping.

## Consistency Guarantees

This decision resolves the **race between new-mapping creation and a negative
cache write**. When Redis responds normally and the post-creation positive write
succeeds, a negative write that started before creation cannot hide the new
mapping for the duration of its TTL.

If the Redis positive write fails after the PostgreSQL commit, creation still
succeeds. If Redis contains an existing negative value, stale not found may be
returned for at most the negative TTL. Observe and mitigate this failure window
as follows:

- Record positive-cache write failures in a dedicated metric.
- Retry cache writes a limited number of times with backoff.
- Keep the negative TTL short to bound the inconsistency window.
- Introduce outbox- or CDC-based cache updates if repeated failures are
  observed.

This decision does not cover a stale lookup that repopulates the cache after a
concurrent update or deletion. Before exposing those operations, compare
mapping versions, Redis Lua scripts, outbox, and CDC in a separate ADR.

## Consequences

### Advantages

- Negative caching reduces PostgreSQL load from missing keys and key scans.
- Redis atomic operations protect positive values from late negative writes.
- Resolving creation races does not require querying PostgreSQL twice for every
  negative miss.

### Disadvantages

- Cache-value serialization, state branching, and `SET NX` result handling are
  added.
- The creation path performs a Redis positive write after the PostgreSQL commit.
- Because PostgreSQL and Redis do not share a distributed transaction, strong
  read-after-write consistency is not guaranteed during Redis write failures.
- Update and deletion races require an additional version or event-based
  invalidation policy.

## Alternatives Considered

### Remove Negative Caching

This simplifies the implementation and consistency model. However, every
repeated lookup and scan of predictable keys would reach PostgreSQL, so this is
not the default policy for a large-scale read environment. Rate limiting can be
used alongside it but cannot absorb misses shared across regions and IPs.

### Requery PostgreSQL After Writing a Negative Entry

This makes creation races easier to detect but queries PostgreSQL twice for
every missing key. It could further increase source-store load during a key
scan, so it was rejected.

### Delayed Double Deletion After Creation

A second deletion can remove a cache entry populated after the first deletion.
However, it remains vulnerable to lookups paused longer than the delay, and the
delivery and retries of the background operation must be managed. Directly
writing the already-known positive value at creation is clearer.

### Update PostgreSQL and Redis in a Distributed Transaction

This can strongly couple the states of both stores, but including the Redis
cache in the source-of-truth transaction boundary reduces availability and
increases implementation complexity. It conflicts with the ADR 02 principle
that the cache is reconstructable derived data.

### Introduce a Versioned Cache Immediately

This can control stale writes from updates and deletion as well as creation.
However, the current public API is limited to creation and lookup, so first
resolve the creation race with state priority. Extend it through a follow-up
decision before exposing updates and deletion.

## Follow-up Work

- Make `SetNegativeIfAbsent` use Redis `SET NX` and return whether it succeeded.
- Write the positive value instead of deleting the cache after a new mapping
  commits.
- Write a concurrency test that controls the order of creation and negative
  cache writes.
- Write an integration test for `SET NX` against a real Redis instance.
- Monitor positive-cache write failures, negative hit ratio, and PostgreSQL
  fallback ratio.
- Apply rate limiting and request coalescing to key scans.
- Decide versioned caching and outbox/CDC policy separately before exposing
  update and deletion operations.
