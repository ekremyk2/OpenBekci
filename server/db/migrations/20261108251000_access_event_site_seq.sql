-- +goose Up
-- sync_seq becomes a per-site counter assigned inside the inserting transaction
-- under the row lock of access_event_counter: gap-free for committed rows and
-- ordered by commit, so a poller that remembers the highest seq it saw never
-- skips a row that commits later with a lower number. (A separate table, so that
-- the allow-list projection holding access_site_state does not block ingestion.)
CREATE TABLE access_event_counter (
    org_id  uuid   NOT NULL REFERENCES organization (id),
    site_id uuid   NOT NULL,
    seq     bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (org_id, site_id),
    FOREIGN KEY (org_id, site_id) REFERENCES site (org_id, id)
);
SELECT openbekci_enable_rls('access_event_counter');
INSERT INTO access_event_counter (org_id, site_id, seq)
SELECT org_id, site_id, max(sync_seq) FROM access_event GROUP BY org_id, site_id;

ALTER TABLE access_event ALTER COLUMN sync_seq DROP IDENTITY;
ALTER TABLE access_event DROP CONSTRAINT access_event_sync_seq_key;
ALTER TABLE access_event ADD CONSTRAINT access_event_site_seq UNIQUE (org_id, site_id, sync_seq);

-- +goose StatementBegin
CREATE FUNCTION access_event_assign_seq() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
BEGIN
    INSERT INTO access_event_counter (org_id, site_id, seq) VALUES (NEW.org_id, NEW.site_id, 1)
    ON CONFLICT (org_id, site_id) DO UPDATE SET seq = access_event_counter.seq + 1
    RETURNING seq INTO NEW.sync_seq;
    RETURN NEW;
END;
$$;
-- +goose StatementEnd
CREATE TRIGGER access_event_seq BEFORE INSERT ON access_event
    FOR EACH ROW EXECUTE FUNCTION access_event_assign_seq();

-- +goose Down
DROP TRIGGER access_event_seq ON access_event;
DROP FUNCTION access_event_assign_seq();
ALTER TABLE access_event DROP CONSTRAINT access_event_site_seq;
ALTER TABLE access_event ADD CONSTRAINT access_event_sync_seq_key UNIQUE (sync_seq);
ALTER TABLE access_event ALTER COLUMN sync_seq ADD GENERATED ALWAYS AS IDENTITY (START WITH 1000000000);
DROP TABLE access_event_counter;
