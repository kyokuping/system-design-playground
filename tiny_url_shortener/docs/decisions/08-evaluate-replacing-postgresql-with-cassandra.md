# 08. Evaluate Replacing PostgreSQL with Cassandra

- Status: Proposed
- Decision date: 2026-08-09
- Related documents:
  - [Tiny URL Shortener Design](../design.md)
  - [01. Use PostgreSQL as the Source of Truth for URL Mappings](01-use-postgresql-as-source-of-truth.md)
  - [03. Generate Short-URL IDs with a Distributed Counter](03-use-distributed-counter-for-url-ids.md)
  - [04. Separate URL Creators from Shared Ownership](04-separate-url-creator-from-shared-ownership.md)

## Context

PostgreSQL is currently the source of truth and stores:

- Mappings between short keys and normalized URLs
- URL creators and shared ownership
- Visit counts and last-access timestamps
- Revisions for updates and deletion

The store does not generate short keys. A distributed ID generator issues
globally unique numeric IDs and converts them to seven-character Base62 short
keys. Therefore, the URL-mapping database does not need to enforce uniqueness
of automatically generated short keys again.

The primary URL lookup pattern finds one mapping by `short_key`. Each short key
represents an independent mapping, so transactions across all URL mappings and
global consistency are unnecessary.

## Replacement Feasibility

### Short-Key Mapping

Using `short_key` as the Cassandra partition key makes the primary lookup a
single-partition operation.

```sql
CREATE TABLE url_mappings_by_short_key (
    short_key text PRIMARY KEY,
    normalized_url text,
    creator_user_id text,
    last_accessed_at timestamp,
    revision bigint
);
```

If the distributed ID generator never issues the same short key to different
requests, each partition has one writer at creation. No competing insertion of
a different URL occurs after a mapping is created, so a global database-level
unique constraint is unnecessary.

If read-after-write is required immediately after creation, use an appropriate
consistency level for that request. This does not require global serialization
or a multi-partition transaction.

### Existing-Mapping Lookup by Normalized URL

The current service reuses an existing short key when it receives the same
normalized URL. This feature can use a separate lookup table:

```sql
CREATE TABLE short_key_by_normalized_url (
    normalized_url_hash blob PRIMARY KEY,
    normalized_url text,
    short_key text
);
```

This table does not enforce uniqueness of automatically generated short keys.
It is a reverse index for reusing an existing short key for the same long URL.

If temporary inconsistency between the mapping and reverse-lookup tables is
acceptable, they do not have to be written in one transaction. Make writes
idempotent and retry or repair failed writes. If concurrent requests must always
return the same short key, use a lightweight transaction on the normalized-URL
hash partition. If that guarantee is unnecessary, remove the reverse-lookup
table entirely.

### Ownership, Updates, and Deletion

Store creator information in the same partition as the short-key mapping. If a
shared-ownership list is needed, represent it in another table partitioned by
short key.

An update changes the mapping for one short key. If concurrent updates require
coordination, use revision-based compare-and-set. No transaction across all URL
mappings is required.

If a mapping, reverse lookup, and shared ownership do not have to disappear at
the same instant, delete each independently and allow temporary inconsistency.
Writing a deletion revision or tombstone first prevents a deleted mapping from
reappearing before cleanup finishes.

### Distributed ID Generator

This decision assumes that the distributed ID generator guarantees short-key
uniqueness independently of the URL-mapping store. Removing the current range
allocation state from PostgreSQL as well would require allocating ranges with a
Cassandra lightweight transaction or switching to an ID-generation method that
does not require stored central allocation state.

That is an implementation concern for the independent ID generator, separate
from whether URL mappings can be stored in Cassandra.

### Custom Short Keys

User-defined custom keys bypass the distributed ID generator. To continue
supporting them, use `INSERT ... IF NOT EXISTS` on the `short_key` partition so
an existing mapping cannot be overwritten.

## Decision

PostgreSQL does not have to remain the source of truth for URL mappings;
Cassandra can replace it.

The reasons are:

- The distributed ID generator guarantees uniqueness of generated short keys.
- Primary reads and writes fit within one `short_key` partition.
- Transactions and global consistency across different short keys are
  unnecessary.
- Reverse URL lookups, shared ownership, and deletion cleanup can be maintained
  asynchronously with temporary inconsistency and repair.
- Partition-level lightweight transactions can be used selectively only for
  custom keys and concurrent creation of the same URL, which require stronger
  coordination.

This ADR does not decide to migrate immediately. It decides only that Cassandra
can express the current functional contract and is therefore a possible
replacement for PostgreSQL.

## Consequences

- Cassandra is recognized as a candidate replacement for the PostgreSQL
  URL-mapping store.
- Replacement assumes that the distributed ID generator owns short-key
  uniqueness.
- Temporary inconsistency is accepted during reverse URL lookup and deletion.
- The migration decision, operating cost, and migration method are outside the
  scope of this ADR.
