# 07. Separate the Public Redirect Path from the Management API

- Status: Accepted
- Created: 2026-08-08
- Decision date: 2026-08-09
- Related document: [Tiny URL Shortener Design](../design.md)

## Context

The URL-creation API requires authentication, request validation, and write rate
limiting, while public redirects have much higher read traffic and benefit from
independent scaling through a CDN or edge cache. A shared short URL must also
continue working for a long time even when the API version changes.

If redirects and management lookups use one API path, the same path can acquire
two meanings: a redirect and a JSON lookup. Public links also become needlessly
long and coupled to an API version.

## Decision

Separate the public redirect path from the versioned Shortener API.

```http
POST /api/v1/short-urls
GET /api/v1/short-urls/{shortKey}
GET /{shortKey}
```

- `POST /api/v1/short-urls` creates a mapping or reuses an existing one.
- `GET /api/v1/short-urls/{shortKey}` returns management information about the
  mapping as JSON.
- `GET /{shortKey}` is the shared public link and redirects to the original URL.

Use the resource-oriented plural noun `short-urls` consistently for the API
collection. The path variable represents the seven-character Base62 key rather
than a complete URL, so name it `{shortKey}`.

Use the same application binary, but expose the first two endpoints from the
Shortener API role and only the public redirect from the Redirect role. Local
development can serve all three endpoints from one process with
`SERVER_ROLE=all`.

## API Contract

### Create a Short URL

```http
POST /api/v1/short-urls
Content-Type: application/json
```

```json
{
  "user_id": "user-123",
  "url": "https://example.com/very/long/path"
}
```

A new mapping returns `201 Created` and the management-resource path.

```http
HTTP/1.1 201 Created
Content-Type: application/json
Location: /api/v1/short-urls/Ab12Cd3
```

```json
{
  "short_key": "Ab12Cd3",
  "short_url": "https://tiny.url/Ab12Cd3",
  "long_url": "https://example.com/very/long/path"
}
```

Reusing an existing mapping for the normalized original URL returns the same
body with `200 OK`. An invalid request returns `400 Bad Request`. Because there
is no authentication layer yet, accept `user_id` in the body; when
authentication is introduced, obtain the user identity from a token or API key.

### Management Mapping Lookup

```http
GET /api/v1/short-urls/Ab12Cd3
```

On success, return the same mapping information as the creation response in a
`200 OK` JSON body. This is not a public-link visit, so it does not update the
visit count or `last_accessed_at`. Return `404 Not Found` if the mapping does not
exist and `410 Gone` if it has expired.

The current response does not expose storage-layer internals such as
`created_at`, `last_accessed_at`, or `revision`. Decide whether to expose them,
together with the authorization model, when the external contract requires it.

### Public Redirect

```http
GET /Ab12Cd3
```

```http
HTTP/1.1 307 Temporary Redirect
Location: https://example.com/very/long/path
```

On success, return `307 Temporary Redirect` with a `Location` header and no JSON
body. Return `404 Not Found` if the mapping does not exist and `410 Gone` if it
has expired. Only successful public redirects update the visit count and last
access time.

Do not use a permanent redirect such as `301` or `308`, because subsequent
requests must continue reaching the service for destination updates, URL
expiration, and visit analytics. When adding a cache policy, evaluate it
together with the consistency requirements for visit aggregation and
destination changes.

## Consequences

- Shared links are short and independent of the API version.
- Paths distinguish the response semantics of management lookups and redirects.
- Shortener API and Redirect traffic can be deployed and scaled independently.
- When both roles share a host, the gateway must route only root paths that are
  exactly seven Base62 characters to the Redirect server and route `/api/`
  paths to the Shortener API server.

## Alternatives Considered

### Place Every Path Under `/api/v1`

```http
POST /api/v1/short-urls
GET /api/v1/short-urls/{shortKey}
```

This reduces the number of paths but creates a semantic conflict between the
management lookup and redirect and couples shared URLs to an API version, so it
was rejected.

### Keep a Verb-Based Creation Path

```http
POST /api/v1/shorten
```

This expresses the core operation directly but differs from the management
resource collection used for later lookups, updates, and deletion. Because HTTP
`POST` already expresses creation, use a noun-based collection.
