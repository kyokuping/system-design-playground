# 02. Use Redis for the URL Lookup Cache

- Status: Accepted
- Decision date: 2026-08-06
- Related documents:
  - [Tiny URL Shortener Design](../design.md)
  - [01. Use PostgreSQL as the Source of Truth for URL Mappings](01-use-postgresql-as-source-of-truth.md)

## Context

This system assumes a read-to-write ratio of approximately 100:1 and estimates
peak read traffic at about 231,600 QPS. The hottest 20% of URLs are expected to
account for most read traffic, and the target cache hit ratio is at least 80%.

Because PostgreSQL is the source of truth for URL mappings, cached data can
always be reconstructed from PostgreSQL. Memcached and Redis were evaluated as
candidates for the URL lookup cache.

## Comparison of Alternatives

| Item | Memcached | Redis |
| --- | --- | --- |
| Primary use | Simple distributed cache | Cache and data-structure server |
| Basic data model | String or byte values | String, Hash, Set, Sorted Set, and others |
| Eviction policies | Primarily LRU | Configurable LRU, LFU, TTL, Random, and others |
| Atomic operations | `incr`, `decr`, CAS | `INCR`, `INCRBY`, `SET NX`, transactions, scripts |
| Persistence | Not supported | Optional RDB and AOF |
| Replication and failover | Not built in | Replica, Sentinel, and Cluster support |
| Operational complexity | Relatively low | Increases with the number of features used |

Memcached is sufficient when the only requirement is a simple GET/SET cache
that looks up an original URL by `shortKey`. Its simple structure makes it easy
to treat loss of cached data as a normal event.

Redis supports TTLs, negative caching, and multiple eviction policies. In
particular, the assumption that traffic is concentrated on a subset of URLs
allows an LFU policy to retain data based on lookup frequency.

## Decision

Use Redis for URL lookups and apply the cache-aside pattern.

```text
Client
  |
  v
Nginx
  |
  v
Go application
  |-- Redis cache
  `-- PostgreSQL source of truth
```

Redis is not the source of truth for URL mappings. PostgreSQL must retain the
URL mappings even if all Redis data is deleted or Redis stops responding.

The initial cache format and policy are:

```text
key:   cache:url:v1:{shortKey}
value: originalURL
TTL:   1 hour with a small random variation
```

- Initially use `allkeys-lfu` as the eviction policy.
- Reconsider `allkeys-lru` if real traffic shows little variation in lookup
  frequency between URLs.
- Negatively cache missing keys for approximately 30 seconds.
- Add random variation to TTLs to reduce simultaneous expiration of hot keys.
- Observe cache hit ratio, miss ratio, eviction count, and Redis latency.
- Do not rely on RDB or AOF for correctness of the cache Redis instance.
- Do not use Nginx response caching initially.

## Failure Handling

On a Redis cache hit, redirect to the stored original URL. On a cache miss,
look up the URL mapping in PostgreSQL, populate Redis, and redirect.

If Redis does not respond, bypass the cache and query PostgreSQL directly. A
Redis failure may therefore increase lookup latency and PostgreSQL load, but it
must not immediately cause loss of source data or complete lookup failure.

If a Redis node failure or restart empties the cache, many requests may reach
PostgreSQL at the same time. Apply TTL jitter to reduce this effect and, if
needed, add request coalescing so concurrent misses for the same key result in
one source lookup.

## Separation from the Distributed Counter

Because eviction is allowed in the cache Redis instance, do not store the
durable distributed ID counter there. The ID allocation method and storage
location are decided in
[ADR 03. Generate Short-URL IDs with a Distributed Counter](03-use-distributed-counter-for-url-ids.md).

## Consequences

### Advantages

- The `allkeys-lfu` policy can be applied to hot URLs.
- PostgreSQL read traffic and connection-pool load are reduced.
- TTLs, negative caching, and request deduplication can be extended with Redis
  features.
- Requests can fall back to PostgreSQL during a Redis failure.

### Disadvantages

- Compared with a simple URL cache in Memcached, Redis has more operational
  features and settings.
- Cache-miss and invalidation logic is required between Redis and PostgreSQL.
- PostgreSQL load may increase while the cache is repopulated after a Redis
  failure.
- Deployment configuration must keep cache state separate from durable state.

## Alternatives Considered

### Use Memcached

For a simple GET/SET cache, Memcached offers a smaller feature set and lower
operational complexity. However, the current design considers an LFU policy for
hot URLs, negative caching, and TTL policies, making Redis the better fit.

### Use an Application-Local Cache

This avoids network requests, but each application instance would maintain a
separate cache, duplicate memory, and make invalidation on updates and deletion
difficult. Apply the Redis cache first and consider an L1 local cache only if
measurements show an additional benefit.

### Use the Nginx Response Cache

This can respond very quickly by bypassing the application and Redis, but visit
counting and URL expiration checks may be skipped. Use Nginx only as a load
balancer in the initial design.

## Follow-up Work

- Finalize the Redis cache-key and value-serialization formats.
- Write tests for cache hits, misses, negative caching, and Redis failure
  fallback.
- Decide the TTL jitter range and cache-stampede prevention mechanism.
- Collect cache-hit and eviction metrics.
- Compare `allkeys-lfu` and `allkeys-lru` using real load-test results.

## References

- [Memcached overview](https://docs.memcached.org/)
- [Memcached configuration and threading](https://docs.memcached.org/serverguide/configuring/)
- [Redis data types](https://redis.io/docs/latest/develop/data-types/)
- [Redis eviction policies](https://redis.io/docs/latest/develop/reference/eviction/)
- [Redis cache-aside](https://redis.io/docs/latest/develop/use-cases/cache-aside/)
