# 04. Separate URL Creators from Shared Ownership

- Status: Accepted
- Decision date: 2026-08-07
- Related documents:
  - [Tiny URL Shortener Design](../design.md)
  - [01. Use PostgreSQL as the Source of Truth for URL Mappings](01-use-postgresql-as-source-of-truth.md)

## Context

This service reuses an existing short key whenever the normalized original URL
matches, regardless of the user. When someone other than the original creator
shortens the same URL, the service relates that user to the existing mapping
instead of creating another URL mapping.

The current data model treats every user linked through `url_owners` as an
equal owner. Update and deletion operations check only whether that relationship
exists. As a result, a user who requests the same URL later can update or delete
the original creator's mapping.

For example, the following sequence is possible:

1. `user-1` shortens a URL and creates `Ab12Cd3`.
2. `user-2` requests the same URL and receives the shared `Ab12Cd3`.
3. `user-2` changes the original URL or deletes the mapping.
4. Both `user-1` and all existing short-URL visitors are affected.

This conflicts with the requirement that the URL creator updates the original
URL or deletes the mapping. The relationship that shares a short key must be
separated from authority to manage the global mapping.

## Decision

Separate the URL mapping's **creator** from the **user relationship** that
shares its short key.

- Every URL mapping has exactly one `creator_user_id`.
- Record the user who first creates the mapping as its creator.
- When another user later requests the same URL, add only a `url_owners`
  relationship.
- Include the creator in `url_owners` as well.
- A `url_owners` relationship allows a user to view and manage a short key in
  their own list; it does not grant permission to change the global URL mapping.
- Permit original-URL updates and URL-mapping deletion only when the requester
  matches `creator_user_id`.
- Return `ErrForbidden` for update or deletion requests from non-creators.
- Repeated requests for an existing URL by the same user and additions of the
  ownership relationship are idempotent.

The conceptual schema is:

```sql
CREATE TABLE url_mappings (
    short_key TEXT PRIMARY KEY,
    normalized_url TEXT NOT NULL UNIQUE,
    creator_user_id TEXT NOT NULL,
    -- timestamps and statistics
);

CREATE TABLE url_owners (
    user_id TEXT NOT NULL,
    short_key TEXT NOT NULL REFERENCES url_mappings(short_key) ON DELETE CASCADE,
    PRIMARY KEY (user_id, short_key)
);
```

Store a mapping and its initial creator relationship in one PostgreSQL
transaction. When storing a new mapping, both
`url_mappings.creator_user_id` and the creator's `url_owners` row must commit or
roll back together.

Existing data does not record when ownership relationships were created. During
migration, select the lexicographically smallest linked `user_id` as each
mapping's creator. Treat a mapping without any owner as inconsistent data, fail
the migration, and require an operator to repair it.

Do not base update and deletion authorization on a result first read by the
application. Include the creator condition in the mutation itself so the check
and change are atomic:

```sql
UPDATE url_mappings
SET normalized_url = $3, updated_at = now()
WHERE short_key = $2 AND creator_user_id = $1;

DELETE FROM url_mappings
WHERE short_key = $2 AND creator_user_id = $1;
```

If no row is affected, check whether the mapping exists to distinguish
`ErrURLMappingNotFound` from `ErrForbidden`.

## Effects of Changing a Shared Mapping

When the creator updates the original URL, every user sharing the same short key
and every external visitor is redirected to the new URL. When the creator
deletes the mapping, `ON DELETE CASCADE` deletes all `url_owners` relationships
and the short key stops working.

This is intentional under the current model, where one short key has one global
mapping. When the update and deletion APIs are exposed, their contracts and user
interfaces must state that the operation also affects users sharing the key.

If a requirement arises for each user to retain a different destination, do not
update the global mapping directly. Decide a copy-on-write policy that issues a
new short key in a separate ADR.

## Consequences

### Advantages

- A short key can be shared for the same URL while management authority remains
  limited to the original creator.
- A later user requesting the same URL cannot take over or delete the global
  mapping.
- The data model clearly distinguishes creation authority from per-user list
  relationships.
- PostgreSQL queries can atomically authorize and apply mutations.

### Disadvantages

- A creator's update or deletion affects every user sharing the key.
- A separate policy is required for mappings whose creator account is deleted
  or deactivated.
- The true original creator cannot be recovered from existing data, requiring a
  lexicographic fallback.
- An extra lookup may be required after a failed mutation to distinguish a
  missing mapping from insufficient permission.

## Alternatives Considered

### Allow Every Owner to Update and Delete

This is closest to the current implementation and convenient for collaborative
management. However, merely requesting the same URL once would allow a user to
change or delete someone else's short URL, so it was rejected.

### Issue a Separate Short Key to Each User

This provides a simple authorization boundary and lets each user change their
mapping independently. It was rejected because it gives up the requirement to
share an existing short key for the same normalized URL and reduces cache
efficiency.

### Make Shared Mappings Immutable After Creation

This prevents unexpected destination changes from propagating to shared users,
but does not satisfy the creator-update requirement. It can work with a policy
that issues a new key on update, so reconsider it if copy-on-write is needed.

### Delete a Mapping Only When Its Last Owner Is Removed

This is suitable for removing per-user ownership relationships but differs from
the current contract that the creator deletes the global URL mapping. If users
need to remove an item from their own list, add a separate API distinct from
global deletion.

## Follow-up Work

- Write a migration that adds `creator_user_id` to `url_mappings`.
- Store a new mapping and its creator-ownership relationship in one transaction.
- Base update and deletion authorization in PostgreSQL and in-memory stores on
  the creator.
- Add separate update and deletion authorization tests for creators and shared
  users.
- Document the effects of creator updates and deletion on shared users in the
  API contract.
- Decide creator-account deletion and authority-transfer policies separately
  when account-lifecycle requirements arise.
