ALTER TABLE url_mappings
    ADD COLUMN creator_user_id TEXT;

-- The initial schema did not record ownership order. Use a deterministic
-- existing owner when upgrading; a mapping without any owner fails the
-- subsequent NOT NULL constraint so inconsistent data is not hidden.
UPDATE url_mappings AS mappings
SET creator_user_id = owners.user_id
FROM (
    SELECT short_key, min(user_id) AS user_id
    FROM url_owners
    GROUP BY short_key
) AS owners
WHERE owners.short_key = mappings.short_key;

ALTER TABLE url_mappings
    ALTER COLUMN creator_user_id SET NOT NULL;
