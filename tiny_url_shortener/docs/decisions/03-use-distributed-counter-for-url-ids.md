# 03. Generate Short-URL IDs with a Distributed Counter

- Status: Accepted
- Decision date: 2026-08-07
- Related documents:
  - [Tiny URL Shortener Design](../design.md)
  - [01. Use PostgreSQL as the Source of Truth for URL Mappings](01-use-postgresql-as-source-of-truth.md)
  - [02. Use Redis for the URL Lookup Cache](02-use-redis-for-url-cache.md)

## Context

Creating a short URL requires a globally unique numeric ID. Encoding that ID
with Base62 produces a seven-character short key.

Updating one central counter for every request concentrates all writes at the
same point. Random keys or URL hashes avoid a central counter but require
collision checks and a retry policy.

This project aims to implement and study the large-scale distributed-system
design from the reference book. Although it is more complex to implement and
operate than a simple central sequence, use a range-based distributed counter
to reduce request concentration on the central counter.

## Decision

Assign each ID Generator instance a non-overlapping range of numeric IDs. Each
instance increments an in-memory local counter within its assigned range and
returns the resulting ID. It requests a new range from the Range Allocation
Service only after exhausting the current range.

```text
App
  |
  | request next ID
  v
ID Generator Cluster
  |
  | refill only when local range is exhausted
  v
Range Allocation Service
  |
  | atomically reserve range
  v
PostgreSQL: ID Allocation State
```

The Range Allocation Service transactionally advances the next allocation
position in PostgreSQL. Use a row lock or an atomic `UPDATE ... RETURNING` so
that two requests cannot receive the same range.

The conceptual allocation-state schema is:

```sql
CREATE TABLE id_allocators (
    name TEXT PRIMARY KEY,
    next_id BIGINT NOT NULL
);
```

Initially, place this table in the same PostgreSQL cluster as the URL mappings.
`ID Allocation State` is not a separate database containing every issued ID; it
is logical state that stores the start of the next range to allocate.

Do not hard-code the initial range size as an implementation constant. Make it
configurable, verify behavior and failure handling with a small value, and then
choose a value by measuring range-refill frequency and wasted IDs under load.

## Short-Key Conversion

Encode each issued numeric ID with Base62. Left-pad results shorter than seven
characters with `0`, and do not issue IDs greater than `62^7 - 1`.

Directly converting an increasing number to Base62 makes keys easy to guess or
enumerate. Initially verify distributed-counter correctness; before public use,
consider a reversible transformation such as a keyed permutation or a separate
obfuscation policy.

## Failure Handling

- Discard any unused range when an ID Generator shuts down.
- Do not guarantee contiguous IDs; gaps are allowed.
- Do not reassign a discarded range to another instance.
- An ID Generator does not depend on the Range Allocation Service until it
  exhausts its current range.
- Resume allocating new ranges after the Range Allocation Service and
  PostgreSQL recover.
- Do not store ID allocation state in the cache Redis instance.
- Apply unique constraints to the numeric ID and seven-character short key in
  PostgreSQL as the final safeguard against duplicates.

In the current design, PostgreSQL is also the source of truth for URL mappings.
Therefore, even if an ID Generator still has IDs in its range, a URL-creation
request does not succeed if its mapping cannot be committed to PostgreSQL.

## Consequences

### Advantages

- URL-creation requests do not update the central counter store individually.
- Adding ID Generator instances distributes ID-generation load.
- PostgreSQL transactions prevent overlapping range allocations.
- Eviction or data loss in the Redis cache does not affect ID generation.

### Disadvantages

- The ID Generator, Range Allocation Service, and allocation state must be
  operated.
- Unused IDs are lost when an instance fails or is deployed.
- A poorly chosen range size and refill policy can bottleneck the allocation
  service or waste many IDs.
- Base62 results from sequential numbers are predictable.
- If the PostgreSQL allocator cannot handle the required scale, ranges must be
  separated by region or stored in a dedicated distributed consensus system.

## Alternatives Considered

### PostgreSQL Sequence

This is the simplest and most practical choice at the expected write volume.
However, every ID generation would depend on the central sequence, and it would
not provide experience with the distributed ID-generation structure that this
project is intended to study.

### Redis `INCR`

This is atomic and fast, but the current Redis instance is a cache where data
loss and eviction are allowed. Do not store a durable ID counter in that same
Redis instance. Operating a separate durable Redis instance is possible, but
the failure and persistence policies of the cache and counter would have to be
separated.

### Random Base62 Key

This requires no central counter and can be generated directly by the
application, but it requires collision detection and retry logic. This project
uses the distributed counter to guarantee ID uniqueness.

### URL Hash

It is easy to produce the same result from the same URL, but collisions occur
when reducing the value to a seven-character space. Handle URL deduplication
with a unique constraint on the normalized URL, and generate short-key IDs with
the distributed counter.

## Follow-up Work

- Define the repository contract for the Range Allocation Service.
- Write an integration test proving that concurrent range requests do not
  overlap.
- Test range exhaustion, reallocation, and instance-restart behavior in the ID
  Generator.
- Expose the range size as configuration and choose its default through load
  testing.
- Test numeric-ID Base62 conversion, seven-character padding, and the maximum
  boundary.
- Record the obfuscation approach for mitigating sequential-key enumeration in
  a separate decision.
