-- +goose Up
-- The request-path lookups no longer need elevated functions: they run through the
-- platform pool (BYPASSRLS, never reachable from request handlers' tenant queries).
DROP FUNCTION IF EXISTS access_agent_org(uuid);
DROP FUNCTION IF EXISTS access_enrol_agent(bytea);
DROP FUNCTION IF EXISTS access_dirty_sites();
DROP FUNCTION IF EXISTS access_active_sites();

-- The two remaining SECURITY DEFINER functions delete or scrub the append-only
-- opening log. Only the platform/worker role may call them; the request-serving
-- role (openbekci_app) and PUBLIC may not, and the search path is pinned.
ALTER FUNCTION access_prune_events(uuid, timestamptz) SET search_path = public, pg_temp;
ALTER FUNCTION access_scrub_plate(uuid, uuid) SET search_path = public, pg_temp;
REVOKE ALL ON FUNCTION access_prune_events(uuid, timestamptz) FROM PUBLIC;
REVOKE ALL ON FUNCTION access_prune_events(uuid, timestamptz) FROM openbekci_app;
REVOKE ALL ON FUNCTION access_scrub_plate(uuid, uuid) FROM PUBLIC;
REVOKE ALL ON FUNCTION access_scrub_plate(uuid, uuid) FROM openbekci_app;
GRANT EXECUTE ON FUNCTION access_prune_events(uuid, timestamptz) TO openbekci_platform;
GRANT EXECUTE ON FUNCTION access_scrub_plate(uuid, uuid) TO openbekci_platform;

-- +goose Down
-- Forward-only: the previous grants were the defect.
SELECT 1;
