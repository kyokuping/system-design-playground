CREATE SEQUENCE url_mapping_revision_seq;

ALTER TABLE url_mappings
    ADD COLUMN revision BIGINT;

UPDATE url_mappings
SET revision = nextval('url_mapping_revision_seq');

ALTER TABLE url_mappings
    ALTER COLUMN revision SET NOT NULL,
    ALTER COLUMN revision SET DEFAULT nextval('url_mapping_revision_seq');

ALTER SEQUENCE url_mapping_revision_seq
    OWNED BY url_mappings.revision;
