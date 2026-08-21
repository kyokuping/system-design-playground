CREATE TABLE url_mappings (
    short_key TEXT PRIMARY KEY CHECK (char_length(short_key) = 7),
    normalized_url TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_accessed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    visits BIGINT NOT NULL DEFAULT 0
);

CREATE TABLE url_owners (
    user_id TEXT NOT NULL,
    short_key TEXT NOT NULL REFERENCES url_mappings(short_key) ON DELETE CASCADE,
    PRIMARY KEY (user_id, short_key)
);

CREATE TABLE id_allocators (
    name TEXT PRIMARY KEY,
    next_id BIGINT NOT NULL CHECK (next_id >= 0)
);
