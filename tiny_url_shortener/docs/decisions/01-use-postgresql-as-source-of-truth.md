# 01. Use PostgreSQL as the Source of Truth for URL Mappings

- Status: Accepted
- Decision date: 2026-08-06
- Related document: [Tiny URL Shortener Design](../design.md)

## Context

The high-availability architecture in the reference book uses a durable
Redis-family key-value store as the final database for URL mappings. Redis can
persist data to disk through RDB snapshots and AOF, so it can serve as a source
of truth.

However, this project has the following requirements and assumptions:

- URL mappings must be safely recoverable after a system failure.
- User ownership, URL deduplication, updates, and deletion will be supported.
- Retaining the original URL mappings for ten years is estimated to require
  about 386.9 TB.
- Short-key and normalized-URL uniqueness must hold even under concurrent
  requests.

Using Redis alone as the source of truth would be expensive because the entire
dataset would reside in memory. Even with AOF and replication, some acknowledged
writes may be lost depending on persistence intervals and asynchronous
replication state, and backup and disaster-recovery policies would have to be
operated separately.

A relational database that provides transactions and unique constraints is a
good fit for data with relationships and constraints such as user ownership and
URL-mapping uniqueness.

## Decision

Use PostgreSQL as the logical source of truth for URL mappings.

Store the following data in PostgreSQL:

- Numeric IDs and Base62 short keys
- Normalized original URLs
- URL creators and ownership
- Creation, update, and expiration timestamps
- Uniqueness constraints on short keys and normalized URLs

Return a successful response for URL creation or modification only after the
PostgreSQL transaction commits. If PostgreSQL is unavailable, fail new URL
creation and changes to existing mappings.

Keep the cache layer for reducing read traffic separate from the source of
truth. The cache product and cache-aside policy are decided in
[ADR 02. Use Redis for the URL Lookup Cache](02-use-redis-for-url-cache.md).

## Consequences

### Advantages

- Transactions and unique constraints control duplicate creation and
  concurrency races.
- Eviction or data loss in the cache does not affect the original URL mappings.
- User ownership, updates, deletion, and expiration policies can be represented
  with a relational model.
- Responsibility for backup, recovery, and data integrity is concentrated in
  the source-of-truth store.

### Disadvantages

- Partitioning and sharding are required to handle large write volumes and
  storage capacity.
- New URL creation and cache-miss lookups are difficult during a PostgreSQL
  outage.
- Transactions, indexes, and replication configurations incur operational cost.

Choosing PostgreSQL does not assume that a single instance can handle 386.9 TB.
PostgreSQL denotes the logical source-of-truth role; before reaching the actual
scale, evaluate data partitioning, sharding, or migration to a managed
distributed database.

## Alternatives Considered

### Use Redis Alone as the Source of Truth

Reads and writes are fast and the structure is simple. However, this project
will not accept the cost of keeping the entire original dataset in memory, the
potential data-loss window of persistence and asynchronous replication, or the
operational complexity of backup and recovery.

### Use a Durable Distributed Key-Value Store

It is suitable for horizontal scaling and simple key-based lookups, but it would
greatly increase the complexity of the current development environment. First
implement the data model and consistency contract with PostgreSQL, then
reconsider this option when actual scale and bottlenecks are observed.

## Follow-up Work

- Define the PostgreSQL schema for URL mappings and ownership.
- Apply unique constraints to short keys and normalized URLs.
- Test transaction boundaries and concurrent-creation conflict handling.
- Define backup, recovery, replication, and failover strategies.
- Record partitioning and sharding decisions for the expected scale in a
  separate ADR.

## References

- [Redis persistence](https://redis.io/docs/latest/operate/oss_and_stack/management/persistence/)
- [Redis replication](https://redis.io/docs/latest/operate/oss_and_stack/management/replication/)
- [PostgreSQL unique constraints](https://www.postgresql.org/docs/current/ddl-constraints.html)
